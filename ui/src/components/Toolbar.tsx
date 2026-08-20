import { ContextMeter } from './ContextMeter'
import { ModelSelector } from './ModelSelector'
import { SystemPromptEditor } from './SystemPromptEditor'
import {
  AddBoxIcon,
  ConstructionIcon,
  ForumIcon,
  PsychologyIcon,
  UploadIcon,
} from './icons'
import type { ModelInfo, SystemPromptMode, UsageRecord } from '../types'

interface Props {
  conversationsOpen: boolean
  onToggleConversations: () => void
  canCreate: boolean
  onNew: () => void
  canExport: boolean
  onExport: () => void
  models: ModelInfo[]
  defaultModel: string
  modelOverride: string
  onModelChange: (model: string) => void
  mode: SystemPromptMode
  customValue: string
  defaultValue: string
  onModeChange: (mode: SystemPromptMode) => void
  onCustomChange: (value: string) => void
  toolsCount: number
  toolsOpen: boolean
  onToggleTools: () => void
  usage: UsageRecord | null
  selectedModel: string
  contextWindows: Record<string, number>
  defaultContextWindow: number
}

// the family's standing chrome, painted in pane's palette: a quiet bar over
// the rail and the chat, a centered glyph cluster grouped by whitespace, and
// the signal column at the right. presentation-only — it renders app state
// and calls the handlers App owns.
export function Toolbar({
  conversationsOpen,
  onToggleConversations,
  canCreate,
  onNew,
  canExport,
  onExport,
  models,
  defaultModel,
  modelOverride,
  onModelChange,
  mode,
  customValue,
  defaultValue,
  onModeChange,
  onCustomChange,
  toolsCount,
  toolsOpen,
  onToggleTools,
  usage,
  selectedModel,
  contextWindows,
  defaultContextWindow,
}: Props) {
  // the count rides the glyph's label while positive: an aria-label overrides
  // descendant text, so a fixed label would take the count away from
  // assistive tech the way the old "Tools (n)" text button exposed it.
  const toolsLabel = toolsCount > 0 ? `tools (${toolsCount})` : 'tools'

  return (
    <header className="toolbar">
      <nav className="toolbar-nav" aria-label="primary">
        <div className="toolbar-cluster">
          {/* group 1 — the conversation */}
          <button
            className="toolbar-link toolbar-glyph"
            title="new conversation"
            aria-label="new conversation"
            onClick={onNew}
            disabled={!canCreate}
          >
            <AddBoxIcon />
          </button>
          <button
            className={`toolbar-link toolbar-glyph${conversationsOpen ? ' lit' : ''}`}
            title="conversations"
            aria-label="conversations"
            aria-pressed={conversationsOpen}
            onClick={onToggleConversations}
          >
            <ForumIcon />
          </button>
          <button
            className="toolbar-link toolbar-glyph"
            title="export"
            aria-label="export"
            onClick={onExport}
            disabled={!canExport}
          >
            <UploadIcon />
          </button>

          <span className="toolbar-cluster-gap" aria-hidden="true" />

          {/* group 2 — the reply */}
          <span className="toolbar-model">
            <span className="toolbar-glyph" title="model" aria-hidden="true">
              <PsychologyIcon />
            </span>
            <ModelSelector
              models={models}
              defaultModel={defaultModel}
              selected={modelOverride}
              onChange={onModelChange}
            />
          </span>
          <SystemPromptEditor
            mode={mode}
            customValue={customValue}
            defaultValue={defaultValue}
            onModeChange={onModeChange}
            onCustomChange={onCustomChange}
          />

          <span className="toolbar-cluster-gap" aria-hidden="true" />

          {/* group 3 — machinery */}
          <button
            className={`toolbar-link toolbar-glyph toolbar-tools${toolsOpen ? ' lit' : ''}`}
            title={toolsLabel}
            aria-label={toolsLabel}
            aria-pressed={toolsOpen}
            onClick={onToggleTools}
          >
            <ConstructionIcon />
            {toolsCount > 0 && (
              <span className="tool-badge" aria-hidden="true">
                {toolsCount}
              </span>
            )}
          </button>
        </div>

        <div className="toolbar-signals">
          <ContextMeter
            usage={usage}
            selectedModel={selectedModel}
            contextWindows={contextWindows}
            defaultContextWindow={defaultContextWindow}
          />
        </div>
      </nav>
    </header>
  )
}