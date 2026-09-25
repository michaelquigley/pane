import type { ActiveToolCall, SSEEvent } from '../types'

type ToolPreviewEvent = Extract<SSEEvent, { type: 'tool_call_start' | 'tool_call_args' }>

export function getOrCreateActiveToolCall(
  toolCallsAccum: Map<number, ActiveToolCall>,
  index: number,
): ActiveToolCall {
  let tc = toolCallsAccum.get(index)
  if (!tc) {
    tc = {
      index,
      name: '',
      status: 'loading',
      argumentsSoFar: '',
    }
    toolCallsAccum.set(index, tc)
  }
  return tc
}

export function applyToolPreview(
  toolCallsAccum: Map<number, ActiveToolCall>,
  event: ToolPreviewEvent,
): Map<number, ActiveToolCall> {
  const tc = { ...getOrCreateActiveToolCall(toolCallsAccum, event.index) }
  if (event.id) tc.id = event.id
  if (event.type === 'tool_call_start') {
    if (event.name) tc.name = event.name
  } else {
    tc.argumentsSoFar += event.arguments_partial
    tc.status = 'args_streaming'
  }
  toolCallsAccum.set(event.index, tc)
  return new Map(toolCallsAccum)
}
