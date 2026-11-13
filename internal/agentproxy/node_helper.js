#!/usr/bin/env node
'use strict';

// Workspace side of the Agent API stdio tunnel.
//
// This file is uploaded into the workspace and run there, so it must use the
// Node standard library only. Frames are positional: every constant and check
// below mirrors frame.go and mux.go, and protocol 1 is not negotiable.

const net = require('net');
const { once } = require('events');

const PROTOCOL = 1;
const HELPER_VERSION = '1';
const READY_PREFIX = 'READY';
const HEADER_SIZE = 9;
const MAX_PAYLOAD = 32 * 1024;
const INITIAL_WINDOW = 256 * 1024;
const MAX_STREAMS = 64;

const OPEN = 1;
const DATA = 2;
const WINDOW = 3;
const CLOSE = 4;
const RESET = 5;

// Outgoing frames are queued so a slow SSH channel cannot interleave partial
// frames and so a full pipe applies backpressure instead of buffering in RAM.
let writeChain = Promise.resolve();
let closed = false;

function fail(message, code) {
  // Exit only after stderr has been flushed: process.exit() would drop a pipe
  // write that has not reached the SSH channel yet.
  process.stderr.write(`agent-proxy: ${message}\n`, () => process.exit(code || 1));
}

function writeAll(buffer) {
  return new Promise((resolve, reject) => {
    process.stdout.write(buffer, (error) => (error ? reject(error) : resolve()));
  });
}

function send(kind, id, payload) {
  const length = payload ? payload.length : 0;
  const buffer = Buffer.allocUnsafe(HEADER_SIZE + length);
  buffer[0] = kind;
  buffer.writeUInt32BE(id, 1);
  buffer.writeUInt32BE(length, 5);
  if (length > 0) {
    payload.copy(buffer, HEADER_SIZE);
  }
  writeChain = writeChain.then(() => writeAll(buffer)).catch((error) => {
    fail(`write frame: ${error.message}`);
  });
}

// Stream is one accepted TCP connection carried over the channel.
//
// The two directions are independent: EOF from the workspace-side socket closes
// only that direction, so an HTTP response can still be delivered after the
// request body ended.
class Stream {
  constructor(mux, id, socket) {
    this.mux = mux;
    this.id = id;
    this.socket = socket;

    // Credit for bytes sent to the peer. A chunk is sent whole, exactly like the
    // Go helper: it waits until the whole payload fits the remaining window.
    this.credit = INITIAL_WINDOW;
    this.pendingOut = [];
    this.paused = false;

    // Bytes received from the peer but not yet granted back, plus the queue of
    // payloads waiting to be written to the socket.
    this.received = 0;
    this.queue = [];
    this.pumping = false;

    this.recvClosed = false;
    this.writeDone = false;
    this.readDone = false;
    this.finished = false;

    socket.setNoDelay(true);
    socket.on('data', (chunk) => this.onSocketData(chunk));
    socket.on('end', () => this.onSocketEnd());
    socket.on('error', () => {
      if (!this.finished) {
        send(RESET, this.id);
      }
      this.abort();
    });
    socket.on('close', () => {
      if (this.finished) {
        return;
      }
      // A socket that closed without reporting EOF was torn down underneath us;
      // the peer must learn that the stream is gone.
      if (!this.writeDone) {
        send(RESET, this.id);
      }
      this.writeDone = true;
      this.readDone = true;
      this.finish();
    });
  }

  onSocketData(chunk) {
    if (this.finished) {
      return;
    }
    // Node reads up to 64 KiB from a socket; the wire allows 32 KiB per frame.
    // Splitting here keeps every frame within the protocol limit that the Go
    // reader enforces.
    for (let offset = 0; offset < chunk.length; offset += MAX_PAYLOAD) {
      this.pendingOut.push(chunk.subarray(offset, Math.min(offset + MAX_PAYLOAD, chunk.length)));
    }
    if (!this.paused) {
      this.paused = true;
      this.socket.pause();
    }
    this.flushOut();
  }

