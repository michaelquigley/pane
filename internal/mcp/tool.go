package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	mcptypes "github.com/mark3labs/mcp-go/mcp"
	"github.com/michaelquigley/pane/internal/llm"
)

const (
	maxFunctionNameLength      = 64
	callableToolNameHashLength = 10
)

type ToolInfo struct {
	Server   string       `json:"server"`
	Name     string       `json:"name"`
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

func callableToolName(server, tool string) string {
	identity := server + "\x00" + tool
	sum := sha256.Sum256([]byte(identity))
	suffix := hex.EncodeToString(sum[:])[:callableToolNameHashLength]

	base := sanitizeFunctionNameBase(server + "_" + tool)
	maxBaseLen := maxFunctionNameLength - callableToolNameHashLength - 1
	if len(base) > maxBaseLen {
		base = strings.Trim(base[:maxBaseLen], "_")
		if base == "" {
			base = "tool"
		}
	}

	return base + "_" + suffix
}

func sanitizeFunctionNameBase(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))

	hasNameChar := false
	lastUnderscore := false
	for _, r := range raw {
		switch {
		case isFunctionNameLetter(r) || isFunctionNameDigit(r):
			b.WriteRune(r)
			hasNameChar = true
			lastUnderscore = false
		case r == '-':
			b.WriteRune(r)
			lastUnderscore = false
		case r == '_':
			if !lastUnderscore {
				b.WriteByte('_')
			}
			lastUnderscore = true
		default:
			if !lastUnderscore {
				b.WriteByte('_')
			}
			lastUnderscore = true
		}
	}

	base := strings.Trim(b.String(), "_")
	if base == "" || !hasNameChar {
		return "tool"
	}
	return base
}

func isFunctionNameLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isFunctionNameDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func translateInputSchema(schema mcptypes.ToolInputSchema) json.RawMessage {
	data, err := json.Marshal(schema)
	if err != nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	return data
}
