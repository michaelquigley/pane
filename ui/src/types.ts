// the session document: exactly what one file under the data directory
// holds. there is no id field -- the id is the store's key, the file's name,
// and every store operation takes it explicitly, so the in-memory and on-disk
// shapes are identical and a round trip is byte-transparent.
export interface Conversation {
  title: string
  messages: Message[]
  createdAt: number
  updatedAt: number
  usage?: UsageRecord | null
}

// the working copy's element: the store's key paired with the document it
// addresses. the pairing is what keeps the id out of the body while leaving
// it in hand for every save, remove, and selection find.
export interface StoredConversation {
  id: string
  doc: Conversation
}

// the rail's projection of a stored conversation, as GET /api/sessions
// reports it.
export interface SessionSummary {
  id: string
  title: string
  createdAt: number
  updatedAt: number
}

export interface UsageRecord {
  promptTokens: number
  completionTokens: number
  totalTokens: number
  model: string
  at: number
}

export interface Message {
  role: 'system' | 'user' | 'assistant' | 'tool'
  content: string | null
  tool_calls?: ToolCall[]
  tool_call_results?: Record<string, ToolCallResult>
  tool_call_id?: string
  thinking?: string
  thinkingCollapsed?: boolean
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
  context_windows: Record<string, number>
  default_context_window: number
}

export interface ChatPreferences {
  modelOverride: string | null
  systemPromptMode: SystemPromptMode
  systemPromptCustom: string
}

export type SSEEvent =
  | { type: 'delta'; content: string }
  | { type: 'thinking_delta'; content: string }
  | { type: 'tool_call_start'; index: number; id: string; name: string }
  | { type: 'tool_call_args'; index: number; id: string; arguments_partial: string }
  | { type: 'tool_call_executing'; index: number; id: string; name: string }
  | { type: 'tool_call_approve'; index: number; id: string; name: string; arguments: string }
  | { type: 'tool_call_result'; index: number; id: string; name: string; status: 'complete' | 'error'; error_code?: ToolCallErrorCode; content: string; duration_ms: number }
  | { type: 'usage'; prompt_tokens: number; completion_tokens: number; total_tokens: number }
  | { type: 'round_complete'; assistant: Message; tool_messages: Message[] }
  | { type: 'error'; code: string; message: string; tool_call_id?: string }
  | { type: 'done' }

export interface ToolInfo {
  server: string
  name: string
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
