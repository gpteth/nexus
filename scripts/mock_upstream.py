#!/usr/bin/env python3
"""最小 OpenAI 兼容上游，用于 Nexus 端到端冒烟测试。监听 127.0.0.1:9000。"""
import json
from http.server import BaseHTTPRequestHandler, HTTPServer


class Handler(BaseHTTPRequestHandler):
    def send_json(self, code, obj):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path.endswith("/models"):
            self.send_json(200, {"object": "list", "data": [
                {"id": "mock-model", "object": "model", "owned_by": "mock"}]})
        else:
            self.send_json(404, {"error": "not found"})

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        payload = json.loads(self.rfile.read(length) or b"{}")

        if not payload.get("stream"):
            self.send_json(200, {
                "id": "chatcmpl-mock",
                "object": "chat.completion",
                "model": "mock-model",
                "usage": {"prompt_tokens": 11, "completion_tokens": 22, "total_tokens": 33},
            })
            return

        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        for chunk in (
            '{"id":"c","model":"mock-model","choices":[]}',
            '{"id":"c","model":"mock-model","usage":{"prompt_tokens":11,"completion_tokens":22,"total_tokens":33}}',
        ):
            self.wfile.write(f"data: {chunk}\n\n".encode())
            self.wfile.flush()
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    HTTPServer(("127.0.0.1", 9000), Handler).serve_forever()
