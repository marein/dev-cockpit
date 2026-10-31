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
#
# Hover answers the way intelephense does, its markdown and the name's range
# copied from the real server, for the PHP names of the class name runner:
# `Bar` and its alias `Baz` are the class Foo\Bar, `Suit` the enum Foo\Suit,
# `GlobalThing` a class without a namespace, `helper` a namespaced function,
# `make`, `name`, `X` and `Hearts` a method, a property, a constant and an
# enum case, any `$variable` a variable. A name inside quotes or behind `//`
# has no hover, and neither has any other word. Hovering `Crash` ends the
# server, standing in for one that fails. A file `.fake-lsp-hold` in the root
# keeps the announced indexing running until the file is gone, which is how
# the runner holds a lookup in front of an index that is not ready yet.
#
# Workspace symbols answer the way gopls v0.23.0 does over the two files the
# type search runner writes, types.go and inner/box.go: every symbol for any
# query, its exact syntax with a quote in front included, the way a fuzzy
# matcher hands over more than matches, with gopls' kinds, names, packages
# and positions, a function, a field, an embedded interface and a standard
# library interface among them. The query "zzz" answers none, the query
# "widg" a hundred copies of one type, gopls' limit, so the runner sees an
# answer the server cut.
#
# The fake plays the server its command line names, the way registry.go
# starts each: `--lsp` is tsgo, `--stdio` intelephense when the handshake
# carries its storage path and pyright otherwise, no argument gopls. The
# other three answer workspace symbols over the files of the runner's mixed
# project the way the real servers did, kinds, containers and ranges copied:
# intelephense's range starts at the doc comment, tsgo answers nothing until
# a document is open, pyright nothing until its first diagnostics, which it
# publishes a moment after the first document opens, and streams its answer
# file by file when the request carries a partial result token. None of the
# three matches a space or an upper case letter, as one word servers whose
# upper case must match. Every server names only the files the project
# holds. tsgo names a declaration file only once the type search switched
# its library symbols on, which it does per request through the
# configuration: one in node_modules, and one of its standard library that
# lies in the image.
#
# A file `.fake-lsp-crowded` in a project root makes intelephense answer the
# query with a thousand vendor classes, its limit, and nothing of the
# project's own, which it names only to a query behind one of the project's
# namespaces, qualified the way the real one does, and in the document
# symbols of an open file: a class of the PHP code on its name, and a
# script's class and a style's selector of an HTML part in the plain shape.
#
# Document symbols answer off the open document's text the way each server
# shaped its answer: gopls v0.23.0 a tree of the constants, the structs with
# their fields, the interfaces with their methods, the functions and the
# methods by receiver, each with gopls' detail, and only once its index
# stands, as the real one answers no request before its workspace loaded,
# while every other server answers at once; tsgo 7.0.0-dev the flat list
# it gives a client without the tree, every imported name, the exported
# constants, a class's properties and methods and a method's locals, each
# naming its container; every other server the class lines and the HTML
# parts above, a mixed answer.
#
# The implementations runner's shape.go is answered off its text as gopls
# v0.23.0 answered it: a type's definition is its declaration, its references
# every place its name stands, its type hierarchy item carries gopls' kind
# (interface 11, any other type 5), and its subtypes are those SUBTYPES
# lists. Off a type, and in every other file, the preparation is refused.
# Its shape.ts and animal.js are answered as tsgo 7.0.0-dev answered them: a
# class's or an interface's definition is its declaration, its references
# every place its name stands, and its implementations, asked anywhere on
# its name, are those TS_IMPLEMENTATIONS lists, ranges copied: a class by its
# name, a class expression from its keyword to its end, an object literal
# typed with the interface from brace to brace, the asked class itself and
# the ones extending an implementation among them. Its shape.php and
# circle.php are answered as intelephense 1.18.5 answered them: a type's
# definition is its whole declaration from its doc comment on, and its
# references every place its name stands in the two files, a name an import
# renames it to being a word of its own; a position in a file the cockpit
# has not opened holds nothing, as after a restart. Its shape.py, circle.py
# and chain.py are answered as pyright 1.1.414 answered them: a class's
# definition is its name, and its references every place its name stands in
# the three files, whether the cockpit opened them or not, a name an import
# renames it to being a word of its own. A file `.fake-lsp-slow-walk` in the
# root makes each of those references take 1.5s, standing in for a server
# too slow for the walk to the indirect subclasses to end in time.
#
# Run as `fake-lsp.py --image cat|tar ...`, it answers the fake docker's read
# out of an image from IMAGE_FILES: `cat <path>` prints one file and fails
# for a path the table does not hold, `tar -cf - -- <paths>` writes a tar of
# those it holds to stdout, member names without the leading slash, and
# fails afterwards when one was missing, as the real tar does.
import io
import json
import os
import re
import sys
import tarfile
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
TS_LIB_FILE = "/usr/local/lib/node_modules/@typescript/native-preview/lib/lib.es5.d.ts"


