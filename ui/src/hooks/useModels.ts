import { useState, useEffect } from 'react'
import type { ModelInfo } from '../types'

export function useModels() {
  const [models, setModels] = useState<ModelInfo[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    fetch('/api/models')
      .then(r => r.json())
      .then(data => {
        setModels(data.data || [])
      })
      .catch(() => setModels([]))
      .finally(() => setLoading(false))
  }, [])

  return { models, loading }
}
