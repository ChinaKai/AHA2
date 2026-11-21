#!/usr/bin/env python3
import importlib.util
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("pet_mcp_coverage", Path(__file__).with_name("pet-mcp-coverage.py"))
coverage = importlib.util.module_from_spec(spec)
spec.loader.exec_module(coverage)


class CoverageInventoryTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        coverage.ROOT = Path(self.temp.name)
        self.http = coverage.ROOT / "internal/httpapi"
        self.http.mkdir(parents=True)
        self.web = coverage.ROOT / "web/src"
        self.web.mkdir(parents=True)

    def test_literal_constant_and_desktop_tables_are_traced(self):
        (self.http / "server.go").write_text('''
const waitRoute = "POST /api/v1/agent/turn/inbox/wait"
mux.Handle("GET /api/v1/system", s.withAuth(http.HandlerFunc(s.systemInfo)))
mux.Handle(waitRoute, s.withAgentCapability(http.HandlerFunc(s.waitAgentInbox)))
mux.HandleFunc("/", s.serveWeb)
''')
        (self.http / "desktop_handlers.go").write_text('''
{"GET " + prefix + "/observation", s.desktopObserve},
mux.Handle(route.pattern, s.withAuth(route.handler))
{"GET /api/v1/agent/desktop", s.desktopStatus},
mux.Handle(route.pattern, s.withAgentCapability(route.handler))
''')
        rows = coverage.routes()
        self.assertEqual(len(rows), 5)
        self.assertEqual(rows["GET /api/v1/system"]["handler"], "systemInfo")
        self.assertEqual(rows["POST /api/v1/agent/turn/inbox/wait"]["boundary"], "agent_turn")
        self.assertIn("GET /api/v1/tasks/{id}/desktop/observation", rows)
        self.assertIn("ANY /", rows)

    def test_unknown_registration_and_duplicate_fail_closed(self):
        path = self.http / "server.go"
        path.write_text('mux.Handle(dynamicRoute, s.handler)')
        with self.assertRaisesRegex(ValueError, "unsupported registration"):
            coverage.routes()
        path.write_text('mux.Handle("GET /same", s.a)\nmux.Handle("GET /same", s.b)')
        with self.assertRaisesRegex(ValueError, "duplicate route"):
            coverage.routes()

    def test_web_templates_keep_source_and_method_hint(self):
        (self.web / "api.ts").write_text('''
return this.request(`/api/v1/tasks/${encodeURIComponent(id)}`, {method: "PATCH", body: "{}"});
fetch("/api/v1/system");
return this.request(`/api/v1/projects${query.size ? `?${query}` : ""}`);
''')
        sites = coverage.web_sites()
        self.assertEqual(len(sites), 3)
        self.assertEqual(sites[0]["method_hint"], "PATCH")
        self.assertEqual(sites[0]["line"], 2)
        self.assertIn("${encodeURIComponent(id)}", sites[0]["expression"])
        self.assertEqual(sites[2]["expression"], '/api/v1/projects${query.size ? `?${query}` : ""}')


if __name__ == '__main__':
    unittest.main()
