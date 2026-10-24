// AHA2 live-inbox plugin for DSH headless.
//
// It turns the Agent API inbox into an in-turn checkpoint: while a step is
// running it claims pending Owner/channel messages from AHA, then steers them
// into the next step and acknowledges the delivery. A failed steer rolls the
// claim back so the message survives for a later turn.
//
// Timing matters. session/event is a post-commit observer feed, and calling
// agent.steer() from its listener re-enters session append and throws:
//   session append cannot reenter while another append is being published
// Claims are therefore prefetched during the step (assistant/message, tool/call)
// so the text is already local when tool/result fires. The steer itself runs
// from a microtask. The turn-stopping hook is serial and awaited, so it can
// claim and steer directly; steering there reopens the turn with one more step
// instead of losing the message to the next turn.

export const name = 'aha-live-inbox'
export const inject = ['agents']

const CLAIM_PATH = '/api/v1/agent/turn/inbox/claim'
const ACK_PATH = '/api/v1/agent/turn/inbox/ack'
const REQUEST_TIMEOUT_MS = 2500
const MAX_BATCH = 20

const chains = new WeakMap()
const caches = new WeakMap()

function env(name) {
  return typeof process === 'undefined' ? '' : String(process.env[name] || '').trim()
}

function enabled() {
  return env('AHA2_LIVE_INBOX') !== '0'
}

function baseURL() {
  return env('AHA2_AGENT_API_URL') || env('DSH_AHA_AGENT_API_URL')
}

function apiToken() {
  return env('AHA2_AGENT_API_TOKEN') || env('DSH_AHA_AGENT_API_TOKEN')
}

function logDebug(message, detail) {
  if (env('AHA2_LIVE_INBOX_DEBUG') === '1') {
    try {
      process.stderr.write('[aha-live-inbox] ' + message + (detail ? ' ' + String(detail) : '') + '\n')
    } catch {}
  }
}

async function call(path, body) {
  const base = baseURL()
  const token = apiToken()
  if (!base || !token) return null
  const response = await fetch(base.replace(/\/+$/, '') + path, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json; charset=utf-8',
      Authorization: 'Bearer ' + token,
    },
    body: JSON.stringify(body),
    signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
  })
  if (!response.ok) return null
  return await response.json().catch(() => null)
}

function fallbackText(messages) {
  const lines = [
    '[AHA live inbox] New messages arrived while this Turn is running. Apply them now before continuing.',
  ]
  for (const message of messages) {
    lines.push('', '---', String(message.content || '').trim() || '(no text)')
  }
  return lines.join('\n')
}

function newMessage(text) {
  const id =
    typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
      ? crypto.randomUUID()
      : 'steer-' + Date.now() + '-' + Math.random().toString(16).slice(2)
  return {
    id,
    role: 'user',
    content: [{ type: 'text', text }],
    source: { kind: 'user' },
  }
}

async function claimIntoCache(agent) {
  if (!enabled() || !agent || agent.status !== 'running' || caches.has(agent)) return
  const claim = await call(CLAIM_PATH, { limit: MAX_BATCH })
  const messages = Array.isArray(claim?.messages) ? claim.messages : []
  if (messages.length === 0) return
  caches.set(agent, {
    text: String(claim.injection_text || '').trim() || fallbackText(messages),
    ids: messages.map((message) => String(message.id || '')).filter(Boolean),
  })
  logDebug('claimed ' + messages.length + ' message(s)')
}

async function steerCached(agent) {
  if (!enabled() || !agent || agent.status !== 'running') return
  const cached = caches.get(agent)
  if (!cached) return
  caches.delete(agent)
  try {
    agent.steer(newMessage(cached.text))
  } catch (error) {
    logDebug('steer failed', error && error.message ? error.message : error)
    if (cached.ids.length > 0) {
      await call(ACK_PATH, { message_ids: cached.ids, outcome: 'failed' }).catch(() => null)
    }
    return
  }
  if (cached.ids.length > 0) {
    await call(ACK_PATH, { message_ids: cached.ids, outcome: 'delivered' }).catch(() => null)
  }
  logDebug('steered ' + cached.ids.length + ' message(s)')
}

// All work for one agent is serialized: a prefetch that is still in flight is
// awaited before a boundary attempts to steer, and two boundaries never claim
// the same pending message.
function enqueue(agent, task) {
  const previous = chains.get(agent) || Promise.resolve()
  const next = previous.catch(() => undefined).then(task)
  chains.set(agent, next)
  return next
}

function prefetch(agent) {
  return enqueue(agent, () => claimIntoCache(agent)).catch(() => undefined)
}

function steerAtBoundary(agent) {
  return enqueue(agent, async () => {
    if (!caches.has(agent)) await claimIntoCache(agent)
    await steerCached(agent)
  }).catch(() => undefined)
}

export function apply(ctx) {
  ctx.on('session/event', (session, event) => {
    const agent = ctx.agents.get(session.id)
    if (!agent) return
    if (event.type === 'assistant/message' || event.type === 'tool/call') {
      queueMicrotask(() => {
        prefetch(agent)
      })
      return
    }
    if (event.type !== 'tool/result') return
    queueMicrotask(() => {
      steerAtBoundary(agent)
    })
  })

  ctx.on('agent/turn-stopping', ({ agent }) => {
    return steerAtBoundary(agent)
  })
}
