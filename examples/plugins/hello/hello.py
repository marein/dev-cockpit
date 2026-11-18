#!/usr/bin/env python3
"""Example external plugin of dev-cockpit, see the README.

Serves its URL subtree on the socket the cockpit names, keeps notes in its
state directory, creates projects through the callback socket and keeps the
last job, coder and compose events.
"""
import http.client
import http.server
import json
import os
import signal
import socket
import socketserver
import sys
import threading
import time

SOCKET = os.environ["DEV_COCKPIT_PLUGIN_SOCKET"]
COCKPIT = os.environ["DEV_COCKPIT_SOCKET"]
TOKEN = os.environ["DEV_COCKPIT_TOKEN"]
NOTES = os.path.join(os.environ["DEV_COCKPIT_PLUGIN_STATE_DIR"], "notes.json")
PROBE = "/.dev-cockpit/probe"
lock = threading.Lock()
events = []


class CockpitConnection(http.client.HTTPConnection):
    def __init__(self, timeout=10):
        super().__init__("cockpit", timeout=timeout)

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(COCKPIT)


def cockpit(method, path, body=None):
    conn = CockpitConnection()
    payload = None if body is None else json.dumps(body)
    conn.request(method, path, body=payload, headers={"Authorization": "Bearer " + TOKEN, "Content-Type": "application/json"})
    res = conn.getresponse()
    raw = res.read()
    conn.close()
    return res.status, (json.loads(raw) if raw else {})


def listen():
    """Keeps the last events, reconnecting when the stream ends."""
    while True:
        try:
            conn = CockpitConnection(timeout=60)
            conn.request("GET", "/v1/events", headers={"Authorization": "Bearer " + TOKEN})
            for raw in conn.getresponse():
                line = raw.decode().strip()
                if line.startswith("data: "):
                    with lock:
                        events.append(json.loads(line[6:]))
                        del events[:-20]
        except (OSError, ValueError, http.client.HTTPException):
            pass
        time.sleep(2)


def read_notes():
    try:
        with open(NOTES) as f:
            return json.load(f)
    except (OSError, ValueError):
        return []


def write_notes(notes):
    tmp = NOTES + ".tmp"
    with open(tmp, "w") as f:
        json.dump(notes, f)
    os.replace(tmp, NOTES)


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def address_string(self):
        return "cockpit"

    def log_message(self, fmt, *args):
        if not self.path.startswith(PROBE):
            sys.stderr.write("%s %s\n" % (self.command, self.path))
            sys.stderr.flush()

    def send_json(self, status, value):
        body = json.dumps(value).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def read_json(self):
        length = int(self.headers.get("Content-Length") or 0)
        try:
            return json.loads(self.rfile.read(length) or b"{}")
        except ValueError:
            return {}

    def do_GET(self):
        if self.path == "/notes":
            with lock:
                return self.send_json(200, {"notes": read_notes()})
        if self.path == "/events":
            with lock:
                return self.send_json(200, {"events": list(reversed(events))})
        if self.path == "/echo":
            return self.send_json(200, {"cookie": self.headers.get("Cookie"), "csrf": self.headers.get("X-CSRF-Token"), "prefix": self.headers.get("X-Forwarded-Prefix")})
        if self.path == "/plugin":
            status, value = cockpit("GET", "/v1/plugin")
            return self.send_json(status, value)
        return self.send_json(404, {"error": "Not found."})

    def do_POST(self):
        if self.path == "/notes":
            text = str(self.read_json().get("text", "")).strip()[:500]
            if not text:
                return self.send_json(400, {"error": "Write a note first."})
            with lock:
                notes = read_notes() + [text]
                write_notes(notes)
            return self.send_json(200, {"notes": notes})
        if self.path == "/projects":
            name = str(self.read_json().get("name", "")).strip()
            status, value = cockpit("POST", "/v1/projects", {"name": name})
            if status == 201:
                cockpit("POST", "/v1/projects/%s/changed" % value["name"])
            return self.send_json(status, value)
        if self.path == "/echo":
            length = int(self.headers.get("Content-Length") or 0)
            body = self.rfile.read(length).decode(errors="replace")
            return self.send_json(200, {"body": body, "cookie": self.headers.get("Cookie"), "csrf": self.headers.get("X-CSRF-Token")})
        if self.path == "/fail":
            how = self.read_json().get("how")
            if how == "garbage":
                self.wfile.write(b"this is not HTTP\r\n\r\n")
                self.close_connection = True
                return
            self.send_json(200, {"failing": how})
            self.wfile.flush()
            if how == "exit":
                os._exit(3)
            if how == "hang":
                os.kill(os.getpid(), signal.SIGSTOP)
            return
        return self.send_json(404, {"error": "Not found."})


class Server(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    daemon_threads = True


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
    if os.path.exists(SOCKET):
        os.remove(SOCKET)
    threading.Thread(target=listen, daemon=True).start()
    server = Server(SOCKET, Handler)
    print("hello is listening", flush=True)
    server.serve_forever()