def lines(numbered):
    return "\n".join(numbered.get(n, "") for n in range(1, max(numbered) + 1)) + "\n"


# The files the servers' images hold: the standard library file a definition
# lands in, and the lines of the standard library types the type search
# reads, the lines in between empty.
IMAGE_FILES = {
    STDLIB_FILE: "package fmt\n\n// Println is the standard library one.\nfunc Println(a ...any) {}\n",
    "/usr/local/go/src/io/io.go": lines({1: "package io", 86: "type Reader interface {", 87: "\tRead(p []byte) (n int, err error)", 88: "}"}),
    TS_LIB_FILE: lines({30: "type OrderLibAlias = string | number;"}),
}


def image(argv):
    program, args = argv[0], argv[1:]
    if program == "cat":
        text = IMAGE_FILES.get(args[-1] if args else "")
        if text is None:
            sys.exit(1)
        sys.stdout.write(text)
        return
    if program != "tar" or "--" not in args:
        sys.exit(1)
    missing = False
    with tarfile.open(fileobj=sys.stdout.buffer, mode="w|") as archive:
        for path in args[args.index("--") + 1:]:
            text = IMAGE_FILES.get(path)
            if text is None:
                missing = True
                continue
            data = text.encode()
            info = tarfile.TarInfo(path.lstrip("/"))
            info.size = len(data)
            archive.addfile(info, io.BytesIO(data))
    sys.exit(2 if missing else 0)


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


SHAPE_FILE = "/shape.go"
SUBTYPES = {"Shape": ["Solid", "Square", "wrapped"]}
NOT_A_TYPE = {"code": 0, "message": "not a type name"}


TS_FILES = ("/shape.ts", "/animal.js")
TS_IMPLEMENTATIONS = {
    "Shape": [(4, 17, 4, 22), (8, 13, 8, 19), (14, 13, 14, 17), (20, 20, 22, 1), (24, 27, 24, 44), (12, 13, 12, 17)],
    "Square": [(8, 13, 8, 19), (12, 13, 12, 17), (14, 13, 14, 17)],
    "Plain": [(30, 13, 30, 18)],
    "Animal": [(0, 13, 0, 19), (4, 13, 4, 16), (8, 19, 8, 42)],
}


PHP_FILES = ("/shape.php", "/circle.php")


def php_definition(docs, uri, name):
    folder = uri.rsplit("/", 1)[0]
    for declaring in PHP_FILES:
        rows = file_text(docs, folder + declaring).split("\n")
        for n, row in enumerate(rows):
            m = re.search(r"\b(?:interface|class|enum|trait) (\w+)", row)
            if name and m and m.group(1) == name:
                start = n
                while start > 0 and rows[start - 1].strip()[:1] in ("/", "*", "#"):
                    start -= 1
                return [{"uri": folder + declaring, "range": {"start": {"line": start, "character": 0}, "end": {"line": n, "character": len(row)}}}]
    return None


PY_FILES = ("/shape.py", "/circle.py", "/chain.py")


def py_definition(docs, uri, name):
    folder = uri.rsplit("/", 1)[0]
    for declaring in PY_FILES:
        for n, row in enumerate(file_text(docs, folder + declaring).split("\n")):
            m = re.search(r"\bclass (\w+)", row)
            if name and m and m.group(1) == name:
                return [loc(folder + declaring, n, m.start(1), len(name))]
    return None


