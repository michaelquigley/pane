import { useState, useEffect } from 'react'
import { useLocalStorage } from './useLocalStorage'
import type { ModelInfo } from '../types'

export function useModels() {
  const [models, setModels] = useState<ModelInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [selectedModel, setSelectedModel] = useLocalStorage<string>('pane:model', '')

  useEffect(() => {
    fetch('/api/models')
      .then(r => r.json())
      .then(data => {
        const list = data.data || []
        setModels(list)
        if (!selectedModel && list.length > 0) {
          setSelectedModel(list[0].id)
        }
      })
      .catch(() => setModels([]))
      .finally(() => setLoading(false))
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  return { models, selectedModel, setSelectedModel, loading }
}
