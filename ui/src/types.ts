export interface Conversation {
  id: string
  title: string
  messages: Message[]
  createdAt: number
  updatedAt: number
}

export interface Message {
  role: 'system' | 'user' | 'assistant' | 'tool'
  content: string | null
  tool_calls?: ToolCall[]
  tool_call_id?: string
}

export interface ToolCall {
  id: string
  type: 'function'
  function: {
    name: string
    arguments: string
  }
}

export interface ActiveToolCall {
  id: string
  name: string
  status: 'loading' | 'args_streaming' | 'awaiting_approval' | 'executing' | 'complete' | 'error'
  argumentsSoFar: string
  result?: string
  durationMs?: number
  error?: string
}

export type SSEEvent =
  | { type: 'delta'; content: string }
  | { type: 'tool_call_start'; id: string; name: string }
  | { type: 'tool_call_args'; id: string; arguments_partial: string }
  | { type: 'tool_call_executing'; id: string; name: string }
  | { type: 'tool_call_approve'; id: string; name: string; arguments: string }
  | { type: 'tool_call_result'; id: string; name: string; content: string; duration_ms: number }
  | { type: 'error'; code: string; message: string; tool_call_id?: string }
  | { type: 'done' }

export interface ToolInfo {
  server: string
  enabled: boolean
  function: {
    name: string
    description: string
    parameters: Record<string, unknown>
  }
}

export interface ServerStatus {
  status: 'running' | 'error' | 'starting'
  tools_count: number
  error?: string
}

export interface ToolsResponse {
  tools: ToolInfo[]
  servers: Record<string, ServerStatus>
}

export interface ModelsResponse {
  object: string
  data: ModelInfo[]
}

export interface ModelInfo {
  id: string
  object: string
  owned_by: string
}