def files_references(docs, uri, files, word):
    folder = uri.rsplit("/", 1)[0]
    return [ref for declaring in files for ref in shape_references(folder + declaring, file_text(docs, folder + declaring), word)] if word else []


def ts_definition(uri, text, name):
    for n, row in enumerate(text.split("\n")):
        m = re.search(r"\b(?:interface|class) (\w+)", row)
        if m and m.group(1) == name:
            return [loc(uri, n, m.start(1), len(name))]
    return None


def ts_implementations(uri, name):
    spans = TS_IMPLEMENTATIONS.get(name)
    if not spans:
        return None
    return [{"uri": uri, "range": {"start": {"line": a, "character": b}, "end": {"line": c, "character": d}}}
            for a, b, c, d in spans]


def word_at(text, line, character):
    rows = text.split("\n")
    if line >= len(rows):
        return ""
    row = rows[line]
    start = end = min(character, len(row))
    while start > 0 and (row[start - 1].isalnum() or row[start - 1] == "_"):
        start -= 1
    while end < len(row) and (row[end].isalnum() or row[end] == "_"):
        end += 1
    return row[start:end]


def shape_types(text):
    found = {}
    for n, row in enumerate(text.split("\n")):
        m = re.match(r"type (\w+) (\w+)", row)
        if m:
            found[m.group(1)] = (n, 11 if m.group(2) == "interface" else 5)
    return found


def shape_item(uri, name, decl):
    span = {"start": {"line": decl[0], "character": 5}, "end": {"line": decl[0], "character": 5 + len(name)}}
    return {"name": name, "kind": decl[1], "detail": "example.com/lib", "uri": uri, "range": span, "selectionRange": span}


def shape_references(uri, text, word):
    return [loc(uri, n, m.start(), len(word))
            for n, row in enumerate(text.split("\n"))
            for m in re.finditer(r"\b%s\b" % re.escape(word), row)]


def file_text(docs, uri):
    if uri in docs:
        return docs[uri]
    try:
        with open(uri.replace("file://", "", 1)) as f:
            return f.read()
    except OSError:
        return ""


CLASS_BAR = "__Foo\\\\Bar__\n\nA docblock.\n\n```php\n<?php\nclass Bar { }\n```"
HOVERS = {
    "Bar": CLASS_BAR,
    "Baz": CLASS_BAR,
    "Suit": "__Foo\\\\Suit__\n\n```php\n<?php\nenum Suit: string\n{\n    case Hearts;\n}\n```",
    "GlobalThing": "__GlobalThing__\n\n```php\n<?php\nclass GlobalThing { }\n```",
    "helper": "__App\\\\helper__\n\n```php\n<?php\nfunction helper(): void { }\n```\n\n_@return_ `void`",
    "make": "__Foo\\\\Bar::make__\n\n```php\n<?php\npublic static function make(): static { }\n```\n\n_@return_ `static`",
    "name": "__Foo\\\\Bar::$name__\n\n```php\n<?php\npublic string $name = '';\n```",
    "X": "__Foo\\\\Bar::X__\n\n```php\n<?php\npublic const X = 1;\n```\n\n_@var_ `1`",
    "Hearts": "__Foo\\\\Suit::Hearts__\n\n```php\n<?php\ncase Hearts = 'h';\n```",
}
NAME_CHARS = set("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_\\$")


def hover_at(text, line, character):
    lines = text.split("\n")
    if line >= len(lines):
        return None
    row = lines[line]
    start = end = character
    while start > 0 and row[start - 1] in NAME_CHARS:
        start -= 1
    while end < len(row) and row[end] in NAME_CHARS:
        end += 1
    if "//" in row[:start] or row[:start].count("'") % 2:
        return None
    name = row[start:end].rsplit("\\", 1)[-1]
    if name == "Crash":
        os._exit(3)
    if name.startswith("$"):
        value = "_@var_ `\\\\Foo\\\\Bar %s`" % name
    else:
        value = HOVERS.get(name)
    if value is None:
        return None
    return {
        "contents": {"kind": "markdown", "value": value},
        "range": {"start": {"line": line, "character": start}, "end": {"line": line, "character": end}},
    }


