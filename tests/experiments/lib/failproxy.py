"""The fault injector: a reverse proxy in front of the registry that answers
502 to any PUT whose path names a package listed in the deny folder, and
forwards everything else untouched. A denied upload is what a flaky registry,
a revoked token or a dropped connection looks like to the tool publishing
through it. Standard library only.

The deny test is on the package the request names, not on a substring of the
path: `core` in the deny folder must refuse an upload of `core` and not one of
`core-utils`, and a scoped name arrives percent-encoded in one segment, so the
segment is decoded before it is compared.

A deny file may name a gate: a path the refusal waits for. The proxy then holds
the refused upload until that path exists and answers 502 only then, which
turns "the provider is refused after the consumer has built" from a race into
an order, because the gate is the file the consumer's build writes. A gate that
never appears still ends in the refusal, after GATE_TIMEOUT seconds, and the
log line says which of the two happened.

Every request is logged, with the decision and the status it ended in. A proxy
that silently drops something is a fault the experiment did not inject.
"""
import http.client
import http.server
import os
import sys
import time
import urllib.parse

UPSTREAM = os.environ.get("UPSTREAM", "127.0.0.1:4874")
DENY_DIR = os.environ.get("DENY_DIR", "/deny")
PORT = int(os.environ.get("PORT", "4873"))
GATE_TIMEOUT = float(os.environ.get("GATE_TIMEOUT", "120"))
GATE_POLL = 0.1

# Hop-by-hop headers, which belong to one connection rather than to the
# message, plus the length this proxy recomputes for the body it holds.
DROPPED = {"transfer-encoding", "connection", "content-length", "keep-alive",
           "proxy-authenticate", "proxy-authorization", "te", "trailer", "upgrade"}


def package_of(path):
    """The package a request path names.

    npm addresses an unscoped package as /<name> and a scoped one as
    /@scope%2fname, one segment either way, so the first segment decoded is
    the name. Everything after it (/-/rev/..., /-rev/...) is about that same
    package."""
    segments = [s for s in urllib.parse.urlsplit(path).path.split("/") if s]
    if not segments:
        return ""
    return urllib.parse.unquote(segments[0])


def deny_rule(deny_dir, name):
    """What the deny folder says about a package's uploads: None when they
    pass, otherwise the gate path the refusal waits for, empty for none.

    The file is read only after the name was found in the listing, so a
    decoded name holding a separator or `..` can never reach a path outside
    the folder."""
    if not name or not os.path.isdir(deny_dir) or name not in set(os.listdir(deny_dir)):
        return None
    try:
        with open(os.path.join(deny_dir, name)) as f:
            return f.read().strip()
    except OSError:
        # Listed and gone again: the decision was already a refusal.
        return ""


def wait_for_gate(path, timeout, poll=GATE_POLL, clock=time.monotonic, sleep=time.sleep):
    """Hold until the gate path exists or the timeout passes, and say which.

    The phrase is what the decision log carries between the package and the
    status, so the record states whether the refusal was ordered after the
    gate or merely came late."""
    deadline = clock() + timeout
    while not os.path.exists(path):
        if clock() >= deadline:
            return f"gate {path} timed out after {timeout:g}s"
        sleep(poll)
    return f"after {path}"


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log(self, message):
        sys.stderr.write(f"failproxy: {message}\n")
        sys.stderr.flush()

    def _deny_rule(self):
        if self.command != "PUT":
            return None
        return deny_rule(DENY_DIR, package_of(self.path))

    def _read_body(self):
        """The request body, whether it arrived with a length or in chunks.

        npm sends a large publish with Transfer-Encoding: chunked, and a proxy
        that reads only Content-Length forwards an empty body and leaves the
        rest of the chunks in the socket for the next request to be parsed
        as."""
        if "chunked" in self.headers.get("Transfer-Encoding", "").lower():
            parts = []
            while True:
                line = self.rfile.readline()
                if not line:
                    break
                size = int(line.split(b";")[0].strip() or b"0", 16)
                if size == 0:
                    while True:
                        trailer = self.rfile.readline()
                        if trailer in (b"\r\n", b"\n", b""):
                            break
                    break
                parts.append(self.rfile.read(size))
                self.rfile.read(2)
            return b"".join(parts)
        length = int(self.headers.get("Content-Length", 0) or 0)
        return self.rfile.read(length) if length else None

    def _respond(self, status, body, headers=(), length=None):
        """One response, with the length this proxy is answerable for.

        A HEAD carries the length of the body it stands for and no body, which
        is why the length is a parameter rather than len(body): forwarding
        Content-Length: 0 for every HEAD would tell a client the package
        document is empty."""
        self.send_response(status)
        for k, v in headers:
            self.send_header(k, v)
        self.send_header("Content-Length", str(len(body) if length is None else length))
        self.end_headers()
        if body and self.command != "HEAD":
            self.wfile.write(body)

    def _forward(self):
        name = package_of(self.path)
        try:
            body = self._read_body()
        except (ValueError, OSError) as e:
            self.log(f"{self.command} {self.path} unreadable body: {e} -> 400")
            self._respond(400, b'{"error":"unreadable request body"}')
            self.close_connection = True
            return

        gate = self._deny_rule()
        if gate is not None:
            # The upload is read and dropped, so the refusal is what the tool
            # sees and not a broken next request on the kept connection. A
            # gated refusal waits here, on this request's own thread, while
            # every other request is still answered.
            ordered = f" {wait_for_gate(gate, GATE_TIMEOUT)}" if gate else ""
            self.log(f"DENY {self.command} {self.path} (package {name}){ordered} -> 502")
            self._respond(502, b'{"error":"injected fault"}', [("Connection", "close")])
            self.close_connection = True
            return

        try:
            conn = http.client.HTTPConnection(UPSTREAM, timeout=60)
            headers = {k: v for k, v in self.headers.items() if k.lower() != "host"}
            conn.request(self.command, self.path, body=body, headers=headers)
            resp = conn.getresponse()
            data = resp.read()
        except Exception as e:  # noqa: BLE001 - any upstream failure is one answer
            # A 502 with a reason, rather than a connection the tool sees drop
            # and reports as something else entirely.
            self.log(f"{self.command} {self.path} upstream failed: {e} -> 502")
            self._respond(502, b'{"error":"upstream unreachable"}', [("Connection", "close")])
            self.close_connection = True
            return

        kept = [(k, v) for k, v in resp.getheaders() if k.lower() not in DROPPED]
        length = None
        if self.command == "HEAD":
            upstream_length = resp.getheader("Content-Length")
            length = int(upstream_length) if upstream_length is not None else 0
        self.log(f"{self.command} {self.path} (package {name}) -> {resp.status}")
        self._respond(resp.status, data, kept, length)

    do_GET = do_PUT = do_POST = do_DELETE = do_HEAD = do_PATCH = do_OPTIONS = _forward

    def log_message(self, *args):
        # The class's own access log is replaced by the decision log above:
        # one line per request saying what it was about and how it ended.
        pass


if __name__ == "__main__":
    sys.stderr.write(f"failproxy: listening on 127.0.0.1:{PORT}, upstream {UPSTREAM},"
                     f" deny folder {DENY_DIR}\n")
    sys.stderr.flush()
    http.server.ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
