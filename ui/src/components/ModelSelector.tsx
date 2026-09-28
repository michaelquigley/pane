import type { ModelInfo } from '../types'

interface Props {
  models: ModelInfo[]
  defaultModel: string
  selected: string
  onChange: (model: string) => void
  // one selected model owns the whole turn, including approvals.
  disabled?: boolean
}

function availabilityLabel(model: ModelInfo): string {
  switch (model.auth_state) {
    case 'login_required':
      return `${model.id} (sign in)`
    case 'error':
      return `${model.id} (auth error)`
    default:
      return model.id
  }
}

// signed-out aliases stay selectable: selection is preserved and sending is
// what the app disables, with cli guidance.
export function ModelSelector({ models, defaultModel, selected, onChange, disabled = false }: Props) {
  const hasSelectedModel = !selected || models.some(m => m.id === selected)

  return (
    <select
      className="model-selector"
      aria-label="model"
      value={selected}
      disabled={disabled}
      onChange={e => onChange(e.target.value)}
    >
      <option value="">
        {defaultModel ? `default (${defaultModel})` : 'default'}
      </option>
      {!hasSelectedModel && selected && (
        <option value={selected}>{selected}</option>
      )}
      {models.map(m => (
        <option key={m.id} value={m.id}>{availabilityLabel(m)}</option>
      ))}
    </select>
  )
}
