package mcp

import (
	"encoding/json"
	"strings"

	mcptypes "github.com/mark3labs/mcp-go/mcp"
	"github.com/michaelquigley/pane/internal/llm"
)

type ToolInfo struct {
	Server   string       `json:"server"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ServerStatus struct {
	Status     string `json:"status"`
	ToolsCount int    `json:"tools_count"`
	Error      string `json:"error,omitempty"`
}

func QualifyToolName(server, tool, separator string) string {
	return server + separator + tool
}

func ParseToolName(qualified, separator string) (server, tool string) {
	idx := strings.Index(qualified, separator)
	if idx < 0 {
		return "", qualified
	}
	return qualified[:idx], qualified[idx+len(separator):]
}

func TranslateToOpenAI(info ToolInfo) llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: &llm.FunctionDef{
			Name:        info.Function.Name,
			Description: info.Function.Description,
			Parameters:  info.Function.Parameters,
		},
	}
}

func translateInputSchema(schema mcptypes.ToolInputSchema) json.RawMessage {
	data, err := json.Marshal(schema)
	if err != nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	return data
}