SYMBOLS = [
    ("Widget", 23, "example.com/geo", "types.go", 4, 5),
    ("Widget.Name", 8, "example.com/geo", "types.go", 4, 19),
    ("Shape", 11, "example.com/geo", "types.go", 6, 5),
    ("Handler", 12, "example.com/geo", "types.go", 8, 5),
    ("NewWidget", 12, "example.com/geo", "types.go", 10, 5),
    ("RW", 11, "example.com/geo", "types.go", 12, 5),
    ("RW.Reader", 11, "example.com/geo", "types.go", 13, 1),
    ("inner.WidgetBox", 23, "example.com/geo/inner", "inner/box.go", 2, 5),
    ("Reader", 11, "io", "/usr/local/go/src/io/io.go", 85, 5),
]


# The mixed project's symbols per server: name, kind, container, path, and
# the range's start line, start column and end line, 0-based.
PHP_NS = "Acme\\Shop\\Domain\\Order"
LANGUAGE_SYMBOLS = {
    "gopls": [
        ("OrderBook", 23, "example.com/mixed", "order.go", 2, 5, 2),
    ],
    "intelephense": [
        ("Order", 5, PHP_NS, "src/Shop/Order.php", 4, 0, 11),
        ("total", 6, PHP_NS + "\\Order", "src/Shop/Order.php", 10, 4, 10),
        ("Payable", 11, PHP_NS, "src/Shop/Order.php", 13, 0, 13),
        ("Loggable", 2, PHP_NS, "src/Shop/Order.php", 15, 0, 15),
        ("OrderState", 10, PHP_NS, "src/Shop/Order.php", 17, 0, 17),
        ("Open", 22, PHP_NS + "\\OrderState", "src/Shop/Order.php", 17, 26, 17),
        ("order_helper", 12, PHP_NS, "src/Shop/Order.php", 19, 0, 19),
        ("VendorOrder", 5, "Acme\\Lib", "vendor/acme/lib/VendorOrder.php", 2, 0, 2),
    ],
    "tsgo": [
        ("OrderLike", 11, None, "web/order.ts", 0, 17, 0),
        ("OrderModel", 5, None, "web/order.ts", 1, 13, 1),
        ("OrderKind", 10, None, "web/order.ts", 2, 12, 2),
        ("OrderId", 5, None, "web/order.ts", 3, 12, 3),
        ("OrderSpace", 3, None, "web/order.ts", 4, 10, 4),
        ("orderConst", 13, None, "web/order.ts", 5, 13, 5),
        ("OrderWidget", 5, None, "web/widget.js", 0, 13, 0),
        ("Ledger", 5, "Billing", "web/space.ts", 1, 15, 1),
        ("DepOrder", 5, None, "web/node_modules/dep/order.js", 0, 13, 0),
        ("VendorScriptOrder", 5, None, "vendor/acme/lib/order.js", 0, 13, 0),
    ],
    "tsgo-libraries": [
        ("OrderDepTypes", 11, None, "web/node_modules/dep/order.d.ts", 0, 17, 0),
        ("OrderLibAlias", 5, None, TS_LIB_FILE, 29, 5, 29),
    ],
    "pyright": [
        ("OrderBase", 5, None, "py/orders.py", 0, 6, 2),
        ("Nested", 5, "OrderBase", "py/orders.py", 1, 10, 2),
        ("OrderAlias", 13, None, "py/orders.py", 5, 0, 5),
        ("order_func", 12, None, "py/orders.py", 8, 4, 9),
        ("VenvOrder", 5, None, "py/third_party/lib/python3.12/site-packages/dep/__init__.py", 0, 6, 1),
    ],
}


def asked_uri(msg):
    return ((msg.get("params") or {}).get("textDocument") or {}).get("uri") or ""


def server_name(init_params):
    args = sys.argv[1:]
    if "--lsp" in args:
        return "tsgo"
    if "--stdio" in args:
        return "intelephense" if "storagePath" in (init_params.get("initializationOptions") or {}) else "pyright"
    return "gopls"


