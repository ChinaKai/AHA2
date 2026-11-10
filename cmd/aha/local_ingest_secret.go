package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/secrets"
)

// storeLocalIngestSecret provisions the bearer secret for the loopback owner
// message ingest endpoint. The value is read from stdin and never printed.
// AHA2 loads the Secret Store at startup, so the operator must stop the
// service before running this and restart it afterwards.
func storeLocalIngestSecret(args []string) error {
	return storeLocalIngestSecretFrom(os.Stdin, os.Stdout, args)
}

func storeLocalIngestSecretFrom(stdin io.Reader, stdout io.Writer, args []string) error {
	flags := flag.NewFlagSet("aha2 local-ingest-secret", flag.ContinueOnError)
	dataDir := flags.String("data-dir", envOr("AHA2_DATA_DIR", ".data"), "AHA2 data directory")
	clear := flags.Bool("clear", false, "remove the stored secret instead of reading one from stdin")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	absoluteDataDir, err := filepath.Abs(*dataDir)
	if err != nil {
		return err
	}
	secretStore, err := secrets.Open(filepath.Join(absoluteDataDir, "secrets.json"))
	if err != nil {
		return err
	}
	if *clear {
		if err := secretStore.DeleteMany([]string{app.LocalOwnerIngestSecretRef}); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "local owner ingest secret cleared; restart AHA2 to apply")
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(stdin, 4097))
	if err != nil {
		return fmt.Errorf("read secret from stdin: %w", err)
	}
	value := strings.TrimSpace(string(data))
	if len(value) < 32 {
		return fmt.Errorf("secret must be at least 32 bytes")
	}
	if len(value) > 4096 {
		return fmt.Errorf("secret is too long")
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e {
			return fmt.Errorf("secret must be printable ASCII without whitespace")
		}
	}
	if err := secretStore.PutMany(map[string]string{app.LocalOwnerIngestSecretRef: value}); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "local owner ingest secret stored; restart AHA2 to apply")
	return nil
}