  flushOut() {
    while (this.pendingOut.length > 0 && !this.finished) {
      const chunk = this.pendingOut[0];
      if (this.credit < chunk.length) {
        break;
      }
      this.credit -= chunk.length;
      this.pendingOut.shift();
      send(DATA, this.id, chunk);
    }
    if (this.finished) {
      this.pendingOut = [];
      return;
    }
    if (this.pendingOut.length === 0 && this.paused) {
      this.paused = false;
      this.socket.resume();
    }
  }

  addCredit(value) {
    if (this.finished) {
      return;
    }
    this.credit += value;
    // A correct peer grants exactly what it consumed, so credit never exceeds
    // the initial window. Clamping keeps a bogus WINDOW from permitting an
    // unbounded backlog.
    if (this.credit > INITIAL_WINDOW) {
      this.credit = INITIAL_WINDOW;
    }
    this.flushOut();
  }

  onSocketEnd() {
    if (this.finished) {
      return;
    }
    this.writeDone = true;
    send(CLOSE, this.id);
    this.maybeFinish();
  }

  receive(payload) {
    if (this.finished || this.recvClosed) {
      return;
    }
    if (this.received + payload.length > INITIAL_WINDOW) {
      // The peer sent beyond its credit. Its accounting no longer matches ours,
      // so this stream cannot be trusted; reset it and stop buffering.
      send(RESET, this.id);
      this.abort();
      return;
    }
    this.received += payload.length;
    this.queue.push(payload);
    this.pumpIn();
  }

  peerClosed() {
    if (this.finished) {
      return;
    }
    this.recvClosed = true;
    this.pumpIn();
    this.maybeFinish();
  }

  async pumpIn() {
    if (this.pumping || this.finished) {
      return;
    }
    this.pumping = true;
    try {
      while (!this.finished && this.queue.length > 0) {
        const payload = this.queue.shift();
        const flushed = this.socket.write(payload);
        if (!flushed) {
          await once(this.socket, 'drain');
        }
        if (this.finished) {
          return;
        }
        this.received -= payload.length;
        if (this.received < 0) {
          this.received = 0;
        }
        const window = Buffer.allocUnsafe(4);
        window.writeUInt32BE(payload.length, 0);
        send(WINDOW, this.id, window);
      }
      if (!this.finished && this.recvClosed) {
        this.readDone = true;
        // Half-close the write side only: the socket may still deliver the
        // remainder of the peer's response.
        this.socket.end();
        this.maybeFinish();
      }
    } catch (error) {
      // The socket was destroyed while a payload was being written.
      this.abort();
    } finally {
      this.pumping = false;
    }
  }

  maybeFinish() {
    if (this.finished) {
      return;
    }
    if (this.writeDone && this.readDone) {
      this.finish();
    }
  }

  finish() {
    if (this.finished) {
      return;
    }
    this.finished = true;
    this.mux.remove(this.id);
    this.pendingOut = [];
    this.queue = [];
    this.socket.destroy();
  }

  // abort ends the stream immediately without telling the peer: either it asked
  // for the reset or the transport carrying the answer is already gone.
  abort() {
    this.finished = true;
    this.mux.remove(this.id);
    this.pendingOut = [];
    this.queue = [];
    this.socket.destroy();
  }
}

const mux = {
  streams: new Map(),
  nextId: 0,

  attach(socket) {
    if (closed) {
      socket.destroy();
      return;
    }
    if (this.streams.size >= MAX_STREAMS) {
      socket.destroy();
      return;
    }
    this.nextId += 1;
    const id = this.nextId;
    const stream = new Stream(this, id, socket);
    // Register before OPEN goes out: the peer may answer with RESET (failed
    // dial) or DATA before this function returns.
    this.streams.set(id, stream);
    send(OPEN, id);
  },

  remove(id) {
    this.streams.delete(id);
  },
};

function handleFrame(kind, id, payload) {
  switch (kind) {
    case OPEN:
      // The helper is the accept side: it is the only side that sends OPEN.
      throw new Error('OPEN frame on the accept side');
    case DATA: {
      const stream = mux.streams.get(id);
      if (stream) {
        stream.receive(Buffer.from(payload));
      } else {
        // Data for a stream this side already released. Reset instead of
        // leaving the peer waiting for an answer that will never come.
        send(RESET, id);
      }
      break;
    }
    case WINDOW: {
      if (payload.length !== 4) {
        throw new Error(`WINDOW frame length ${payload.length}`);
      }
      const stream = mux.streams.get(id);
      if (stream) {
        stream.addCredit(payload.readUInt32BE(0));
      }
      break;
    }
    case CLOSE: {
      const stream = mux.streams.get(id);
      if (stream) {
        stream.peerClosed();
      }
      break;
    }
    case RESET: {
      const stream = mux.streams.get(id);
      if (stream) {
        stream.abort();
      }
      break;
    }
    default:
      throw new Error(`unknown frame type ${kind}`);
  }
}

