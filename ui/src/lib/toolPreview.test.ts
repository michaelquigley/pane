import { describe, expect, it } from 'vitest'
import type { ActiveToolCall } from '../types'
import { applyToolPreview } from './toolPreview'

const callId = 'pane_call_1_0_0'

describe('tool preview updates', () => {
  it('uses a name present in the first chunk', () => {
    const calls = new Map<number, ActiveToolCall>()
    const initial = applyToolPreview(calls, {
      type: 'tool_call_start', index: 0, id: callId, name: 'read',
    })
    expect(initial.size).toBe(1)
    expect(initial.get(0)).toMatchObject({ id: callId, name: 'read', status: 'loading' })

    const withArgs = applyToolPreview(calls, {
      type: 'tool_call_args', index: 0, id: callId, arguments_partial: '{"path":"README.md"}',
    })
    expect(withArgs.size).toBe(1)
    expect(withArgs.get(0)).toMatchObject({
      id: callId, name: 'read', status: 'args_streaming', argumentsSoFar: '{"path":"README.md"}',
    })
  })

  it('updates the same call when the name arrives after arguments', () => {
    const calls = new Map<number, ActiveToolCall>()
    const initial = applyToolPreview(calls, {
      type: 'tool_call_start', index: 0, id: callId, name: '',
    })
    applyToolPreview(calls, {
      type: 'tool_call_args', index: 0, id: callId, arguments_partial: '{"path":',
    })
    const argsBeforeName = applyToolPreview(calls, {
      type: 'tool_call_args', index: 0, id: callId, arguments_partial: '"README.md"',
    })
    const named = applyToolPreview(calls, {
      type: 'tool_call_start', index: 0, id: callId, name: 'read',
    })
    const complete = applyToolPreview(calls, {
      type: 'tool_call_args', index: 0, id: callId, arguments_partial: '}',
    })

    expect(initial.size).toBe(1)
    expect(argsBeforeName.get(0)).toMatchObject({
      id: callId, name: '', status: 'args_streaming', argumentsSoFar: '{"path":"README.md"',
    })
    expect(named.size).toBe(1)
    expect(named.get(0)).toMatchObject({
      id: callId, name: 'read', status: 'args_streaming', argumentsSoFar: '{"path":"README.md"',
    })
    expect(complete.size).toBe(1)
    expect(complete.get(0)).toMatchObject({
      id: callId, name: 'read', status: 'args_streaming', argumentsSoFar: '{"path":"README.md"}',
    })
  })
})
