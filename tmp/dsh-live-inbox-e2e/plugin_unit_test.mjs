// Focused unit test for internal/backend/aha_live_inbox.mjs using a fake ctx.
// Verifies successful steer -> ack delivered and failed steer -> ack failed.
import assert from 'node:assert/strict'

const plugin = await import('../../internal/backend/aha_live_inbox.mjs')

process.env.AHA2_AGENT_API_URL = 'http://mock'
process.env.AHA2_AGENT_API_TOKEN = 'token'
process.env.AHA2_LIVE_INBOX = '1'

const calls = []
let claimPayload = {
  messages: [{ id: 'inbox-1', content: 'do it' }],
  injection_text: '[AHA live inbox] do it',
}
globalThis.fetch = async (url, options) => {
  const body = JSON.parse(options.body)
  calls.push({ url, body })
  if (url.endsWith('/claim')) {
    const payload = claimPayload
    claimPayload = { messages: [], injection_text: '' }
    return { ok: true, json: async () => payload }
  }
  return { ok: true, json: async () => ({ ok: true }) }
}

const handlers = new Map()
const agent = {
  status: 'running',
  steered: [],
  steer(message) {
    this.steered.push(message)
  },
}
const ctx = {
  agents: { get: () => agent },
  on(event, handler) {
    handlers.set(event, handler)
  },
}
plugin.apply(ctx)

const tick = () => new Promise((resolve) => setTimeout(resolve, 30))
const emit = async (type) => {
  handlers.get('session/event')({ id: 'session-1' }, { type })
  await tick()
}

// Assistant message prefetches the claim; tool/result steers it.
await emit('assistant/message')
assert.equal(agent.steered.length, 0, 'prefetch must not steer early')
await emit('tool/result')
assert.equal(agent.steered.length, 1, 'tool boundary must steer the cached message')
assert.equal(agent.steered[0].role, 'user')
assert.equal(agent.steered[0].content[0].text, '[AHA live inbox] do it')
assert.equal(calls.filter((call) => call.url.endsWith('/ack')).at(-1).body.outcome, 'delivered')

// A steer failure rolls the claim back instead of losing it.
claimPayload = {
  messages: [{ id: 'inbox-2', content: 'second' }],
  injection_text: '[AHA live inbox] second',
}
agent.steer = () => {
  throw new Error('injected failure')
}
await emit('assistant/message')
await emit('tool/result')
assert.equal(calls.filter((call) => call.url.endsWith('/ack')).at(-1).body.outcome, 'failed')
assert.deepEqual(calls.filter((call) => call.url.endsWith('/ack')).at(-1).body.message_ids, ['inbox-2'])

// turn-stopping can claim and steer with no tool result at all.
claimPayload = {
  messages: [{ id: 'inbox-3', content: 'third' }],
  injection_text: '[AHA live inbox] third',
}
agent.steered = []
agent.steer = (message) => agent.steered.push(message)
const turnStopping = handlers.get('agent/turn-stopping')
assert.ok(turnStopping, 'turn-stopping listener is registered')
await turnStopping({ agent })
assert.equal(agent.steered.length, 1)
assert.equal(calls.filter((call) => call.url.endsWith('/ack')).at(-1).body.outcome, 'delivered')

// Disabled mode never calls the control plane.
process.env.AHA2_LIVE_INBOX = '0'
const before = calls.length
await emit('assistant/message')
await emit('tool/result')
assert.equal(calls.length, before, 'disabled plugin must not call AHA')

console.log('plugin unit test: PASS')
