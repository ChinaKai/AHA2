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
"""

import argparse
import os
import sys
import zipfile
from pathlib import Path

REQUIRED_INSTALLERS = ("AHA2-Setup-x64.exe", "AHA2-Setup-User-x64.exe")


def fail(message):
    print(f"assemble-windows-package: {message}", file=sys.stderr)
    return 1


def add_tree(archive, root, prefix):
    root = root.resolve()
    for dirpath, dirnames, filenames in os.walk(root, followlinks=False):
        dirnames.sort()
        for name in sorted(filenames):
            full = Path(dirpath) / name
            if full.is_symlink():
                raise ValueError(f"package input is a symlink: {full}")
            relative = full.relative_to(root).as_posix()
            archive.write(full, f"{prefix}/{relative}")


def assemble(package_dir, installer_dir, output):
    package_dir = Path(package_dir)
    installer_dir = Path(installer_dir)
    output = Path(output)
    if not (package_dir / "release-manifest.json").is_file():
        raise ValueError(f"package is missing release-manifest.json: {package_dir}")
    output.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        add_tree(archive, package_dir, "dist")
        for name in REQUIRED_INSTALLERS:
            source = installer_dir / name
            if not source.is_file():
                raise ValueError(f"installer is missing: {source}")
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
