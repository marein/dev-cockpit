#!/usr/bin/env python3
# Deterministic fake language server for the editor-intel.js runner. Speaks
# just enough framed LSP over stdio: initialize handshake, full text document
# sync and a fixed completion list, so the suite never depends on a real
# gopls install. Start it as "gopls" via a wrapper script on the throwaway
# instance's PATH (see tests/e2e/README.md).
import json
import sys


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
    sys.stdout.buffer.write(b"Content-Length: %d\r\n\r\n" % len(data))
    sys.stdout.buffer.write(data)
    sys.stdout.buffer.flush()


ITEMS = [
    {
        "label": "IntelAlpha",
        "kind": 3,
        "detail": "func IntelAlpha() error",
        "documentation": "Deterministic fake completion used by the e2e suite.",
        "insertText": "IntelAlpha",
    },
    {
        "label": "IntelBeta",
        "kind": 6,
        "detail": "var IntelBeta int",
        "insertText": "IntelBeta",
    },
]


def main():
    stdin = sys.stdin.buffer
    while True:
        msg = read_frame(stdin)
        if msg is None:
            return
        method = msg.get("method")
        if method == "initialize":
            send({"jsonrpc": "2.0", "id": msg["id"], "result": {"capabilities": {}}})
        elif method == "textDocument/completion":
            send({"jsonrpc": "2.0", "id": msg["id"], "result": {"isIncomplete": False, "items": ITEMS}})
        elif method == "shutdown":
            send({"jsonrpc": "2.0", "id": msg["id"], "result": None})
        elif method == "exit":
            return


if __name__ == "__main__":
    main()