def workspace_symbols(server, root, query, libraries):
    def symbol(entry):
        name, kind, container, path, line, character = entry
        uri = "file://" + path if path.startswith("/") else root + "/" + path
        return {"name": name, "kind": kind, "containerName": container,
                "location": loc(uri, line, character, len(name.rsplit(".", 1)[-1]))}
    if server == "gopls" and os.path.isfile(root.replace("file://", "", 1) + "/types.go"):
        query = query.lstrip("'")
        if query == "zzz":
            return None
        if query == "widg":
            return [symbol(SYMBOLS[0])] * 100
        return [symbol(entry) for entry in SYMBOLS]
    return [entry for part in language_symbols(server, root, query, libraries) for entry in part]


def crowded_symbols(root, query):
    root_dir = root.replace("file://", "", 1)
    if "\\" not in query:
        vendor = root + "/vendor/acme/lib/VendorOrder.php"
        return [[{"name": "OrderVendor%04d" % i, "kind": 5, "containerName": "Acme\\Lib",
                  "location": loc(vendor, 2, 0, 11)} for i in range(1000)]]
    own = []
    for name, kind, container, path, line, character, end in LANGUAGE_SYMBOLS["intelephense"]:
        if container == PHP_NS and query.startswith(PHP_NS.lower() + "\\") and os.path.isfile(root_dir + "/" + path):
            own.append({"name": container + "\\" + name, "kind": kind, "location": {"uri": root + "/" + path, "range": {
                "start": {"line": line, "character": character}, "end": {"line": end, "character": 1}}}})
    return [own]


def document_symbols(uri, text):
    symbols = []
    for n, line in enumerate(text.split("\n")):
        if line.startswith("class "):
            name = line[len("class "):].split()[0]
            symbols.append({"name": name, "kind": 5,
                            "range": {"start": {"line": n, "character": 0}, "end": {"line": n, "character": len(line)}},
                            "selectionRange": {"start": {"line": n, "character": 6}, "end": {"line": n, "character": 6 + len(name)}}})
        for opening in ("<script>class ", "<style>"):
            at = line.find(opening)
            if at >= 0:
                symbols.append({"name": line[at + len(opening):].split()[0], "kind": 5,
                                "location": loc(uri, n, at, len(line) - at)})
    return symbols


def span(line, start, end_line, end):
    return {"start": {"line": line, "character": start}, "end": {"line": end_line, "character": end}}


def closing(lines, n, indent):
    """The line of the brace closing the block opened on line n."""
    for m in range(n + 1, len(lines)):
        if lines[m] == indent + "}":
            return m
    return n


GO_FUNC = re.compile(r"func (?:\(\w+ (\*?)(\w+)\) )?(\w+)(\(.*\S)\s*\{$")
GO_TYPE = re.compile(r"type (\w+) (struct|interface) \{$")


def go_document_symbols(text):
    """gopls v0.23.0's tree: constants, structs with their fields and
    interfaces with their methods, functions and methods by receiver, each
    with gopls' detail."""
    lines = text.split("\n")
    symbols = []
    for n, line in enumerate(lines):
        const = re.match(r"const (\w+) = ", line)
        typ = GO_TYPE.match(line)
        fn = GO_FUNC.match(line)
        if const:
            name = const.group(1)
            symbols.append({"name": name, "kind": 14, "range": span(n, 6, n, len(line)), "selectionRange": span(n, 6, n, 6 + len(name))})
        elif typ:
            name, what = typ.group(1), typ.group(2)
            end = closing(lines, n, "")
            children = []
            for m in range(n + 1, end):
                member = re.match(r"\t(\w+)(\(.*)$", lines[m]) if what == "interface" else re.match(r"\t(\w+)\s+(.+)$", lines[m])
                if member:
                    child, rest = member.group(1), member.group(2)
                    children.append({"name": child, "detail": "func" + rest if what == "interface" else rest,
                                     "kind": 6 if what == "interface" else 8,
                                     "range": span(m, 1, m, len(lines[m])), "selectionRange": span(m, 1, m, 1 + len(child))})
            symbols.append({"name": name, "detail": what + "{...}", "kind": 23 if what == "struct" else 11,
                            "range": span(n, 5, end, 1), "selectionRange": span(n, 5, n, 5 + len(name)), "children": children})
        elif fn:
            star, receiver, name, rest = fn.groups()
            at = line.index(name + "(")
            symbols.append({"name": "(%s%s).%s" % (star, receiver, name) if receiver else name, "detail": "func" + rest,
                            "kind": 6 if receiver else 12,
                            "range": span(n, 0, closing(lines, n, ""), 1), "selectionRange": span(n, at, n, at + len(name))})
    return symbols


