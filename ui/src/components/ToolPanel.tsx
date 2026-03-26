import type { ToolInfo, ServerStatus } from '../types'

interface Props {
  tools: ToolInfo[]
  servers: Record<string, ServerStatus>
  separator: string
  onClose: () => void
}

export function ToolPanel({ tools, servers, separator, onClose }: Props) {
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
                <div key={tool.function.name} className="tool-entry">
                  <div className="tool-entry-name">{displayToolName(tool.function.name, tool.server, separator)}</div>
                  {tool.function.description && (
                    <div className="tool-entry-description">{tool.function.description}</div>
                  )}
                </div>
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

function displayToolName(qualifiedName: string, serverName: string, separator: string): string {
  const prefix = `${serverName}${separator}`
  if (qualifiedName.startsWith(prefix)) {
    return qualifiedName.slice(prefix.length)
  }
  return qualifiedName
}
