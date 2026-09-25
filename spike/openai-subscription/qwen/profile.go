// Package qwen is the spike's chat-completions adapter with the proposed
// built-in compatibility profiles for the deployed qwen3.8-27b engines.
package qwen

import (
	"fmt"
	"strings"
)

// Sampling is an explicit sampling override set.
type Sampling struct {
	Temperature float64 `json:"temperature"`
	TopP        float64 `json:"top_p"`
	TopK        int     `json:"top_k"`
}

// Profile captures one engine's conventions. profiles are explicit and never
// inferred from alias, host, or model id; they carry no arbitrary overrides.
type Profile struct {
	Name string

	// Efforts are the accepted reasoning_effort values ('none' disables).
	Efforts []string

	// NonThinkingSampling is sent only when effort is 'none'. nil means the
	// engine selects its own non-thinking preset.
	NonThinkingSampling *Sampling

	// TemplateKwargs are sent on every request.
	TemplateKwargs map[string]any

	// ReplayActiveReasoning sends request-local reasoning_content on
	// assistant messages after the last real user message.
	ReplayActiveReasoning bool

	// ForcedFinalInInitialSystem folds the recovery instruction into the
	// leading system message instead of appending a system message.
	ForcedFinalInInitialSystem bool
}

// Profiles are the proposed built-ins. sources:
//   - ninfer 'a140e7ae' (eleven): top-level reasoning_effort none/low/medium/xhigh;
//     rejects unknown chat_template_kwargs keys (so no preserve kwarg is sent;
//     server default preserve_thinking=false already drops closed turns);
//     swaps to its non-thinking sampling preset itself.
//   - llama.cpp '0adcc3b' / tag 'b10502' (fortyfive): top-level effort, same
//     values ('none' prompt-verified); sampling pinned to thinking values, so
//     non-thinking needs explicit overrides; preserve_reasoning=false drops
//     closed-turn blocks; appended system messages raise HTTP 500.
var Profiles = map[string]Profile{
	"qwen3.8-ninfer": {
		Name:                       "qwen3.8-ninfer",
		Efforts:                    []string{"none", "low", "medium", "xhigh"},
		ReplayActiveReasoning:      true,
		ForcedFinalInInitialSystem: true,
	},
	"qwen3.8-llamacpp": {
		Name:                       "qwen3.8-llamacpp",
		Efforts:                    []string{"none", "low", "medium", "xhigh"},
		NonThinkingSampling:        &Sampling{Temperature: 0.7, TopP: 0.8, TopK: 20},
		TemplateKwargs:             map[string]any{"preserve_reasoning": false},
		ReplayActiveReasoning:      true,
		ForcedFinalInInitialSystem: true,
	},
}

// Resolve returns the named profile; empty name is the legacy generic
// connection, which accepts no effort setting.
func Resolve(name, effort string) (*Profile, error) {
	if name == "" {
		if effort != "" {
			return nil, fmt.Errorf("reasoning_effort requires a compatibility_profile")
		}
		return nil, nil
	}
	p, ok := Profiles[name]
	if !ok {
		return nil, fmt.Errorf("unknown compatibility_profile '%s'", name)
	}
	if effort != "" {
		found := false
		for _, e := range p.Efforts {
			if e == effort {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("reasoning_effort '%s' is not supported by profile '%s' (supported: %s)", effort, name, strings.Join(p.Efforts, ", "))
		}
	}
	return &p, nil
}
