"""Fixed POSIX workspace reader invoked through the SSH/WSL runner."""
import base64
import datetime
import json
import os
import stat
import subprocess
import sys
import threading

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


def run_git_command(argv, env, limit, timeout=None):
    with subprocess.Popen(
        argv, env=env, stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, shell=False,
    ) as process:
        timer = None
        if timeout is not None:
            timer = threading.Timer(timeout, process.kill)
            timer.start()
        try:
            data = process.stdout.read(limit + 1)
            truncated = len(data) > limit
            if truncated:
                process.kill()
            return data[:limit], process.wait(), truncated
        finally:
            if timer is not None:
                timer.cancel()


def line_ending_args(env):
    config_env = env.copy()
    for key in ("GIT_CONFIG_SYSTEM", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM"):
        if key in os.environ:
            config_env[key] = os.environ[key]
    try:
        data, status, truncated = run_git_command(
            ["git", "--no-pager", "--git-dir=.git", "--work-tree=.",
             "config", "--includes", "--null", "--get-regexp", r"^core\.(autocrlf|eol)$"],
            config_env, 4096, timeout=0.5,
        )
    except OSError:
        return []
    if status != 0 or truncated:
        return []

    # Later values take precedence. Only these keys and enum values may
    # cross from the normal config into the isolated viewer command.
    values = {}
    for record in data.split(b"\x00"):
        key, _, value = record.partition(b"\n")
        values[key] = value.lower()
    args = []
    for key, allowed in ((b"core.autocrlf", (b"true", b"false", b"input")),
                         (b"core.eol", (b"lf", b"crlf", b"native"))):
        value = values.get(key)
        if value in allowed:
            args.extend(("-c", key.decode("ascii") + "=" + value.decode("ascii")))
    return args


def run_git(req):
    env = {key: value for key, value in os.environ.items() if not key.upper().startswith("GIT_")}
    line_endings = line_ending_args(env) if req.get("read_line_endings") else []
    env.update(
        LC_ALL="C", GIT_OPTIONAL_LOCKS="0", GIT_CONFIG_NOSYSTEM="1",
        GIT_CONFIG_GLOBAL=os.devnull, GIT_ATTR_NOSYSTEM="1", GIT_NO_LAZY_FETCH="1",
        GIT_ALLOW_PROTOCOL="", GIT_TERMINAL_PROMPT="0",
    )
    try:
        data, status, truncated = run_git_command(["git", *line_endings, *req["args"]], env, req["limit"])
        return {"data": encode(data), "exit": status, "truncated": truncated}
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
