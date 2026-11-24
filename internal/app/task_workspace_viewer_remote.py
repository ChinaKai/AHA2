"""Fixed POSIX workspace reader invoked through the SSH/WSL runner."""
import base64
import datetime
import json
import os
import stat
import subprocess
import sys

DIRECTORY = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW


def open_path(root, relative, flags):
    parts = relative.split("/")
    current = os.dup(root)
    try:
        for part in parts[:-1]:
            child = os.open(part, DIRECTORY, dir_fd=current)
            os.close(current)
            current = child
        return os.open(parts[-1] or ".", flags | os.O_NOFOLLOW, dir_fd=current)
    finally:
        os.close(current)


def list_tree(root, relative):
    handle = open_path(root, relative, DIRECTORY)
    entries = []
    truncated = False
    try:
        with os.scandir(handle) as children:
            for index, child in enumerate(children):
                if index == 5000:
                    truncated = True
                    break
                try:
                    info = child.stat(follow_symlinks=False)
                except FileNotFoundError:
                    continue
                kind = "other"
                if stat.S_ISDIR(info.st_mode):
                    kind = "directory"
                elif stat.S_ISREG(info.st_mode):
                    kind = "file"
                elif stat.S_ISLNK(info.st_mode):
                    kind = "symlink"
                name = child.name
                try:
                    name.encode("utf-8")
                except UnicodeEncodeError:
                    continue
                entries.append({
                    "path": relative + "/" + name if relative else name,
                    "kind": kind, "size": info.st_size,
                    "mtime": datetime.datetime.fromtimestamp(info.st_mtime, datetime.timezone.utc).isoformat(),
                })
    finally:
        os.close(handle)
    return {"tree": {"entries": entries, "truncated": truncated}}


def read_file(root, relative, limit):
    handle = open_path(root, relative, os.O_RDONLY | os.O_NONBLOCK)
    with os.fdopen(handle, "rb") as file:
        info = os.fstat(file.fileno())
        if not stat.S_ISREG(info.st_mode):
            return {"error": "path"}
        if info.st_size > limit:
            return {"error": "large"}
        return {"data": encode(file.read(limit + 1))}


def run_git(req):
    env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
    env.update(
        LC_ALL="C", GIT_OPTIONAL_LOCKS="0", GIT_CONFIG_NOSYSTEM="1",
        GIT_CONFIG_GLOBAL=os.devnull, GIT_ATTR_NOSYSTEM="1", GIT_NO_LAZY_FETCH="1",
        GIT_ALLOW_PROTOCOL="", GIT_TERMINAL_PROMPT="0",
    )
    try:
        with subprocess.Popen(
            ["git", *req["args"]], env=env, stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, shell=False,
        ) as process:
            limit = req["limit"]
            data = process.stdout.read(limit + 1)
            truncated = len(data) > limit
            if truncated:
                process.kill()
            return {"data": encode(data[:limit]), "exit": process.wait(), "truncated": truncated}
    except OSError:
        return {"error": "git"}


def encode(data):
    return base64.b64encode(data).decode("ascii")


def read(req):
    try:
        os.chdir(req["root"])
        root = os.open(".", DIRECTORY)
    except OSError:
        return {"error": "root"}
    try:
        op = req["op"]
        if op == "root":
            return {}
        if op == "marker":
            try:
                info = os.stat(".git", dir_fd=root, follow_symlinks=False)
            except FileNotFoundError:
                return {"marker": False}
            if stat.S_ISREG(info.st_mode) or stat.S_ISDIR(info.st_mode):
                return {"marker": True}
            return {"error": "git"}
        if op == "tree":
            return list_tree(root, req.get("path", ""))
        if op == "file":
            return read_file(root, req["path"], req["limit"])
        if op == "git":
            return run_git(req)
        return {"error": "path"}
    except FileNotFoundError:
        return {"error": "missing"}
    except OSError:
        return {"error": "path"}
    finally:
        os.close(root)


frame = encode(json.dumps(read(json.load(sys.stdin)), ensure_ascii=False).encode("utf-8"))
for offset in range(0, len(frame), 16384):
    print(frame[offset:offset + 16384])