function validateFrame(kind, length) {
  if (length > MAX_PAYLOAD) {
    throw new Error(`frame length ${length} exceeds ${MAX_PAYLOAD}`);
  }
  switch (kind) {
    case DATA:
      return;
    case OPEN:
    case CLOSE:
    case RESET:
      if (length !== 0) {
        throw new Error(`control frame length ${length}`);
      }
      return;
    case WINDOW:
      if (length !== 4) {
        throw new Error(`window frame length ${length}`);
      }
      return;
    default:
      throw new Error(`unknown frame type ${kind}`);
  }
}

function parseFrames() {
  while (buffer.length >= HEADER_SIZE) {
    const kind = buffer[0];
    const id = buffer.readUInt32BE(1);
    const length = buffer.readUInt32BE(5);
    validateFrame(kind, length);
    if (buffer.length < HEADER_SIZE + length) {
      return;
    }
    const payload = buffer.subarray(HEADER_SIZE, HEADER_SIZE + length);
    buffer = buffer.subarray(HEADER_SIZE + length);
    handleFrame(kind, id, payload);
  }
}

let buffer = Buffer.alloc(0);

function shutdown(code) {
  closed = true;
  for (const stream of mux.streams.values()) {
    stream.abort();
  }
  mux.streams.clear();
  process.exit(code);
}

function run() {
  const listener = net.createServer((socket) => mux.attach(socket));
  listener.on('error', (error) => fail(`listen on workspace loopback: ${error.message}`));
  listener.listen(0, '127.0.0.1', () => {
    const address = listener.address();
    if (!address || !address.port) {
      fail('listener did not report a port');
      return;
    }
    const ready = `${READY_PREFIX} proto=${PROTOCOL} port=${address.port}\n`;
    process.stdout.write(ready, (error) => {
      if (error) {
        fail(`announce port: ${error.message}`);
      }
    });
  });

  process.stdin.on('data', (chunk) => {
    buffer = buffer.length === 0 ? chunk : Buffer.concat([buffer, chunk]);
    try {
      parseFrames();
    } catch (error) {
      fail(error.message);
    }
  });
  process.stdin.on('end', () => {
    // stdin EOF is the normal end, but only between frames. Bytes left in the
    // buffer are a frame cut in half, which the Go reader treats as a broken
    // channel; reporting it as a clean exit would hide data loss.
    if (buffer.length > 0) {
      fail(`truncated frame at end of input: ${buffer.length} bytes`);
      return;
    }
    shutdown(0);
  });
  process.stdin.on('error', (error) => fail(`read stdin: ${error.message}`));
  process.stdout.on('error', () => process.exit(1));
}

function main(argv) {
  let stdio = false;
  let protocol = PROTOCOL;
  let wantVersion = false;
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument === '--stdio') {
      stdio = true;
    } else if (argument === '--protocol') {
      index += 1;
      protocol = Number(argv[index]);
    } else if (argument.startsWith('--protocol=')) {
      protocol = Number(argument.slice('--protocol='.length));
    } else if (argument === '--version' || argument === 'version') {
      wantVersion = true;
    } else {
      fail(`unexpected argument ${argument}`, 2);
      return;
    }
  }
  if (wantVersion) {
    process.stdout.write(`aha2-agent-proxy-node ${HELPER_VERSION} protocol=${PROTOCOL}\n`);
    return;
  }
  if (!stdio) {
    fail('only --stdio is supported', 2);
    return;
  }
  if (protocol !== PROTOCOL) {
    fail(`unsupported protocol ${protocol} (this helper speaks ${PROTOCOL})`, 2);
    return;
  }
  run();
}

main(process.argv.slice(2));