def ts_document_symbols(uri, text):
    """The flat list tsgo 7.0.0-dev answers a client without the tree: every
    imported name, the exported constants, a class with its properties and
    methods and a method's locals, each naming its container."""
    lines = text.split("\n")
    symbols = []
    def plain(name, kind, container, line, start, end_line, end):
        entry = {"name": name, "kind": kind, "location": {"uri": uri, "range": span(line, start, end_line, end)}}
        if container:
            entry["containerName"] = container
        symbols.append(entry)
    cls = method = ""
    for n, line in enumerate(lines):
        imports = re.match(r"import \{ (.*) \} from ", line)
        if imports:
            at = line.index("{") + 2
            for name in imports.group(1).split(", "):
                plain(name, 13, "", n, at, n, at + len(name))
                at += len(name) + 2
        elif re.match(r"export const (\w+) = ", line):
            name = line.split()[2]
            plain(name, 13, "", n, 13, n, len(line) - 1)
        elif re.match(r"export class (\w+) \{$", line):
            cls = line.split()[2]
            plain(cls, 5, "", n, 0, closing(lines, n, ""), 1)
        elif cls and re.match(r"  (\w+)\(.*\) \{$", line):
            method = line.split("(")[0].strip()
            plain(method, 6, cls, n, 2, closing(lines, n, "  "), 3)
        elif cls and re.match(r"  (\w+) = ", line):
            name = line.split()[0]
            plain(name, 7, cls, n, 2, n, len(line) - 1)
        elif method and re.match(r"    const (\w+) = ", line):
            name = line.split()[1]
            plain(name, 13, method, n, 10, n, len(line) - 1)
    return symbols


def qualified_symbols(root, query):
    """intelephense's answer to a query holding a backslash: the symbols whose
    qualified name holds the query's parts in their order, named by it."""
    root_dir = root.replace("file://", "", 1)
    parts = {}
    for name, kind, container, path, line, character, end in LANGUAGE_SYMBOLS["intelephense"]:
        qualified = (container + "\\" if container else "") + name
        at = 0
        for part in query.split("\\"):
            at = qualified.lower().find(part, at)
            if at < 0:
                break
            at += len(part)
        if at >= 0 and not path.startswith("/") and os.path.isfile(root_dir + "/" + path):
            parts.setdefault(path, []).append({"name": qualified, "kind": kind, "location": {"uri": root + "/" + path, "range": {
                "start": {"line": line, "character": character}, "end": {"line": end, "character": 1}}}})
    return list(parts.values())


def language_symbols(server, root, query, libraries=False):
    if server != "gopls" and any(c == " " or c.isupper() for c in query):
        return []
    if server == "intelephense" and os.path.isfile(root.replace("file://", "", 1) + "/.fake-lsp-crowded"):
        return crowded_symbols(root, query)
    if server == "intelephense" and "\\" in query:
        return qualified_symbols(root, query)
    by_name = server != "gopls" and os.path.isfile(root.replace("file://", "", 1) + "/.fake-lsp-names")
    parts = {}
    entries = LANGUAGE_SYMBOLS.get(server, []) + (LANGUAGE_SYMBOLS["tsgo-libraries"] if server == "tsgo" and libraries else [])
    for name, kind, container, path, line, character, end in entries:
        if by_name and query not in name.lower():
            continue
        if path.startswith("/"):
            uri = "file://" + path
        elif os.path.isfile(root.replace("file://", "", 1) + "/" + path):
            uri = root + "/" + path
        else:
            continue
        entry = {"name": name, "kind": kind, "location": {"uri": uri, "range": {
            "start": {"line": line, "character": character},
            "end": {"line": end, "character": 1},
        }}}
        if container:
            entry["containerName"] = container
        parts.setdefault(path, []).append(entry)
    return list(parts.values())


