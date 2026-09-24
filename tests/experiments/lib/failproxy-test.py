#!/usr/bin/env python3
"""The fault proxy's decisions: which request names which package, and how a
gated refusal waits for its gate.

The deferred-build cell depends on the gate to order the provider's refusal
after the consumer's build, so a gate that stopped waiting, or waited forever,
would turn that cell back into the race it replaced.
"""
import http.client
import http.server
import importlib.util
import io
import os
import pathlib
import tempfile
import threading
import time
import unittest

HERE = pathlib.Path(__file__).parent
spec = importlib.util.spec_from_file_location("failproxy", HERE / "failproxy.py")
failproxy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(failproxy)


class PackageOfTest(unittest.TestCase):
    def test_the_first_segment_decoded(self):
        for path, want in (
            ("/core", "core"),
            ("/core/-rev/3-abc", "core"),
            ("/@acme%2fcore", "@acme/core"),
            ("/core-utils", "core-utils"),
            ("/core?write=true", "core"),
            ("/", ""),
            ("", ""),
        ):
            with self.subTest(path=path):
                self.assertEqual(failproxy.package_of(path), want)


class DenyRuleTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)

    def deny(self, name, gate=""):
        pathlib.Path(self.dir.name, name).write_text(gate)

    def test_an_unlisted_package_passes(self):
        self.deny("cli")
        self.assertIsNone(failproxy.deny_rule(self.dir.name, "core"))
        self.assertIsNone(failproxy.deny_rule(self.dir.name, "cli-utils"))
        self.assertIsNone(failproxy.deny_rule(self.dir.name, ""))

    def test_an_empty_file_refuses_at_once(self):
        self.deny("cli")
        self.assertEqual(failproxy.deny_rule(self.dir.name, "cli"), "")

    def test_a_file_names_its_gate(self):
        self.deny("core", "/work/dispat/packages/cli/dist/index.js\n")
        self.assertEqual(failproxy.deny_rule(self.dir.name, "core"), "/work/dispat/packages/cli/dist/index.js")

    def test_a_name_never_reaches_outside_the_folder(self):
        self.assertIsNone(failproxy.deny_rule(self.dir.name, "../etc"))
        self.assertIsNone(failproxy.deny_rule(os.path.join(self.dir.name, "absent"), "cli"))


class WaitForGateTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.gate = os.path.join(self.dir.name, "dist", "index.js")

    def test_the_gate_appears(self):
        def build():
            time.sleep(0.3)
            os.makedirs(os.path.dirname(self.gate))
            pathlib.Path(self.gate).write_text("// built\n")
        builder = threading.Thread(target=build)
        started = time.monotonic()
        builder.start()
        said = failproxy.wait_for_gate(self.gate, timeout=10, poll=0.02)
        builder.join()
        self.assertEqual(said, f"after {self.gate}")
        self.assertGreaterEqual(time.monotonic() - started, 0.3, "the refusal did not wait for the gate")

    def test_a_gate_already_there_does_not_wait(self):
        os.makedirs(os.path.dirname(self.gate))
        pathlib.Path(self.gate).write_text("// built\n")
        self.assertEqual(failproxy.wait_for_gate(self.gate, timeout=0), f"after {self.gate}")

    def test_the_gate_times_out(self):
        now = [0.0]

        def clock():
            return now[0]

        def sleep(seconds):
            now[0] += seconds

        said = failproxy.wait_for_gate(self.gate, timeout=120, poll=5, clock=clock, sleep=sleep)
        self.assertEqual(said, f"gate {self.gate} timed out after 120s")
        self.assertGreaterEqual(now[0], 120)


class Upstream(http.server.BaseHTTPRequestHandler):
    """A registry that accepts everything."""
    protocol_version = "HTTP/1.1"

    def do_PUT(self):
        self.rfile.read(int(self.headers.get("Content-Length", 0) or 0))
        body = b'{"ok":true}'
        self.send_response(201)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


class HandlerTest(unittest.TestCase):
    """The proxy as a tool meets it: a PUT through it, refused or forwarded,
    and the decision line the harness counts uploads from."""

    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.deny_dir = os.path.join(self.dir.name, "deny")
        os.makedirs(self.deny_dir)
        upstream = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Upstream)
        proxy = http.server.ThreadingHTTPServer(("127.0.0.1", 0), failproxy.Handler)
        for server in (upstream, proxy):
            threading.Thread(target=server.serve_forever, daemon=True).start()
            self.addCleanup(server.server_close)
            self.addCleanup(server.shutdown)
        self.proxy_port = proxy.server_address[1]
        saved = (failproxy.UPSTREAM, failproxy.DENY_DIR, failproxy.GATE_TIMEOUT)
        failproxy.UPSTREAM = f"127.0.0.1:{upstream.server_address[1]}"
        failproxy.DENY_DIR = self.deny_dir
        failproxy.GATE_TIMEOUT = 10
        self.addCleanup(lambda: setattr(failproxy, "UPSTREAM", saved[0]))
        self.addCleanup(lambda: setattr(failproxy, "DENY_DIR", saved[1]))
        self.addCleanup(lambda: setattr(failproxy, "GATE_TIMEOUT", saved[2]))
        self.log = io.StringIO()
        real = failproxy.sys.stderr
        failproxy.sys.stderr = self.log
        self.addCleanup(lambda: setattr(failproxy.sys, "stderr", real))

    def put(self, name):
        conn = http.client.HTTPConnection("127.0.0.1", self.proxy_port, timeout=30)
        self.addCleanup(conn.close)
        conn.request("PUT", f"/{name}", body=b'{"name":"x"}', headers={"Content-Type": "application/json"})
        response = conn.getresponse()
        response.read()
        return response.status

    def test_an_allowed_upload_is_forwarded(self):
        self.assertEqual(self.put("core"), 201)
        self.assertIn("failproxy: PUT /core (package core) -> 201", self.log.getvalue())

    def test_a_refusal_without_a_gate(self):
        pathlib.Path(self.deny_dir, "cli").write_text("")
        self.assertEqual(self.put("cli"), 502)
        self.assertIn("failproxy: DENY PUT /cli (package cli) -> 502", self.log.getvalue())

    def test_a_gated_refusal_waits_for_the_gate(self):
        gate = os.path.join(self.dir.name, "dist", "index.js")
        pathlib.Path(self.deny_dir, "core").write_text(gate)

        def build():
            time.sleep(0.3)
            os.makedirs(os.path.dirname(gate))
            pathlib.Path(gate).write_text("// built\n")
        builder = threading.Thread(target=build)
        builder.start()
        self.assertEqual(self.put("core"), 502)
        builder.join()
        self.assertIn(f"failproxy: DENY PUT /core (package core) after {gate} -> 502", self.log.getvalue())


if __name__ == "__main__":
    unittest.main()
