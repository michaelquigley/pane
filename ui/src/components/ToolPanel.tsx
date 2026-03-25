import type { ToolInfo, ServerStatus } from '../types'

interface Props {
  tools: ToolInfo[]
  servers: Record<string, ServerStatus>
  onToggle: (name: string, enabled: boolean) => void
  onClose: () => void
}

export function ToolPanel({ tools, servers, onToggle, onClose }: Props) {
  const grouped = new Map<string, ToolInfo[]>()
  for (const tool of tools) {
    const list = grouped.get(tool.server) || []
    list.push(tool)
    grouped.set(tool.server, list)
  }

  return (
    <div className="tool-panel">
      <div className="tool-panel-header">
        <h3>Tools</h3>
        <button className="tool-panel-close" onClick={onClose}>&times;</button>
      </div>
      <div className="tool-panel-content">
        {Array.from(grouped.entries()).map(([serverName, serverTools]) => {
          const status = servers[serverName]
          return (
            <div key={serverName} className="tool-server-group">
              <div className="tool-server-header">
                <span className={`server-dot ${status?.status || 'error'}`} />
                <span className="server-name">{serverName}</span>
                <span className="server-count">{serverTools.length} tools</span>
              </div>
              {serverTools.map(tool => (
                <label key={tool.function.name} className="tool-toggle">
                  <input
                    type="checkbox"
                    checked={tool.enabled}
                    onChange={e => onToggle(tool.function.name, e.target.checked)}
                  />
                  <span className="tool-toggle-name">{tool.function.name.split('_').slice(1).join('_')}</span>
                </label>
              ))}
            </div>
          )
        })}
        {tools.length === 0 && (
          <p className="tool-panel-empty">No MCP tools configured.</p>
        )}
      </div>
    </div>
  )
}
