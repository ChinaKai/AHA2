#!/usr/bin/env python3
"""Minimal AHA Agent API mock for the DSH live-inbox end-to-end harness."""
import json
import os
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = os.path.dirname(os.path.abspath(__file__))
LOG = os.path.join(ROOT, 'aha-requests.jsonl')
MESSAGE_ID = 'inbox-live-1'
CONTENT = 'LIVE-INBOX-OWNER: stop the current work and reply LIVE-INBOX-RECEIVED.'
INJECTION = (
    '[AHA live inbox] 1 new message(s) arrived while this Turn is still running. '
    'They are already delivered to you; do not call inbox/wait for them and do not defer them to a later Turn. '
    'Apply them now, before continuing your current work.\n\n'
    '### Message 1\nSource: owner (authority=owner)\n' + CONTENT
)

lock = threading.Lock()
state = {'claimed': False, 'ack': None}


class Handler(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def log_message(self, fmt, *args):
        sys.stderr.write('mock-aha: ' + fmt % args + '\n')

    def do_POST(self):
        length = int(self.headers.get('Content-Length', '0'))
        body = json.loads(self.rfile.read(length) or b'{}')
        with lock:
            record = {'path': self.path, 'body': body, 'authorization': bool(self.headers.get('Authorization'))}
            with open(LOG, 'a', encoding='utf-8') as handle:
                handle.write(json.dumps(record, ensure_ascii=False) + '\n')
            if self.path.endswith('/inbox/claim'):
                if not state['claimed'] and os.environ.get('MOCK_DISABLE') != '1':
                    state['claimed'] = True
                    payload = {
                        'ok': True,
                        'messages': [{
                            'sequence': 1,
                            'id': MESSAGE_ID,
                            'round_id': 'round-mock',
                            'source_kind': 'owner_message',
                            'source_agent_id': 'owner',
                            'content': CONTENT,
                            'source': {'kind': 'owner', 'authority': 'owner'},
                            'payload': {},
                        }],
                        'next_cursor': 1,
                        'injection_text': INJECTION,
                        'materialized_attachments': [],
                    }
                else:
                    payload = {'ok': True, 'messages': [], 'next_cursor': 0, 'injection_text': ''}
            elif self.path.endswith('/inbox/ack'):
                state['ack'] = body
                if body.get('outcome') == 'failed':
                    state['claimed'] = False
                payload = {'ok': True, 'acknowledged': len(body.get('message_ids') or []), 'outcome': body.get('outcome')}
            else:
                self.send_error(404)
                return
        encoded = json.dumps(payload, ensure_ascii=False).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json; charset=utf-8')
        self.send_header('Content-Length', str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)


if __name__ == '__main__':
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18767
    server = ThreadingHTTPServer(('127.0.0.1', port), Handler)
    print('mock AHA listening on %d' % port, flush=True)
    server.serve_forever()
