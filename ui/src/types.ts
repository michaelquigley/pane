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
  // the recovery record of each submitted turn. optional: documents written
  // before turn records existed remain valid and need no rewrite.
  turns?: TurnRecord[]
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
  tool_call_id?: string
  // provenance and opaque provider continuation, preserved but never
  // interpreted by the browser.
  origin?: RoundOrigin
  continuation?: unknown
  // bindings to the turn record, and the kind of a recovery placeholder.
  turn_id?: string
  round_id?: string
  recovery_placeholder?: RecoveryPlaceholder
  // display-only decorations: never part of a chat request.
  tool_call_results?: Record<string, ToolCallResult>
  thinking?: string
  thinkingCollapsed?: boolean
}

export interface RoundOrigin {
  alias: string
  identity: {
    provider: string
    protocol: string
    upstream_model: string
    service: string
    profile?: string
    account_scope?: string
  }
  requested_effort?: string
  effective_effort?: string
}

export type RecoveryPlaceholder = 'not_executed' | 'unknown' | 'operator_reported'

export type TurnState = 'in_progress' | 'completed' | 'interrupted'

export type CallState =
  | 'pending'
  | 'denied'
  | 'rejected'
  | 'not_dispatched'
  | 'dispatched'
  | 'completed'
  | 'failed'
  | 'unknown'

export type Execution = 'none' | 'known' | 'unknown'

export interface CallRecord extends ToolCall {
  state: CallState
  result?: { content: string; is_error: boolean }
  reconciliation?: { execution: Execution; note: string }
}

export interface RoundRecord {
  round_id: string
  // the finalized assistant, pending until normal promotion; retained as
  // evidence when a recovery projection places it in history instead.
  assistant?: Message
  message_index?: number
  calls: CallRecord[]
  committed: boolean
}

export interface TurnRecord {
  id: string
  v: 1
  model_alias: string
  user_message_index: number
  state: TurnState
  last_seq: number
  rounds: RoundRecord[]
  origin?: RoundOrigin
  partial_text?: string
  error_code?: string
  terminal?: { outcome: 'completed' | 'failed' | 'cancelled'; execution: Execution }
  reconciliation?: { execution: Execution; note: string; message_index: number }
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
  status: 'loading' | 'args_streaming' | 'awaiting_approval' | 'executing' | 'complete' | 'error' | 'recovered'
  // set on a card closed by a recovery projection rather than a tool reply.
  recovery?: RecoveryPlaceholder
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
  duration_ms?: number
  // present when the content is a pane recovery placeholder, never a tool
  // reply.
  recovery?: RecoveryPlaceholder
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

// every event names its turn; critical lifecycle events also carry 'seq',
// and round/call events their round.
interface TurnBound { turn_id?: string; round_id?: string }
interface Critical { turn_id: string; seq: number }

export type SSEEvent =
  | ({ type: 'delta'; content: string } & TurnBound)
  | ({ type: 'thinking_delta'; content: string } & TurnBound)
  | ({ type: 'tool_call_start'; index: number; id: string; name: string } & TurnBound)
  | ({ type: 'tool_call_args'; index: number; id: string; arguments_partial: string } & TurnBound)
  | ({ type: 'tool_call_executing'; index: number; id: string; name: string } & TurnBound & Critical)
  | ({ type: 'tool_call_approve'; index: number; id: string; name: string; arguments: string } & TurnBound & Critical)
  | ({ type: 'tool_call_result'; index: number; id: string; name: string; status: 'complete' | 'error'; error_code?: ToolCallErrorCode; content: string; duration_ms: number; execution_state: 'not_dispatched' | 'result_received' | 'unknown' } & TurnBound & Critical)
  | ({ type: 'usage'; prompt_tokens: number; completion_tokens: number; total_tokens: number } & TurnBound)
  | ({ type: 'turn_start'; alias: string; origin?: RoundOrigin } & Critical)
  | ({ type: 'round_ready'; round_id: string; assistant: Message; finish: string } & Critical)
  | ({ type: 'round_complete'; round_id: string; assistant: Message; tool_messages: Message[] } & Critical)
  | ({ type: 'turn_end'; outcome: 'completed' | 'failed' | 'cancelled'; execution: Execution; error_code?: string; message?: string; partial_text?: string } & Critical)
  | ({ type: 'error'; code: string; message: string; tool_call_id?: string } & TurnBound)
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

export type AuthState = 'not_required' | 'login_required' | 'credential_available' | 'refresh_pending' | 'error'

export interface ModelInfo {
  id: string
  object: string
  owned_by: string
  // registry availability; absent from legacy listings.
  provider?: string
  auth_state?: AuthState
  last_error?: { code: string; message: string; at: number }
}
