import type { ModelInfo } from '../types'

interface Props {
  models: ModelInfo[]
  selected: string
  onChange: (model: string) => void
}

export function ModelSelector({ models, selected, onChange }: Props) {
  return (
    <select
      className="model-selector"
      value={selected}
      onChange={e => onChange(e.target.value)}
    >
      {models.map(m => (
        <option key={m.id} value={m.id}>{m.id}</option>
      ))}
    </select>
  )
}
