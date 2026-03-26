package mcp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcptypes "github.com/mark3labs/mcp-go/mcp"
	"github.com/michaelquigley/df/dl"
	"github.com/michaelquigley/pane/internal/config"
	"github.com/michaelquigley/pane/internal/llm"
)

type Manager struct {
	servers   map[string]*ServerInstance
	separator string
	mu        sync.RWMutex
}

type ServerInstance struct {
	name     string
	config   *config.ServerConfig
	client   *mcpclient.Client
	tools    []mcptypes.Tool
	status   string
	err      string
	disabled map[string]bool
}

func NewManager(cfg *config.MCPConfig) *Manager {
	m := &Manager{
		servers:   make(map[string]*ServerInstance),
		separator: "_",
	}
	if cfg == nil {
		return m
	}
	if cfg.Separator != "" {
		m.separator = cfg.Separator
	}
	for name, sc := range cfg.Servers {
		m.servers[name] = &ServerInstance{
			name:     name,
			config:   sc,
			status:   "starting",
			disabled: make(map[string]bool),
		}
	}
	return m
}

func (m *Manager) Start(ctx context.Context) {
	for name, si := range m.servers {
		dl.Infof("starting MCP server: %s", name)
		if err := m.startServer(ctx, si); err != nil {
			si.status = "error"
			si.err = err.Error()
			dl.Warnf("MCP server %s failed: %v", name, err)
			continue
		}
		si.status = "running"
		dl.Infof("MCP server ready: %s (%d tools)", name, len(si.tools))
		m.monitorStderr(name, si)
	}
}

func (m *Manager) startServer(ctx context.Context, si *ServerInstance) error {
	env := make([]string, 0, len(si.config.Env))
	for k, v := range si.config.Env {
		env = append(env, k+"="+v)
	}

	c, err := mcpclient.NewStdioMCPClient(si.config.Command, env, si.config.Args...)
	if err != nil {
		return fmt.Errorf("spawning: %w", err)
	}
	si.client = c

	initReq := mcptypes.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcptypes.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcptypes.Implementation{
		Name:    "pane",
		Version: config.Version,
	}

	if _, err := c.Initialize(ctx, initReq); err != nil {
		c.Close()
		return fmt.Errorf("initializing: %w", err)
	}

	result, err := c.ListTools(ctx, mcptypes.ListToolsRequest{})
	if err != nil {
		c.Close()
		return fmt.Errorf("listing tools: %w", err)
	}

	si.tools = result.Tools
	return nil
}

func (m *Manager) monitorStderr(name string, si *ServerInstance) {
	stderr, ok := mcpclient.GetStderr(si.client)
	if !ok {
		return
	}
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			dl.Warnf("MCP server %s (stderr): %s", name, scanner.Text())
		}
		if err := scanner.Err(); err != nil && err != io.EOF {
			dl.Warnf("MCP server %s stderr reader: %v", name, err)
		}
	}()
}

func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, si := range m.servers {
		if si.client != nil {
			dl.Infof("stopping MCP server: %s", name)
			si.client.Close()
		}
	}
}

func (m *Manager) GetServer(name string) *ServerInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.servers[name]
}

func (m *Manager) GetAllTools() []ToolInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var tools []ToolInfo
	for serverName, si := range m.servers {
		if si.status != "running" {
			continue
		}
		for _, t := range si.tools {
			qualified := QualifyToolName(serverName, t.Name, m.separator)
			tools = append(tools, ToolInfo{
				Server:  serverName,
				Enabled: !si.disabled[t.Name],
				Function: ToolFunction{
					Name:        qualified,
					Description: t.Description,
					Parameters:  translateInputSchema(t.InputSchema),
				},
			})
		}
	}
	return tools
}

func (m *Manager) GetEnabledTools(disabled []string) []llm.Tool {
	disabledSet := make(map[string]bool, len(disabled))
	for _, d := range disabled {
		disabledSet[d] = true
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var tools []llm.Tool
	for serverName, si := range m.servers {
		if si.status != "running" {
			continue
		}
		for _, t := range si.tools {
			if si.disabled[t.Name] {
				continue
			}
			qualified := QualifyToolName(serverName, t.Name, m.separator)
			if disabledSet[qualified] {
				continue
			}
			info := ToolInfo{
				Server:  serverName,
				Enabled: true,
				Function: ToolFunction{
					Name:        qualified,
					Description: t.Description,
					Parameters:  translateInputSchema(t.InputSchema),
				},
			}
			tools = append(tools, TranslateToOpenAI(info))
		}
	}
	return tools
}

func (m *Manager) GetServerStatuses() map[string]ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make(map[string]ServerStatus, len(m.servers))
	for name, si := range m.servers {
		statuses[name] = ServerStatus{
			Status:     si.status,
			ToolsCount: len(si.tools),
			Error:      si.err,
		}
	}
	return statuses
}

func (m *Manager) ToggleTool(qualifiedName string, enabled bool) error {
	server, tool := ParseToolName(qualifiedName, m.separator)

	m.mu.Lock()
	defer m.mu.Unlock()

	si, ok := m.servers[server]
	if !ok {
		return fmt.Errorf("unknown server: %s", server)
	}

	if enabled {
		delete(si.disabled, tool)
	} else {
		si.disabled[tool] = true
	}
	return nil
}

func (m *Manager) CallTool(ctx context.Context, qualifiedName string, args map[string]any) (string, time.Duration, error) {
	server, tool := ParseToolName(qualifiedName, m.separator)

	m.mu.RLock()
	si, ok := m.servers[server]
	m.mu.RUnlock()

	if !ok {
		return "", 0, fmt.Errorf("unknown server: %s", server)
	}
	if si.status != "running" {
		return "", 0, fmt.Errorf("server %s is not running (status: %s)", server, si.status)
	}

	timeout := 30 * time.Second
	if si.config.Timeout != "" {
		if d, err := time.ParseDuration(si.config.Timeout); err == nil {
			timeout = d
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req := mcptypes.CallToolRequest{}
	req.Params.Name = tool
	req.Params.Arguments = args

	start := time.Now()
	result, err := si.client.CallTool(callCtx, req)
	duration := time.Since(start)

	if err != nil {
		return "", duration, fmt.Errorf("calling tool %s: %w", qualifiedName, err)
	}

	if result.IsError {
		text := extractText(result.Content)
		if text == "" {
			text = "tool returned an error"
		}
		return "", duration, fmt.Errorf("%s", text)
	}

	return extractText(result.Content), duration, nil
}

func (m *Manager) NeedsApproval(qualifiedName string) bool {
	server, _ := ParseToolName(qualifiedName, m.separator)

	m.mu.RLock()
	defer m.mu.RUnlock()

	si, ok := m.servers[server]
	if !ok {
		return false
	}
	return si.config.Approve
}

func extractText(content []mcptypes.Content) string {
	var parts []string
	for _, c := range content {
		if tc, ok := c.(mcptypes.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += "\n" + p
	}
	return result
}