def held_index(marker):
    progress("begin", None)
    while os.path.exists(marker):
        time.sleep(0.2)
    indexed.set()
    progress("end", None)


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
    docs = {}
    server = "gopls"
    libraries = False
    listed = threading.Event()
    while True:
        msg = read_frame(stdin)
        if msg is None:
            return
        method = msg.get("method")
        if method == "initialize":
            root = (msg.get("params") or {}).get("rootUri") or ""
            server = server_name(msg.get("params") or {})
            if server == "tsgo":
                # Its workspace is the directory above the project, the
                # project is where it runs.
                root = "file://" + os.getcwd()
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
            hold = os.path.join(root_dir, ".fake-lsp-hold")
            if os.path.isfile(hold):
                threading.Thread(target=held_index, args=(hold,), daemon=True).start()
            elif os.path.isfile(marker):
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
        elif method == "workspace/didChangeConfiguration":
            settings = (msg.get("params") or {}).get("settings") or {}
            switch = (settings.get("js/ts") or {}).get("workspaceSymbols") or {}
            libraries = switch.get("excludeLibrarySymbols") is False
        elif method == "textDocument/didOpen":
            doc = msg["params"]["textDocument"]
            docs[doc["uri"]] = doc.get("text", "")
            if server == "pyright" and not listed.is_set():
                def publish(uri):
                    time.sleep(0.5)
                    listed.set()
                    send({"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics",
                          "params": {"uri": uri, "diagnostics": []}})
                threading.Thread(target=publish, args=(doc["uri"],), daemon=True).start()
        elif method == "textDocument/documentSymbol" and server == "gopls":
            def answer_indexed(msg_id, text):
                indexed.wait()
                send({"jsonrpc": "2.0", "id": msg_id, "result": go_document_symbols(text)})
            uri = msg["params"]["textDocument"]["uri"]
            threading.Thread(target=answer_indexed, args=(msg["id"], docs.get(uri, "")), daemon=True).start()
        elif method == "textDocument/documentSymbol":
            uri = msg["params"]["textDocument"]["uri"]
            if uri not in docs:
                result = []
            elif server == "tsgo":
                result = ts_document_symbols(uri, docs[uri])
            else:
                result = document_symbols(uri, docs[uri])
            send({"jsonrpc": "2.0", "id": msg["id"], "result": result})
        elif method == "textDocument/didClose":
            docs.pop(msg["params"]["textDocument"]["uri"], None)
        elif method == "textDocument/didChange":
            changes = msg["params"].get("contentChanges") or []
            if changes:
                docs[msg["params"]["textDocument"]["uri"]] = changes[-1].get("text", "")
        elif method == "textDocument/hover":
            uri = msg["params"]["textDocument"]["uri"]
            pos = msg["params"]["position"]
            result = hover_at(docs.get(uri, ""), pos["line"], pos["character"]) if indexed.is_set() else None
            send({"jsonrpc": "2.0", "id": msg["id"], "result": result})
        elif method == "textDocument/definition":
            line = msg["params"]["position"]["line"]
            asked = (msg["params"].get("textDocument") or {}).get("uri") or ""
            if asked.endswith(SHAPE_FILE):
                text = docs.get(asked, "")
                name = word_at(text, line, msg["params"]["position"]["character"])
                decl = shape_types(text).get(name)
                result = [loc(asked, decl[0], 5, len(name))] if decl else None
            elif asked.endswith(TS_FILES):
                text = docs.get(asked, "")
                result = ts_definition(asked, text, word_at(text, line, msg["params"]["position"]["character"]))
            elif asked.endswith(PHP_FILES):
                text = file_text(docs, asked)
                result = php_definition(docs, asked, word_at(text, line, msg["params"]["position"]["character"]))
            elif asked.endswith(PY_FILES):
                text = file_text(docs, asked)
                result = py_definition(docs, asked, word_at(text, line, msg["params"]["position"]["character"]))
            elif line == 0:
                result = None
            elif asked.endswith("/deps.go") and dependency:
                result = [loc("file://" + dependency, 3, 5, 6)]
            elif asked.endswith("/stdlib.go") or (dependency and asked.endswith(dependency)):
                result = [loc("file://" + STDLIB_FILE, 3, 5, 7)]
            else:
                result = [loc(root + "/lib.go", 2, 5, 11)]
            send({"jsonrpc": "2.0", "id": msg["id"], "result": result})
        elif method == "textDocument/references" and asked_uri(msg).endswith((SHAPE_FILE,) + TS_FILES):
            asked = asked_uri(msg)
            text = docs.get(asked, "")
            pos = msg["params"]["position"]
            send({"jsonrpc": "2.0", "id": msg["id"], "result": shape_references(asked, text, word_at(text, pos["line"], pos["character"]))})
        elif method == "textDocument/references" and asked_uri(msg).endswith(PHP_FILES):
            asked = asked_uri(msg)
            text = docs.get(asked, "")
            pos = msg["params"]["position"]
            send({"jsonrpc": "2.0", "id": msg["id"], "result": files_references(docs, asked, PHP_FILES, word_at(text, pos["line"], pos["character"]))})
        elif method == "textDocument/references" and asked_uri(msg).endswith(PY_FILES):
            asked = asked_uri(msg)
            text = file_text(docs, asked)
            pos = msg["params"]["position"]
            if os.path.isfile(root.replace("file://", "", 1) + "/.fake-lsp-slow-walk"):
                time.sleep(1.5)
            send({"jsonrpc": "2.0", "id": msg["id"], "result": files_references(docs, asked, PY_FILES, word_at(text, pos["line"], pos["character"]))})
        elif method == "textDocument/implementation":
            asked = asked_uri(msg)
            text = docs.get(asked, "") if asked.endswith(TS_FILES) else ""
            pos = msg["params"]["position"]
            send({"jsonrpc": "2.0", "id": msg["id"], "result": ts_implementations(asked, word_at(text, pos["line"], pos["character"]))})
        elif method == "textDocument/prepareTypeHierarchy":
            asked = asked_uri(msg)
            text = docs.get(asked, "") if asked.endswith(SHAPE_FILE) else ""
            pos = msg["params"]["position"]
            name = word_at(text, pos["line"], pos["character"])
            decl = shape_types(text).get(name)
            if decl:
                send({"jsonrpc": "2.0", "id": msg["id"], "result": [shape_item(asked, name, decl)]})
            else:
                send({"jsonrpc": "2.0", "id": msg["id"], "error": NOT_A_TYPE})
        elif method == "typeHierarchy/subtypes":
            item = msg["params"]["item"]
            types = shape_types(file_text(docs, item["uri"]))
            result = [shape_item(item["uri"], name, types[name]) for name in SUBTYPES.get(item["name"], []) if name in types]
            send({"jsonrpc": "2.0", "id": msg["id"], "result": result or None})
        elif method == "textDocument/references":
            locs = [loc(root + "/use.go", 3, 1, 11)]
            if indexed.is_set():
                locs += [
                    loc(root + "/use.go", 4, 1, 11),
                    loc(root + "/lib.go", 2, 5, 11),
                    loc("file:///outside/stub.go", 0, 0, 1),
                ]
            send({"jsonrpc": "2.0", "id": msg["id"], "result": locs})
        elif method == "workspace/symbol":
            params = msg.get("params") or {}
            query = params.get("query") or ""
            if server == "tsgo" and not docs or server == "pyright" and not listed.is_set():
                result = []
            elif server == "pyright" and "partialResultToken" in params:
                for part in language_symbols(server, root, query):
                    send({"jsonrpc": "2.0", "method": "$/progress",
                          "params": {"token": params["partialResultToken"], "value": part}})
                result = []
            else:
                result = workspace_symbols(server, root, query, libraries)
            send({"jsonrpc": "2.0", "id": msg["id"], "result": result})
        elif method == "shutdown":
            send({"jsonrpc": "2.0", "id": msg["id"], "result": None})
        elif method == "exit":
            return


if __name__ == "__main__":
    if sys.argv[1:2] == ["--image"]:
        image(sys.argv[2:])
    else:
        main()
