package mcp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcptypes "github.com/mark3labs/mcp-go/mcp"
	"github.com/michaelquigley/df/dl"
	"github.com/michaelquigley/pane/internal/config"
	"github.com/michaelquigley/pane/internal/llm"
	"github.com/michaelquigley/push/build"
)

type Manager struct {
	servers    map[string]*ServerInstance
	toolRoutes map[string]toolRoute
	mu         sync.RWMutex
}

type ServerInstance struct {
	name   string
	config *config.ServerConfig
	client *mcpclient.Client
	tools  []mcptypes.Tool
	status string
	err    string
}

type toolRoute struct {
	server string
	tool   string
}

func NewManager(cfg *config.MCPConfig) *Manager {
	m := &Manager{
		servers:    make(map[string]*ServerInstance),
		toolRoutes: make(map[string]toolRoute),
	}
	if cfg == nil {
		return m
	}
	for name, sc := range cfg.Servers {
		m.servers[name] = &ServerInstance{
			name:   name,
			config: sc,
			status: "starting",
		}
	}
	return m
}

func (m *Manager) Start(ctx context.Context) {
	for name, si := range m.servers {
		dl.Infof("starting MCP server: %s", name)
		if err := m.startServer(ctx, si); err != nil {
			m.mu.Lock()
			si.status = "error"
			si.err = err.Error()
			m.rebuildToolRoutesLocked()
			m.mu.Unlock()
			dl.Warnf("MCP server %s failed: %v", name, err)
			continue
		}
		m.mu.Lock()
		si.status = "running"
		si.err = ""
		m.rebuildToolRoutesLocked()
		m.mu.Unlock()
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

	version := build.Version
	if version == "" {
		version = build.DevVersion
	}

	initReq := mcptypes.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcptypes.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcptypes.Implementation{
		Name:    "pane",
		Version: version,
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
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.rebuildToolRoutesLocked()
}

func (m *Manager) rebuildToolRoutesLocked() []ToolInfo {
	m.toolRoutes = make(map[string]toolRoute)

	var tools []ToolInfo
	for serverName, si := range m.servers {
		if si.status != "running" {
			continue
		}
		for _, t := range si.tools {
			callableName := callableToolName(serverName, t.Name)
			m.toolRoutes[callableName] = toolRoute{
				server: serverName,
				tool:   t.Name,
			}
			tools = append(tools, ToolInfo{
				Server: serverName,
				Name:   t.Name,
				Function: ToolFunction{
					Name:        callableName,
					Description: t.Description,
					Parameters:  translateInputSchema(t.InputSchema),
				},
			})
		}
	}
	return tools
}

func (m *Manager) GetEnabledTools() []llm.Tool {
	tools := m.GetAllTools()
	enabled := make([]llm.Tool, 0, len(tools))
	for _, t := range tools {
		enabled = append(enabled, TranslateToOpenAI(t))
	}
	return enabled
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

func (m *Manager) CallTool(ctx context.Context, callableName string, args map[string]any) (string, time.Duration, error) {
	si, route, ok := m.resolveToolRoute(callableName)
	if !ok {
		return "", 0, fmt.Errorf("unknown tool: '%s'", callableName)
	}
	if si.status != "running" {
		return "", 0, fmt.Errorf("server '%s' is not running (status: '%s')", route.server, si.status)
	}

	timeout := 30 * time.Second
	if si.config != nil && si.config.Timeout != "" {
		if d, err := time.ParseDuration(si.config.Timeout); err == nil {
			timeout = d
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req := mcptypes.CallToolRequest{}
	req.Params.Name = route.tool
	req.Params.Arguments = args

	start := time.Now()
	result, err := si.client.CallTool(callCtx, req)
	duration := time.Since(start)

	if err != nil {
		return "", duration, fmt.Errorf("calling tool '%s': %w", callableName, err)
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

func (m *Manager) NeedsApproval(callableName string) bool {
	si, _, ok := m.resolveToolRoute(callableName)
	if !ok {
		return false
	}
	if si.config == nil {
		return false
	}
	return si.config.Approve
}

func (m *Manager) resolveToolRoute(callableName string) (*ServerInstance, toolRoute, bool) {
	m.mu.RLock()
	route, ok := m.toolRoutes[callableName]
	if ok {
		si, serverOK := m.servers[route.server]
		m.mu.RUnlock()
		return si, route, serverOK
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.rebuildToolRoutesLocked()
	route, ok = m.toolRoutes[callableName]
	if !ok {
		return nil, toolRoute{}, false
	}
	si, ok := m.servers[route.server]
	return si, route, ok
}

func extractText(content []mcptypes.Content) string {
	var parts []string
	for _, c := range content {
		if tc, ok := c.(mcptypes.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}
