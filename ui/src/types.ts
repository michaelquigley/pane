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
  tool_call_results?: Record<string, ToolCallResult>
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
  index: number
  id?: string
  name: string
  status: 'loading' | 'args_streaming' | 'awaiting_approval' | 'executing' | 'complete' | 'error'
  argumentsSoFar: string
  result?: string
  durationMs?: number
  errorCode?: ToolCallErrorCode
}

export type ToolCallErrorCode =
  | 'denied'
  | 'approval_timeout'
  | 'cancelled'
  | 'malformed_arguments'
  | 'execution_error'

export interface ToolCallResult {
  status: 'complete' | 'error'
  error_code?: ToolCallErrorCode
  content: string
  duration_ms: number
}

export type SystemPromptMode = 'default' | 'custom' | 'none'

export interface ConfigResponse {
  default_model: string
  default_system: string
  mcp_separator: string
}

export interface ChatPreferences {
  modelOverride: string | null
  systemPromptMode: SystemPromptMode
  systemPromptCustom: string
}

export type SSEEvent =
  | { type: 'delta'; content: string }
  | { type: 'tool_call_start'; index: number; id: string; name: string }
  | { type: 'tool_call_args'; index: number; id: string; arguments_partial: string }
  | { type: 'tool_call_executing'; index: number; id: string; name: string }
  | { type: 'tool_call_approve'; index: number; id: string; name: string; arguments: string }
  | { type: 'tool_call_result'; index: number; id: string; name: string; status: 'complete' | 'error'; error_code?: ToolCallErrorCode; content: string; duration_ms: number }
  | { type: 'round_complete'; assistant: Message; tool_messages: Message[] }
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
