package mcp

import (
	"regexp"
	"strings"
	"testing"

	mcptypes "github.com/mark3labs/mcp-go/mcp"
	"github.com/michaelquigley/pane/internal/config"
)

var functionNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func TestCallableToolNameIsOpenAICompatible(t *testing.T) {
	t.Parallel()

	cases := []struct {
		server string
		tool   string
	}{
		{server: "filesystem", tool: "read_file"},
		{server: "work_space", tool: "read_file"},
		{server: "server::name", tool: "tool::name"},
		{server: "space name", tool: "open/path"},
		{server: "unicodé", tool: "читать"},
		{server: strings.Repeat("server", 20), tool: strings.Repeat("tool", 20)},
	}

	for _, tc := range cases {
		name := callableToolName(tc.server, tc.tool)
		if !functionNamePattern.MatchString(name) {
			t.Fatalf("expected OpenAI-compatible function name for %#v, got %q", tc, name)
		}
		if len(name) > maxFunctionNameLength {
			t.Fatalf("expected function name length <= %d, got %d", maxFunctionNameLength, len(name))
		}
		if !regexp.MustCompile(`_[0-9a-f]{10}$`).MatchString(name) {
			t.Fatalf("expected hash suffix in function name %q", name)
		}
	}
}

func TestCallableToolNameTruncatesLongNames(t *testing.T) {
	t.Parallel()

	name := callableToolName(strings.Repeat("server", 20), strings.Repeat("tool", 20))
	if len(name) != maxFunctionNameLength {
		t.Fatalf("expected long callable name to truncate to %d chars, got %d", maxFunctionNameLength, len(name))
	}
	if !functionNamePattern.MatchString(name) {
		t.Fatalf("expected OpenAI-compatible function name, got %q", name)
	}
}

func TestCallableToolNameDistinguishesSanitizedCollisions(t *testing.T) {
	t.Parallel()

	first := callableToolName("one two", "read/file")
	second := callableToolName("one/two", "read file")

	if first == second {
		t.Fatalf("expected sanitized collisions to produce distinct callable names, got %q", first)
	}
	if !strings.HasPrefix(first, "one_two_read_file_") {
		t.Fatalf("expected readable sanitized prefix, got %q", first)
	}
	if !strings.HasPrefix(second, "one_two_read_file_") {
		t.Fatalf("expected readable sanitized prefix, got %q", second)
	}
}

func TestManagerGetAllToolsReturnsOriginalAndCallableNames(t *testing.T) {
	t.Parallel()

	manager := newRouteTestManager()
	tools := manager.GetAllTools()
	if len(tools) != 5 {
		t.Fatalf("expected 5 tools, got %d", len(tools))
	}

	seen := make(map[string]string, len(tools))
	for _, info := range tools {
		key := info.Server + "\x00" + info.Name
		seen[key] = info.Function.Name

		if info.Name == "" {
			t.Fatalf("expected original tool name in %#v", info)
		}
		if !functionNamePattern.MatchString(info.Function.Name) {
			t.Fatalf("expected OpenAI-compatible callable name, got %q", info.Function.Name)
		}

		route, ok := manager.toolRoutes[info.Function.Name]
		if !ok {
			t.Fatalf("expected route for callable name %q", info.Function.Name)
		}
		if route.server != info.Server || route.tool != info.Name {
			t.Fatalf("expected route back to %q/%q, got %#v", info.Server, info.Name, route)
		}
	}

	assertSeenRoute(t, seen, "file_system", "read_file")
	assertSeenRoute(t, seen, "file_system", "open/path")
	assertSeenRoute(t, seen, "server::name", "tool::name")
	assertSeenRoute(t, seen, "space name", "tool name")
	assertSeenRoute(t, seen, "unicodé", "читать")
}

func TestManagerNeedsApprovalUsesCallableRouteMap(t *testing.T) {
	t.Parallel()

	manager := newRouteTestManager()
	tools := manager.GetAllTools()
	seen := make(map[string]string, len(tools))
	for _, info := range tools {
		seen[info.Server+"\x00"+info.Name] = info.Function.Name
	}

	approvedTool := seen["file_system\x00read_file"]
	if !manager.NeedsApproval(approvedTool) {
		t.Fatalf("expected callable tool %q to require approval", approvedTool)
	}

	unapprovedTool := seen["server::name\x00tool::name"]
	if manager.NeedsApproval(unapprovedTool) {
		t.Fatalf("expected callable tool %q to skip approval", unapprovedTool)
	}

	if manager.NeedsApproval("file_system_read_file") {
		t.Fatalf("expected legacy separator-style name to miss route map")
	}
}

func TestManagerIgnoresConfiguredSeparatorForCallableNames(t *testing.T) {
	t.Parallel()

	manager := NewManager(&config.MCPConfig{
		Separator: "::",
		Servers: map[string]*config.ServerConfig{
			"file_system": {Approve: true},
		},
	})
	manager.servers["file_system"].status = "running"
	manager.servers["file_system"].tools = []mcptypes.Tool{{Name: "read_file"}}

	tools := manager.GetAllTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if strings.Contains(tools[0].Function.Name, "::") {
		t.Fatalf("expected callable name to ignore separator, got %q", tools[0].Function.Name)
	}
	if !manager.NeedsApproval(tools[0].Function.Name) {
		t.Fatalf("expected callable name to route through approval map")
	}
}

func newRouteTestManager() *Manager {
	return &Manager{
		servers: map[string]*ServerInstance{
			"file_system": {
				name:   "file_system",
				config: &config.ServerConfig{Approve: true},
				status: "running",
				tools: []mcptypes.Tool{
					{Name: "read_file", Description: "read files"},
					{Name: "open/path", Description: "open paths"},
				},
			},
			"server::name": {
				name:   "server::name",
				config: &config.ServerConfig{},
				status: "running",
				tools: []mcptypes.Tool{
					{Name: "tool::name", Description: "namespaced tool"},
				},
			},
			"space name": {
				name:   "space name",
				config: &config.ServerConfig{},
				status: "running",
				tools: []mcptypes.Tool{
					{Name: "tool name", Description: "spaced tool"},
				},
			},
			"unicodé": {
				name:   "unicodé",
				config: &config.ServerConfig{},
				status: "running",
				tools: []mcptypes.Tool{
					{Name: "читать", Description: "unicode tool"},
				},
			},
		},
		toolRoutes: make(map[string]toolRoute),
	}
}

func assertSeenRoute(t *testing.T, seen map[string]string, server, tool string) {
	t.Helper()

	if seen[server+"\x00"+tool] == "" {
		t.Fatalf("expected route for %q/%q", server, tool)
	}
}
