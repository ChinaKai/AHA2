// Spike plugin: claim the live agent at a tool boundary and steer a message
// into the running turn. It mirrors the proposed AHA live-inbox plugin, but
// injects a fixed marker instead of calling the AHA inbox API.
export const name = 'aha-live-inbox-spike'
export const inject = ['agents']

import { appendFileSync } from 'node:fs'
import { randomUUID } from 'node:crypto'

export function apply(ctx) {
  const trigger = process.env.SPIKE_TRIGGER || 'tool-result'
  const logPath = process.env.SPIKE_STEER_LOG || ''
  const steered = new Set()
  const write = (record) => {
    if (!logPath) return
    try {
      appendFileSync(logPath, JSON.stringify({ t: new Date().toISOString(), ...record }) + '\n')
    } catch {}
  }

  ctx.on('session/event', (session, event) => {
    write({
      kind: 'session-event',
      event: event.type,
      seq: event.seq,
      turn: event.turn,
      step: event.step,
    })
    if (event.type !== 'tool/result') return
    if (trigger === 'turn-stopping') {
      write({ kind: 'tool-result-observed', sessionId: session.id })
      return
    }

    // Q1 of the spike: can a plugin locate the live agent from the session
    // event alone, without the model or the prompt cooperating?
    const agent = ctx.agents.get(session.id)
    write({
      kind: 'tool-result-probe',
      sessionId: session.id,
      agentFound: Boolean(agent),
      agentStatus: agent?.status,
    })
    if (!agent || steered.has(session.id)) return
    steered.add(session.id)
    const text =
      'SPIKE-STEER-MARKER: this message was injected at a tool/result boundary, ' +
      'before the next model request. Reply exactly SPIKE-STEER-RECEIVED and stop.'
    const doSteer = () => {
      try {
        write({ kind: 'steer-attempt', sessionId: session.id, hasSteer: typeof agent.steer === 'function' })
        agent.steer({
          id: randomUUID(),
          role: 'user',
          content: [{ type: 'text', text }],
          source: { kind: 'user' },
        })
        write({ kind: 'steered', sessionId: session.id, inboxNextStep: agent.inbox?.nextStep?.length })
      } catch (error) {
        write({
          kind: 'steer-error',
          sessionId: session.id,
          name: error?.name,
          message: String(error?.message || error),
          stack: String(error?.stack || '').split('\n').slice(0, 6),
        })
      }
    }
    // session/event is a post-commit fire-and-forget feed: appending from the
    // listener re-enters the publication. Deferring is required.
    const mode = process.env.SPIKE_DEFER || 'microtask'
    write({ kind: 'defer', mode })
    if (mode === 'microtask') queueMicrotask(doSteer)
    else if (mode === 'timeout') setTimeout(doSteer, 0)
    else doSteer()
  })

  if (trigger === 'turn-stopping') applyTurnStopping(ctx, write)
}

// Optional second variant: the turn-stopping boundary is an awaited serial hook,
// so steering there is documented to reopen the turn with another step.
export function applyTurnStopping(ctx, write) {
  ctx.on('agent/turn-stopping', ({ agent }) => {
    const key = agent.session?.id
    if (!key || steeredForStop.has(key)) return
    steeredForStop.add(key)
    write({ kind: 'turn-stopping-probe', sessionId: key })
    try {
      agent.steer({
        id: randomUUID(),
        role: 'user',
        content: [{ type: 'text', text: 'SPIKE-STEER-MARKER: injected at turn-stopping.' }],
        source: { kind: 'user' },
      })
      write({ kind: 'turn-stopping-steered', sessionId: key })
    } catch (error) {
      write({ kind: 'turn-stopping-error', message: String(error?.message || error) })
    }
  })
}

const steeredForStop = new Set()
