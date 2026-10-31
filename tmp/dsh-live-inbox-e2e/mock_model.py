#!/usr/bin/env python3
"""Deterministic OpenAI-compatible model for the DSH live-inbox harness."""
import json
import os
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = os.path.dirname(os.path.abspath(__file__))
LOG = os.path.join(ROOT, 'model-requests.jsonl')
MARKER = 'LIVE-INBOX-OWNER'
lock = threading.Lock()


def sse(payload):
    return f'data: {json.dumps(payload, ensure_ascii=False)}\n\n'.encode()


def chunk(delta, finish=None):
    return {
        'id': 'chatcmpl-live', 'object': 'chat.completion.chunk', 'created': 0,
        'model': 'mock-model',
        'choices': [{'index': 0, 'delta': delta, 'finish_reason': finish}],
    }


def messages_of(body):
    return body.get('messages') or []


def marker_seen(messages):
    for message in messages:
        content = message.get('content')
        if isinstance(content, str) and MARKER in content:
            return True
        if isinstance(content, list):
            for part in content:
                if isinstance(part, dict) and part.get('type') == 'text' and MARKER in str(part.get('text', '')):
                    return True
    return False


def has_tool_result(messages):
    return any(message.get('role') == 'tool' for message in messages)


class Handler(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def log_message(self, fmt, *args):
        sys.stderr.write('mock-model: ' + fmt % args + '\n')

    def do_POST(self):
        length = int(self.headers.get('Content-Length', '0'))
        body = json.loads(self.rfile.read(length) or b'{}')
        messages = messages_of(body)
        with lock:
            with open(LOG, 'a', encoding='utf-8') as handle:
                handle.write(json.dumps({
                    'roles': [message.get('role') for message in messages],
                    'marker': marker_seen(messages),
                    'messages': messages,
                }, ensure_ascii=False) + '\n')

        mode = os.environ.get('MODEL_MODE', 'tool')
        if mode == 'final':
            # No tool call: the turn reaches turn-stopping, where the plugin
            # must claim and steer before DSH closes the turn.
            text = 'LIVE-INBOX-RECEIVED' if marker_seen(messages) else 'LIVE-INBOX-NO-TOOL'
            frames = [chunk({'role': 'assistant', 'content': text}), chunk({}, 'stop')]
        elif has_tool_result(messages):
            text = 'LIVE-INBOX-RECEIVED' if marker_seen(messages) else 'LIVE-INBOX-MISSING'
            frames = [chunk({'role': 'assistant', 'content': text}), chunk({}, 'stop')]
        else:
            args = json.dumps({'description': 'Run live inbox harness tool', 'command': 'echo LIVE-TOOL-RAN'})
            frames = [
                chunk({'role': 'assistant', 'content': 'Running harness tool.'}),
                chunk({'tool_calls': [{'index': 0, 'id': 'call_live_1', 'type': 'function', 'function': {'name': 'bash', 'arguments': args}}]}),
                chunk({}, 'tool_calls'),
            ]
        payload = b''.join(sse(frame) for frame in frames) + b'data: [DONE]\n\n'
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.send_header('Content-Length', str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)


if __name__ == '__main__':
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18768
    server = ThreadingHTTPServer(('127.0.0.1', port), Handler)
    print('mock model listening on %d' % port, flush=True)
    server.serve_forever()
