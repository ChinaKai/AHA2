from http.server import BaseHTTPRequestHandler, HTTPServer
import json, time

class Handler(BaseHTTPRequestHandler):
    def _send(self, code, obj):
        data = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length) if length else b"{}"
        if self.path == "/v1/devices/register":
            request = json.loads(body or b"{}")
            self._send(200, {"device_id": "dev_e2e", "device_name": request.get("device_name") or "E2E", "token": "e2e-token"})
        elif self.path == "/v1/sync/push":
            request = json.loads(body or b"{}")
            self._send(200, {"acked_keys": [obj.get("idempotency_key") for obj in request.get("objects", [])]})
        else:
            self._send(404, {"error": "not_found"})

    def do_GET(self):
        if self.path.startswith("/v1/sync/pull"):
            time.sleep(5)
            self._send(200, {"objects": [], "cursor": "1", "has_more": False})
        else:
            self._send(404, {"error": "not_found"})

    def do_PATCH(self):
        length = int(self.headers.get("Content-Length") or 0)
        if length:
            self.rfile.read(length)
        self._send(200, {})

    def log_message(self, *args):
        pass

HTTPServer(("127.0.0.1", 18767), Handler).serve_forever()
