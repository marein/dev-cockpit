#!/usr/bin/env python3
# Deterministic fake language server for the editor-lsp.js runner. Speaks
# just enough framed LSP over stdio: initialize handshake, full text document
# sync, textDocument/definition and textDocument/references, so the suite
# never depends on a real gopls install. Start it as "gopls" via a wrapper
# script on the throwaway instance's PATH (see tests/e2e/README.md).
#
# Contract with the runner: the definition of anything is lib.go line 2
# (0-based) character 5, which is where the runner's lib.go declares
# IntelTarget; a position on line 0 has no definition; references answer the
# two calls in use.go, the declaration in lib.go, and one location outside
# the project root that the server must drop and count.
#
# Two files of the runner ask for a target outside the project instead, and
# which one is read off the asked document, so the rules above stand
# unchanged: from `deps.go` the definition lies in the module cache the
# cockpit binds (GOMODCACHE, written into the container's environment; the
# fake writes the dependency file there itself on the first handshake), from
# `stdlib.go` it lies in the image, under the standard library root.
#
# The chain continues out of those files, which is what a lookup inside a
# read only tab asks for: from the dependency in the module cache on into
# the standard library, and from there back into the project, which is the
# rule above answering for every other document.
#
# The workspace root steers the indexing announcement, which is what the
# lifetime and indicator checks stand on. A file `.fake-lsp-slow` in the root
# makes the fake announce a slow indexing (~3s) with percentage reports, or
# without any percentage when the file's content starts with "nopct"; while
# it runs, references answer only the first location, the way a real index
# answers partially. Every start with that marker appends one line to
# `.fake-lsp-starts` in the root, so the runner reads through the editor's
# own file routes how many server processes a flow really cost. A file
# `.fake-lsp-restart` appearing in the root makes the fake exit with the
# watcher contract's code 64 after removing the file, standing in for the
# container watcher seeing a workspace change.
import json
import os
import sys
import threading
import time

write_lock = threading.Lock()
indexed = threading.Event()


def read_frame(stdin):
    length = None
    while True:
        line = stdin.readline()
        if not line:
            return None
        line = line.strip()
        if line == b"":
            break
        if line.lower().startswith(b"content-length:"):
            length = int(line.split(b":", 1)[1])
    if length is None:
        return None
    return json.loads(stdin.read(length))


def send(obj):
    data = json.dumps(obj).encode()
    with write_lock:
        sys.stdout.buffer.write(b"Content-Length: %d\r\n\r\n" % len(data))
        sys.stdout.buffer.write(data)
        sys.stdout.buffer.flush()


def progress(kind, pct):
    value = {"kind": kind}
    if pct is not None:
        value["percentage"] = pct
    send({"jsonrpc": "2.0", "method": "$/progress", "params": {"token": "work", "value": value}})


# The dependency the fake pretends the project downloaded: it lies in the
# module cache the cockpit binds at the same path inside and outside, which
# is what makes the read route able to answer it at all.
DEP_REL = "example.com/dep@v1.0.0/dep.go"
DEP_TEXT = "package dep\n\n// Target is the definition in a dependency.\nfunc Target() {}\n"
STDLIB_FILE = "/usr/local/go/src/fmt/print.go"


def write_dependency():
    modcache = os.environ.get("GOMODCACHE", "")
    if not modcache:
        return ""
    path = os.path.join(modcache, DEP_REL)
    try:
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as out:
            out.write(DEP_TEXT)
    except OSError:
        return ""
    return path


def loc(uri, line, character, length):
    return {
        "uri": uri,
        "range": {
            "start": {"line": line, "character": character},
            "end": {"line": line, "character": character + length},
        },
    }


def slow_index(with_pct):
    progress("begin", 0 if with_pct else None)
    for step in (20, 45, 70, 90):
        time.sleep(0.7)
        progress("report", step if with_pct else None)
    time.sleep(0.4)
    indexed.set()
    progress("end", None)


def main():
    stdin = sys.stdin.buffer
    root = ""
    dependency = ""
    while True:
        msg = read_frame(stdin)
        if msg is None:
            return
        method = msg.get("method")
        if method == "initialize":
            root = (msg.get("params") or {}).get("rootUri") or ""
            dependency = write_dependency()
            send({"jsonrpc": "2.0", "id": msg["id"], "result": {"capabilities": {}}})
            root_dir = root.replace("file://", "", 1)
            def restart_watch(where):
                flag = os.path.join(where, ".fake-lsp-restart")
                while True:
                    time.sleep(0.3)
                    if os.path.isfile(flag):
                        try:
                            os.remove(flag)
                        except OSError:
                            pass
                        os._exit(64)
            threading.Thread(target=restart_watch, args=(root_dir,), daemon=True).start()
            marker = os.path.join(root_dir, ".fake-lsp-slow")
            if os.path.isfile(marker):
                with open(os.path.join(root_dir, ".fake-lsp-starts"), "a") as ledger:
                    ledger.write("start\n")
                with_pct = not open(marker).read().startswith("nopct")
                threading.Thread(target=slow_index, args=(with_pct,), daemon=True).start()
            else:
                # Announce an already finished indexing, so the server side
                # never waits a grace for an announcement that would not come.
                indexed.set()
                progress("begin", None)
                progress("end", None)
        elif method == "textDocument/definition":
            line = msg["params"]["position"]["line"]
            asked = (msg["params"].get("textDocument") or {}).get("uri") or ""
            if line == 0:
                result = None
            elif asked.endswith("/deps.go") and dependency:
                result = [loc("file://" + dependency, 3, 5, 6)]
            elif asked.endswith("/stdlib.go") or (dependency and asked.endswith(dependency)):
                result = [loc("file://" + STDLIB_FILE, 3, 5, 7)]
            else:
                result = [loc(root + "/lib.go", 2, 5, 11)]
            send({"jsonrpc": "2.0", "id": msg["id"], "result": result})
        elif method == "textDocument/references":
            locs = [loc(root + "/use.go", 3, 1, 11)]
            if indexed.is_set():
                locs += [
                    loc(root + "/use.go", 4, 1, 11),
                    loc(root + "/lib.go", 2, 5, 11),
                    loc("file:///outside/stub.go", 0, 0, 1),
                ]
            send({"jsonrpc": "2.0", "id": msg["id"], "result": locs})
        elif method == "shutdown":
            send({"jsonrpc": "2.0", "id": msg["id"], "result": None})
        elif method == "exit":
            return


if __name__ == "__main__":
    main()
