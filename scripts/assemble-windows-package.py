#!/usr/bin/env python3
"""Assemble the deployable Windows PackageDir layout as a zip.

The archive contains file entries only:
  dist/<package files>
  installer/AHA2-Setup-x64.exe
  installer/AHA2-Setup-User-x64.exe

No directory entries are written. Compress-Archive records directory metadata,
which adds no deployment value and gives extraction another path shape to
validate; a file-only zip keeps the Release archive minimal and lets
verify-windows-package-archive.py reject directory members outright. The
Release workflow and the round-trip test both run this script, so the tested
assembly path is the one that ships.

Every input is inspected with lstat before it is written. Symlinks, Windows
junctions and other reparse points, and non-regular files are rejected rather
than followed or skipped: following a link would package bytes that the
release manifest never hashed, and silently skipping one would ship a package
whose contents differ from what the installers were verified against.
"""

import argparse
import os
import stat
import sys
import zipfile
from pathlib import Path

REQUIRED_INSTALLERS = ("AHA2-Setup-x64.exe", "AHA2-Setup-User-x64.exe")

# Windows exposes junctions and other reparse points through
# st_file_attributes/st_reparse_tag; on POSIX and for Windows symbolic links
# os.lstat() sets S_ISLNK. Both signals are checked so a junction is rejected
# even on Python versions where it is not reported as a symlink.
FILE_ATTRIBUTE_REPARSE_POINT = 0x0400
IO_REPARSE_TAG_MOUNT_POINT = 0xA0000003
IO_REPARSE_TAG_SYMLINK = 0xA000000C


def fail(message):
    print(f"assemble-windows-package: {message}", file=sys.stderr)
    return 1


def reparse_kind(info):
    """Describe why a stat result is a link-like entry, or return None."""
    if stat.S_ISLNK(info.st_mode):
        return "symlink"
    if getattr(info, "st_file_attributes", 0) & FILE_ATTRIBUTE_REPARSE_POINT:
        tag = getattr(info, "st_reparse_tag", 0)
        if tag == IO_REPARSE_TAG_MOUNT_POINT:
            return "junction (reparse point)"
        if tag == IO_REPARSE_TAG_SYMLINK:
            return "symlink (reparse point)"
        return f"reparse point (tag {tag:#010x})"
    return None


def lstat_input(path, label):
    try:
        return os.lstat(path)
    except FileNotFoundError:
        raise ValueError(f"{label} is missing: {path}") from None
    except OSError as error:
        raise ValueError(f"{label} cannot be inspected: {path}: {error.strerror or error}") from error


def require_directory(path, label):
    info = lstat_input(path, label)
    kind = reparse_kind(info)
    if kind:
        raise ValueError(f"{label} is a {kind}: {path}")
    if not stat.S_ISDIR(info.st_mode):
        raise ValueError(f"{label} is not a directory: {path}")


def require_regular_file(path, label):
    info = lstat_input(path, label)
    kind = reparse_kind(info)
    if kind:
        raise ValueError(f"{label} is a {kind}: {path}")
    if not stat.S_ISREG(info.st_mode):
        raise ValueError(f"{label} is not a regular file: {path}")
    return info


def add_tree(archive, root, prefix):
    root = Path(root)
    for dirpath, dirnames, filenames in os.walk(root, followlinks=False):
        dirnames.sort()
        for name in dirnames:
            # os.walk(followlinks=False) lists a directory link but does not
            # descend into it; skipping it silently would leave its files out
            # of the archive without any error.
            full = Path(dirpath) / name
            kind = reparse_kind(lstat_input(full, "package input"))
            if kind:
                raise ValueError(f"package input directory is a {kind}: {full}")
        for name in sorted(filenames):
            full = Path(dirpath) / name
            require_regular_file(full, "package input")
            relative = full.relative_to(root).as_posix()
            archive.write(full, f"{prefix}/{relative}")


def assemble(package_dir, installer_dir, output):
    package_dir = Path(package_dir)
    installer_dir = Path(installer_dir)
    output = Path(output)
    require_directory(package_dir, "package directory")
    require_directory(installer_dir, "installer directory")
    require_regular_file(package_dir / "release-manifest.json", "package manifest")
    output.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        add_tree(archive, package_dir, "dist")
        for name in REQUIRED_INSTALLERS:
            source = installer_dir / name
            require_regular_file(source, "installer")
            archive.write(source, f"installer/{name}")
    return output


def main(argv):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--package-dir", required=True)
    parser.add_argument("--installer-dir", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args(argv)
    try:
        output = assemble(args.package_dir, args.installer_dir, args.output)
    except (OSError, ValueError) as error:
        return fail(str(error))
    print(f"Assembled {output}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
