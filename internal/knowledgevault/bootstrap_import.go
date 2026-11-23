package knowledgevault

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// BootstrapFingerprint includes the vault files and directory structure,
// including content the index skips. Local runtime state under .aha does not
// affect whether the scaffold is still unused.
func BootstrapFingerprint(vaultPath string) (string, error) {
	const maxEntries, maxFileSize = 64, 128 << 10
	hash := sha256.New()
	count := 0
	err := filepath.WalkDir(vaultPath, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		count++
		if count > maxEntries || (!entry.IsDir() && (path == vaultPath || !entry.Type().IsRegular())) {
			return ErrTargetNotEmpty
		}
		relative, err := filepath.Rel(vaultPath, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			fmt.Fprintf(hash, "dir %q\n", filepath.ToSlash(relative))
			// Keep the directory entry in the hash so existing fingerprints of
			// the scaffold, recorded before auto-sync starts, still match.
			if relative == stateDirectory {
				return filepath.SkipDir
			}
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		contents := sha256.New()
		size, err := io.Copy(contents, io.LimitReader(file, maxFileSize+1))
		if err != nil {
			return err
		}
		if size > maxFileSize {
			return ErrTargetNotEmpty
		}
		fmt.Fprintf(hash, "file %q %x\n", filepath.ToSlash(relative), contents.Sum(nil))
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// CanReplaceBootstrap reports whether the live files still match the exact
// automatic scaffold recorded in the control-plane database.
func (m *Manager) CanReplaceBootstrap(expected string) bool {
	if len(expected) != sha256.Size*2 {
		return false
	}
	actual, err := BootstrapFingerprint(m.config.VaultPath)
	return err == nil && actual == expected
}

// ImportOverBootstrap verifies the replacement before moving the live vault.
// The original directory is kept as a backup, including edits made by an
// external editor during the final rename. record persists verified metadata
// only after the replacement has an index and an open reader.
func (m *Manager) ImportOverBootstrap(ctx context.Context, expected string, options ExistingVaultImportOptions, record func(ExistingVaultImportReport) error) (ExistingVaultImportReport, error) {
	if len(options.KnownProjects) != 0 || !m.CanReplaceBootstrap(expected) {
		return ExistingVaultImportReport{}, ErrTargetNotEmpty
	}
	if strings.EqualFold(strings.TrimSpace(options.Kind), "local") && strings.TrimSpace(options.Path) != "" {
		source, err := filepath.Abs(strings.TrimSpace(options.Path))
		if err != nil {
			return ExistingVaultImportReport{}, err
		}
		if samePath(source, m.config.VaultPath) || insidePath(source, m.config.VaultPath) || insidePath(m.config.VaultPath, source) {
			return ExistingVaultImportReport{}, fmt.Errorf("import source and target must not contain each other")
		}
	}
	stage, err := os.MkdirTemp(filepath.Dir(m.config.VaultPath), ".aha-vault-import-")
	if err != nil {
		return ExistingVaultImportReport{}, err
	}
	keepBackup := false
	defer func() {
		if keepBackup {
			_ = os.RemoveAll(filepath.Join(stage, "index"))
		} else {
			_ = os.RemoveAll(stage)
		}
	}()
	staged := m.config
	staged.VaultPath = filepath.Join(stage, "vault")
	staged.IndexPath = filepath.Join(stage, "index", "index.db")
	report, err := ImportExistingVault(ctx, staged, options)
	report.VaultPath = m.config.VaultPath
	if err != nil {
		return report, err
	}

	m.gitMu.Lock()
	defer m.gitMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if !m.CanReplaceBootstrap(expected) {
		return report, ErrTargetNotEmpty
	}
	wasEnabled := m.enabled
	if m.reader != nil {
		reader := m.reader
		m.reader = nil
		if err := reader.Close(); err != nil {
			m.reopenLocked(ctx)
			return report, err
		}
	}
	backup := filepath.Join(stage, "previous-vault")
	if err := os.Rename(m.config.VaultPath, backup); err != nil {
		m.reopenLocked(ctx)
		return report, err
	}
	keepBackup = true
	report.BackupPath = backup
	if err := os.Rename(staged.VaultPath, m.config.VaultPath); err != nil {
		restoreErr := os.Rename(backup, m.config.VaultPath)
		if restoreErr == nil {
			keepBackup = false
			report.BackupPath = ""
		}
		m.reopenLocked(ctx)
		return report, errors.Join(err, restoreErr)
	}

	m.enabled = true
	_, err = m.rebuildLocked(ctx)
	if err == nil {
		err = record(report)
	}
	if err == nil {
		m.source = SourceDatabase
		return report, nil
	}
	if m.reader != nil {
		_ = m.reader.Close()
		m.reader = nil
	}
	m.enabled = wasEnabled
	if moveErr := os.Rename(m.config.VaultPath, staged.VaultPath); moveErr != nil {
		m.reopenLocked(ctx)
		return report, errors.Join(err, moveErr)
	}
	if restoreErr := os.Rename(backup, m.config.VaultPath); restoreErr != nil {
		m.reopenLocked(ctx)
		return report, errors.Join(err, restoreErr)
	}
	keepBackup = false
	report.BackupPath = ""
	_, rebuildErr := m.rebuildLocked(context.WithoutCancel(ctx))
	return report, errors.Join(err, rebuildErr)
}
