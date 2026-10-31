#!/usr/bin/env python3
"""Deterministic OpenAI-compatible mock provider for the DSH steer spike.

Request 1 (no tool result yet) streams a `bash` tool call.
Request 2+ inspects the request body for the steer marker and streams a final
text answer that reports whether the marker reached the model request.
Every request body is appended to requests.jsonl as evidence.
"""
import json
import os
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = os.path.dirname(os.path.abspath(__file__))
REQUESTS = os.path.join(ROOT, 'requests.jsonl')
MARKER = 'SPIKE-STEER-MARKER'

lock = threading.Lock()


def sse_chunk(payload):
    return f'data: {json.dumps(payload, ensure_ascii=False)}\n\n'.encode()


def chunk(delta, finish=None):
    return {
        'id': 'chatcmpl-spike',
        'object': 'chat.completion.chunk',
        'created': 0,
        'model': 'mock-model',
        'choices': [{'index': 0, 'delta': delta, 'finish_reason': finish}],
    }


def has_tool_result(messages):
    return any(m.get('role') == 'tool' for m in messages)


def has_marker(messages):
    for message in messages:
        content = message.get('content')
        if isinstance(content, str) and MARKER in content:
            return True
        if isinstance(content, list):
            for part in content:
                if isinstance(part, dict) and part.get('type') == 'text' and MARKER in str(part.get('text', '')):
                    return True
    return False


class Handler(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def log_message(self, fmt, *args):
        sys.stderr.write('mock: ' + fmt % args + '\n')

    def do_POST(self):
        length = int(self.headers.get('Content-Length', '0'))
        raw = self.rfile.read(length)
        try:
            body = json.loads(raw)
        except Exception:
            body = {'_raw': raw.decode('utf-8', 'replace')}
        messages = body.get('messages', []) or []
        record = {
            'path': self.path,
            'model': body.get('model'),
            'stream': body.get('stream'),
            'tool_choice': body.get('tool_choice'),
            'roles': [m.get('role') for m in messages],
            'has_marker': has_marker(messages),
            'tool_names': [t.get('function', {}).get('name') for t in (body.get('tools') or [])],
            'messages': messages,
        }
        with lock:
            with open(REQUESTS, 'a', encoding='utf-8') as handle:
                handle.write(json.dumps(record, ensure_ascii=False) + '\n')

        if has_tool_result(messages):
            text = 'SPIKE-STEER-RECEIVED' if has_marker(messages) else 'SPIKE-STEER-MISSING'
            frames = [
                chunk({'role': 'assistant', 'content': text}),
                chunk({}, 'stop'),
            ]
        else:
            args = json.dumps({'description': 'Echo the spike tool marker', 'command': 'echo SPIKE-TOOL-RAN'})
            frames = [
                chunk({'role': 'assistant', 'content': 'Running the spike command.'}),
                chunk({'tool_calls': [{'index': 0, 'id': 'call_spike_1', 'type': 'function', 'function': {'name': 'bash', 'arguments': args}}]}),
                chunk({}, 'tool_calls'),
            ]
        payload = b''.join(sse_chunk(frame) for frame in frames) + b'data: [DONE]\n\n'
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.send_header('Cache-Control', 'no-cache')
        self.send_header('Content-Length', str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)
        self.wfile.flush()


if __name__ == '__main__':
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18766
    server = ThreadingHTTPServer(('127.0.0.1', port), Handler)
    print(f'mock provider listening on {port}', flush=True)
    server.serve_forever()
