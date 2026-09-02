"""The fault injector: a reverse proxy in front of the registry that answers
502 to any PUT whose path names a package listed in the deny folder, and
forwards everything else untouched. A denied upload is what a flaky
registry, a revoked token or a dropped connection looks like to the tool
publishing through it. Standard library only."""
import http.client
import http.server
import os
import sys

UPSTREAM = os.environ.get("UPSTREAM", "127.0.0.1:4874")
DENY_DIR = os.environ.get("DENY_DIR", "/deny")
PORT = int(os.environ.get("PORT", "4873"))


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _denied(self):
        if self.command != "PUT" or not os.path.isdir(DENY_DIR):
            return False
        return any(name and name in self.path for name in os.listdir(DENY_DIR))

    def _forward(self):
        length = int(self.headers.get("Content-Length", 0) or 0)
        body = self.rfile.read(length) if length else None
        if self._denied():
            # The upload is read and dropped, so the refusal is what the
            # tool sees and not a broken next request on the kept connection.
            body = b'{"error":"injected fault"}'
            self.send_response(502)
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Connection", "close")
            self.end_headers()
            self.wfile.write(body)
            self.close_connection = True
            sys.stderr.write(f"DENY {self.command} {self.path}\n")
            return
        conn = http.client.HTTPConnection(UPSTREAM, timeout=60)
        headers = {k: v for k, v in self.headers.items() if k.lower() != "host"}
        conn.request(self.command, self.path, body=body, headers=headers)
        resp = conn.getresponse()
        data = resp.read()
        self.send_response(resp.status)
        for k, v in resp.getheaders():
            if k.lower() in ("transfer-encoding", "connection", "content-length"):
                continue
            self.send_header(k, v)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    do_GET = do_PUT = do_POST = do_DELETE = _forward

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    http.server.ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
