"""Fixed POSIX workspace reader invoked through the SSH/WSL runner."""
import base64
import datetime
import json
import os
import re
import stat
import subprocess
import sys
import threading
import time

DIRECTORY = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
REPO_TIMEOUT = 5
REPO_METADATA_LIMIT = 8 << 20
GITLINK_LIMIT = 8 << 20


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


def line_ending_args(env, git_dir):
    config_env = env.copy()
    for key in ("GIT_CONFIG_SYSTEM", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM"):
        if key in os.environ:
            config_env[key] = os.environ[key]
    try:
        data, status, truncated = run_git_command(
            ["git", "--no-pager", "--git-dir=" + git_dir, "--work-tree=.",
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


def git_environment():
    env = {key: value for key, value in os.environ.items() if not key.upper().startswith("GIT_")}
    return env


def isolated_git_environment(env):
    env.update(
        LC_ALL="C", GIT_OPTIONAL_LOCKS="0", GIT_CONFIG_NOSYSTEM="1",
        GIT_CONFIG_GLOBAL=os.devnull, GIT_ATTR_NOSYSTEM="1", GIT_NO_LAZY_FETCH="1",
        GIT_ALLOW_PROTOCOL="", GIT_TERMINAL_PROMPT="0",
    )
    return env


def run_git(req, git_dir):
    env = git_environment()
    line_endings = line_ending_args(env, git_dir) if req.get("read_line_endings") else []
    try:
        data, status, truncated = run_git_command(
            ["git", *line_endings, "--git-dir=" + git_dir, "-c", "core.hooksPath=" + os.devnull, *req["args"]],
            isolated_git_environment(env), req["limit"], timeout=12,
        )
        return {"data": encode(data), "exit": status, "truncated": truncated}
    except OSError:
        return {"error": "git"}


def encode(data):
    return base64.b64encode(data).decode("ascii")


def valid_repository_path(relative):
    try:
        if not relative or len(relative.encode("utf-8")) > 4096 or any(char in relative for char in "\\:\x00"):
            return False
    except UnicodeEncodeError:
        return False
    blocked = {
        ".git", ".aha2-context", ".env", ".envrc", ".ssh", ".gnupg", ".aws", ".kube",
        ".npmrc", ".netrc", ".pypirc", ".git-credentials", ".docker", "credentials",
        "credentials.json", "secrets", "secrets.json", "token", "tokens", "auth.json",
        "id_rsa", "id_ed25519", "id_ecdsa",
    }
    prefixes = (".env.", "credential.", "credentials.", "secret.", "secrets.", "token.", "tokens.")
    suffixes = (".env", ".pem", ".key", ".p12", ".pfx", ".kdbx", ".jks")
    return all(
        part and part.rstrip(". ") == part and part.lower() not in blocked
        and not part.lower().startswith(prefixes) and not part.lower().endswith(suffixes)
        for part in relative.split("/")
    )


def git_marker(root):
    try:
        info = os.stat(".git", dir_fd=root, follow_symlinks=False)
    except FileNotFoundError:
        return False
    if not (stat.S_ISREG(info.st_mode) or stat.S_ISDIR(info.st_mode)):
        raise OSError("invalid Git marker")
    return True


def repository_git(git_dir, limit, *args, deadline=None):
    timeout = 2 if deadline is None else min(2, deadline - time.monotonic())
    if timeout <= 0:
        return b"", -1, False
    return run_git_command(
        ["git", "--no-pager", "--git-dir=" + git_dir, "--work-tree=.", "-c", "core.fsmonitor=false",
         "-c", "core.hooksPath=" + os.devnull, *args],
        isolated_git_environment(git_environment()), limit, timeout=timeout,
    )


def workspace_module_dirs(deadline=None):
    dirs = []
    for flag in ("--git-common-dir", "--git-dir"):
        data, code, cut = repository_git(".git", 4096, "rev-parse", "--path-format=absolute", flag, deadline=deadline)
        git_dir = os.fsdecode(data.rstrip(b"\n"))
        if not cut and code == 0 and os.path.isabs(git_dir):
            dirs.append(os.path.join(git_dir, "modules"))
    return dirs


def submodule_git_dir(root, scoped, relative, root_path, module_dirs):
    if not relative or stat.S_ISDIR(os.stat(".git", dir_fd=scoped, follow_symlinks=False).st_mode):
        return ".git"
    # Gitfiles can point into the shared Git directory or the linked
    # worktree's own Git directory.
    handle = open_path(scoped, ".git", os.O_RDONLY | os.O_NONBLOCK)
    with os.fdopen(handle, "rb") as file:
        if not stat.S_ISREG(os.fstat(file.fileno()).st_mode):
            return None
        data = file.read(4107).rstrip(b"\r\n")
    if not data.startswith(b"gitdir: "):
        return None
    raw = os.fsdecode(data[8:])
    if not raw or len(data[8:]) > 4096 or any(char in raw for char in "\r\n\x00"):
        return None
    target = os.path.normpath(os.path.join(root_path, relative, raw))
    if os.path.realpath(target) != target or not os.path.isdir(target):
        return None
    if any(target.startswith(modules + "/") for modules in module_dirs):
        return target
    parent = relative.rpartition("/")[0]
    while parent:
        marker = parent + "/.git"
        if target.startswith(os.path.join(root_path, marker, "modules") + "/"):
            try:
                os.close(open_path(root, marker, DIRECTORY))
                return target
            except OSError:
                pass
        parent = parent.rpartition("/")[0]
    return None


def registered_gitlinks(git_dir, deadline):
    children = set()
    try:
        index, code, truncated = repository_git(git_dir, GITLINK_LIMIT, "ls-files", "--stage", "-z", "--", deadline=deadline)
        truncated = truncated or code != 0
        for record in index.split(b"\x00")[:-1]:
            header, _, relative = record.partition(b"\t")
            name = os.fsdecode(relative)
            if header.startswith(b"160000 ") and valid_repository_path(name):
                children.add(name)
    except OSError:
        truncated = True

    return sorted(children), truncated


def repository_details(root, relative, root_path, module_dirs, deadline):
    item = {
        "id": relative, "path": relative, "name": os.path.basename(relative or root_path),
        "initialized": False, "readable": False, "head": "", "branch": "", "detached": False,
    }
    try:
        handle = open_path(root, relative, DIRECTORY)
    except OSError:
        return item, [], False
    try:
        os.fchdir(handle)
        item["initialized"] = git_marker(handle)
        if not item["initialized"]:
            return item, [], False
        git_dir = submodule_git_dir(root, handle, relative, root_path, module_dirs)
        if git_dir is None:
            return item, [], False
        head, code, cut = repository_git(git_dir, 1024, "rev-parse", "--verify", "--quiet", "HEAD", deadline=deadline)
        head = head.strip()
        if cut or (code != 1 and (code != 0 or not re.fullmatch(b"(?:[0-9a-f]{40}|[0-9a-f]{64})", head))):
            return item, [], False
        branch, code, cut = repository_git(git_dir, 4096, "symbolic-ref", "--quiet", "HEAD", deadline=deadline)
        if cut or code not in (0, 1):
            return item, [], False
        if branch.endswith(b"\n"):
            branch = branch[:-1]
        if branch.startswith(b"refs/heads/"):
            branch = branch[len(b"refs/heads/"):]
        item.update(readable=True, head=head.decode("ascii"), detached=code == 1 and bool(head),
                    branch=branch.decode("utf-8", errors="replace"))
        children, cut = registered_gitlinks(git_dir, deadline)
        return item, children, cut
    except OSError:
        return item, [], True
    finally:
        os.close(handle)


def repositories(root):
    if not git_marker(root):
        return {}
    root_path = os.getcwd()
    deadline = time.monotonic() + REPO_TIMEOUT
    module_dirs = workspace_module_dirs(deadline)
    items, pending, seen = [], [""], set()
    size = 0
    truncated = size_reached = False
    while pending and time.monotonic() < deadline:
        relative = pending.pop()
        if relative in seen:
            continue
        seen.add(relative)
        item, children, cut = repository_details(root, relative, root_path, module_dirs, deadline)
        size += len(json.dumps(item, ensure_ascii=False).encode("utf-8")) + 2
        if size > REPO_METADATA_LIMIT:
            size_reached = True
            break
        items.append(item)
        truncated = truncated or cut
        for child in reversed(children):
            full_path = relative + "/" + child if relative else child
            if valid_repository_path(full_path):
                pending.append(full_path)
    if not items or not items[0]["readable"]:
        return {"error": "git"}
    deadline_reached = time.monotonic() >= deadline
    return {
        "repositories": items, "repositoriesTruncated": truncated or deadline_reached or size_reached,
        "repositoryDeadlineReached": deadline_reached, "repositorySizeReached": size_reached,
    }


def read(req):
    try:
        os.chdir(req["root"])
        root = os.open(".", DIRECTORY)
    except OSError:
        return {"error": "root"}
    scoped = root
    try:
        git_dir = ".git"
        op = req["op"]
        if op == "repositories":
            try:
                return repositories(root)
            except OSError:
                return {"error": "git"}
        relative = req.get("repo")
        if relative:
            if not valid_repository_path(relative):
                return {"error": "repo"}
            try:
                scoped = open_path(root, relative, DIRECTORY)
                if not git_marker(scoped):
                    return {"error": "repo"}
                if not stat.S_ISDIR(os.stat(".git", dir_fd=scoped, follow_symlinks=False).st_mode):
                    git_dir = submodule_git_dir(root, scoped, relative, os.getcwd(), workspace_module_dirs())
                    if git_dir is None:
                        return {"error": "repo"}
                os.fchdir(scoped)
            except OSError:
                return {"error": "repo"}
        if op == "root":
            return {}
        if op == "marker":
            try:
                return {"marker": git_marker(scoped)}
            except OSError:
                return {"error": "git"}
        if op == "tree":
            return list_tree(scoped, req.get("path", ""))
        if op == "file":
            return read_file(scoped, req["path"], req["limit"])
        if op == "git":
            return run_git(req, git_dir)
        return {"error": "path"}
    except FileNotFoundError:
        return {"error": "missing"}
    except OSError:
        return {"error": "path"}
    finally:
        if scoped != root:
            os.close(scoped)
        os.close(root)


frame = encode(json.dumps(read(json.load(sys.stdin)), ensure_ascii=False).encode("utf-8"))
for offset in range(0, len(frame), 16384):
    print(frame[offset:offset + 16384])
