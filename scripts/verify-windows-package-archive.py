#!/usr/bin/env python3
"""Verify the deployable Windows package archive published by the Release job.

The archive must be the official PackageDir layout:
  installer/AHA2-Setup-x64.exe
  installer/AHA2-Setup-User-x64.exe
  dist/<package files>
  dist/release-manifest.json

The manifest identity, scope and per-file hashes are re-checked against the
archive bytes, and the dist/ tree is compared bidirectionally with the
manifest. A zip containing only the installers and an empty manifest is
rejected.
"""

import argparse
import datetime
import hashlib
import json
import re
import sys
import zipfile
from pathlib import Path

SCOPE = json.loads((Path(__file__).resolve().parent / "windows-package-scope.json").read_text(encoding="utf-8"))
REQUIRED_PACKAGE_FILES = SCOPE["required"]
EXACT_PACKAGE_FILES = set(SCOPE["exact"])
PACKAGE_FILE_PREFIXES = tuple(SCOPE["prefixes"])
REQUIRED_ARCHIVE_FILES = {
    "installer/AHA2-Setup-x64.exe",
    "installer/AHA2-Setup-User-x64.exe",
    "dist/release-manifest.json",
}
MANIFEST_NAME = "dist/release-manifest.json"


def fail(message):
    raise ValueError(message)


def normalize_version(value):
    if not isinstance(value, str) or not value.strip():
        fail("manifest version must be a non-empty string")
    normalized = value.strip()
    if normalized[:1] in ("v", "V"):
        normalized = normalized[1:]
    if not normalized:
        fail("manifest version must name a version after the v prefix")
    return normalized


def normalize_relative(value):
    if not isinstance(value, str) or not value.strip():
        fail("manifest path must be a non-empty string")
    if value.startswith("/") or re.match(r"^[A-Za-z]:", value):
        fail(f"manifest path must be relative to the package root: {value}")
    parts = [part for part in re.split(r"[\\/]+", value) if part]
    if not parts or "." in parts or ".." in parts:
        fail(f"manifest path escapes the package root: {value}")
    return "/".join(parts)


def package_path_in_scope(path):
    return path in EXACT_PACKAGE_FILES or any(path.startswith(prefix) for prefix in PACKAGE_FILE_PREFIXES)


def parse_identity(manifest, version, commit, expected_web_version):
    if not isinstance(manifest, dict):
        fail("manifest must be a JSON object")
    if manifest.get("schema_version") != 1:
        fail(f"manifest schema_version {manifest.get('schema_version')} != 1")
    if manifest.get("product") != "AHA2":
        fail(f"manifest product {manifest.get('product')} != AHA2")
    if normalize_version(manifest.get("version")) != normalize_version(version):
        fail(f"manifest version {manifest.get('version')} does not match {version}")
    manifest_commit = manifest.get("commit")
    if not isinstance(manifest_commit, str) or not re.match(r"^[0-9a-fA-F]{12,40}$", manifest_commit):
        fail(f"manifest commit is missing or malformed: {manifest_commit}")
    if manifest_commit.lower() != commit.lower():
        fail(f"manifest commit {manifest_commit} does not match expected commit {commit}")
    built_at = manifest.get("built_at")
    if not isinstance(built_at, str) or not built_at.strip():
        fail("manifest built_at is missing")
    try:
        datetime.datetime.fromisoformat(built_at.replace("Z", "+00:00"))
    except ValueError:
        fail(f"manifest built_at is not an RFC3339 timestamp: {built_at}")
    generator = manifest.get("generator")
    if not isinstance(generator, dict):
        fail("manifest generator must be an object")
    if not isinstance(generator.get("script"), str) or not generator["script"].strip():
        fail("manifest generator.script is missing")
    web_version = generator.get("web_version")
    if not isinstance(web_version, str) or not web_version.strip():
        fail("manifest generator.web_version is missing")
    pattern = re.compile(
        r"^v" + re.escape(normalize_version(version)) + r"\.(\d{8})\.([0-9a-f]{12})(\.dirty)?$"
    )
    match = pattern.match(web_version)
    if not match:
        fail(f"manifest generator.web_version {web_version} must look like v{version}.<yyyymmdd>.<commit12>")
    try:
        datetime.datetime.strptime(match.group(1), "%Y%m%d")
    except ValueError:
        fail(f"manifest generator.web_version {web_version} carries an invalid build date")
    if match.group(2) != manifest_commit[:12].lower():
        fail(f"manifest generator.web_version commit {match.group(2)} does not match manifest commit {manifest_commit}")
    if expected_web_version is not None and web_version != expected_web_version:
        fail(f"manifest generator.web_version {web_version} != {expected_web_version}")


def parse_files(manifest):
    files = manifest.get("files")
    if not isinstance(files, list) or not files:
        fail("manifest files must list at least one package file")
    entries = {}
    for entry in files:
        if not isinstance(entry, dict):
            fail("manifest file entry must be an object")
        raw_path = entry.get("path")
        path = normalize_relative(raw_path)
        if path != raw_path:
            fail(f"manifest path is not normalized: {raw_path}")
        if path == "release-manifest.json":
            fail("manifest must not list itself")
        if path in entries:
            fail(f"manifest file is listed twice: {path}")
        size = entry.get("size")
        if isinstance(size, bool) or not isinstance(size, int) or size < 0:
            fail(f"manifest size is invalid for {path}")
        if not isinstance(entry.get("sha256"), str) or not re.match(r"^[0-9a-f]{64}$", entry["sha256"]):
            fail(f"manifest has a malformed sha256 for {path}")
        if not package_path_in_scope(path):
            fail(f"manifest file is outside the package install scope: {path}")
        entries[path] = entry
    for required in REQUIRED_PACKAGE_FILES:
        if required not in entries:
            fail(f"manifest is missing required package file: {required}")
    return entries


def verify_archive(archive_path, version, commit, expected_web_version):
    with zipfile.ZipFile(archive_path) as archive:
        names = set(archive.namelist())
        for required in sorted(REQUIRED_ARCHIVE_FILES):
            if required not in names:
                fail(f"Windows package archive is missing {required}")
        if MANIFEST_NAME not in names:
            fail(f"Windows package archive is missing {MANIFEST_NAME}")
        manifest = json.loads(archive.read(MANIFEST_NAME).decode("utf-8"))
        parse_identity(manifest, version, commit, expected_web_version)
        entries = parse_files(manifest)

        archive_dist = {
            name[len("dist/"):]
            for name in names
            if name.startswith("dist/") and not name.endswith("/") and name != MANIFEST_NAME
        }
        for path in entries:
            if path not in archive_dist:
                fail(f"Windows package archive is missing manifest entry dist/{path}")
        for path in sorted(archive_dist - set(entries)):
            fail(f"Windows package archive contains dist/{path}, which the manifest does not list")

        for path, entry in entries.items():
            data = archive.read(f"dist/{path}")
            if len(data) != entry["size"]:
                fail(f"Windows package archive size for dist/{path} = {len(data)}, manifest says {entry['size']}")
            if hashlib.sha256(data).hexdigest() != entry["sha256"]:
                fail(f"Windows package archive SHA256 mismatch for dist/{path}")
        return len(entries)


def main(argv):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive")
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--expected-web-version")
    args = parser.parse_args(argv)
    try:
        count = verify_archive(args.archive, args.version, args.commit, args.expected_web_version)
    except (OSError, ValueError, zipfile.BadZipFile, json.JSONDecodeError) as error:
        print(f"verify-windows-package-archive: {error}", file=sys.stderr)
        return 1
    print(f"Windows package archive verified ({count} manifest files)")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
