import type { ModelInfo } from '../types'

interface Props {
  models: ModelInfo[]
  defaultModel: string
  selected: string
  onChange: (model: string) => void
}

export function ModelSelector({ models, defaultModel, selected, onChange }: Props) {
  const hasSelectedModel = !selected || models.some(m => m.id === selected)

  return (
    <select
      className="model-selector"
      value={selected}
      onChange={e => onChange(e.target.value)}
    >
      <option value="">
        {defaultModel ? `default (${defaultModel})` : 'default'}
      </option>
      {!hasSelectedModel && selected && (
        <option value={selected}>{selected}</option>
      )}
      {models.map(m => (
        <option key={m.id} value={m.id}>{m.id}</option>
      ))}
    </select>
  )
}
