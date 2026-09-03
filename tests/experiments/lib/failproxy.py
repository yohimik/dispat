"""The fault injector: a reverse proxy in front of the registry that answers
502 to any PUT whose path names a package listed in the deny folder, and
forwards everything else untouched. A denied upload is what a flaky registry,
a revoked token or a dropped connection looks like to the tool publishing
through it. Standard library only.

The deny test is on the package the request names, not on a substring of the
path: `core` in the deny folder must refuse an upload of `core` and not one of
`core-utils`, and a scoped name arrives percent-encoded in one segment, so the
segment is decoded before it is compared.

Every request is logged, with the decision and the status it ended in. A proxy
that silently drops something is a fault the experiment did not inject.
"""
import http.client
import http.server
import os
import sys
import urllib.parse

UPSTREAM = os.environ.get("UPSTREAM", "127.0.0.1:4874")
DENY_DIR = os.environ.get("DENY_DIR", "/deny")
PORT = int(os.environ.get("PORT", "4873"))

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


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log(self, message):
        sys.stderr.write(f"failproxy: {message}\n")
        sys.stderr.flush()

    def _denied(self):
        if self.command != "PUT" or not os.path.isdir(DENY_DIR):
            return False
        return package_of(self.path) in set(os.listdir(DENY_DIR))

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

        if self._denied():
            # The upload is read and dropped, so the refusal is what the tool
            # sees and not a broken next request on the kept connection.
            self.log(f"DENY {self.command} {self.path} (package {name}) -> 502")
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
