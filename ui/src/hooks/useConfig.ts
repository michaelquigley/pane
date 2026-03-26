import { useEffect, useState } from 'react'
import type { ConfigResponse } from '../types'

const emptyConfig: ConfigResponse = {
  default_model: '',
  default_system: '',
  mcp_separator: '_',
}

export function useConfig() {
  const [config, setConfig] = useState<ConfigResponse>(emptyConfig)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    fetch('/api/config')
      .then(r => r.json())
      .then(data => {
        setConfig({
          default_model: data.default_model || '',
          default_system: data.default_system || '',
          mcp_separator: data.mcp_separator || '_',
        })
      })
      .catch(() => setConfig(emptyConfig))
      .finally(() => setLoading(false))
  }, [])

  return { config, loading }
}
