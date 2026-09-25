package config

import "fmt"

const (
	ProviderChatCompletions = "openai-chat-completions"
	ProviderCodex           = "openai-codex"
	ProfileNinfer           = "qwen3.8-ninfer"
	ProfileLlamaCPP         = "qwen3.8-llamacpp"
)

// codex efforts are the built-in first-version capability table shared with the subscription adapter.
var codexEfforts = map[string]map[string]bool{
	"gpt-5.6-sol": {"none": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true},
	"gpt-6-astra": {"low": true, "medium": true, "high": true, "xhigh": true, "max": true},
}

func SupportsCodexEffort(upstreamModel, effort string) (supported, knownModel bool) {
	efforts, knownModel := codexEfforts[upstreamModel]
	return efforts[effort], knownModel
}

var qwenEfforts = map[string]bool{"none": true, "low": true, "medium": true, "xhigh": true}

func validateModel(alias string, model *ModelConfig, resolved ResolvedModel) error {
	switch resolved.Provider {
	case ProviderChatCompletions:
		switch model.CompatibilityProfile {
		case "":
			if model.ReasoningEffort != nil {
				return fmt.Errorf("model '%s': reasoning_effort requires a supported compatibility_profile", alias)
			}
		case ProfileNinfer, ProfileLlamaCPP:
			if model.ReasoningEffort != nil && !qwenEfforts[*model.ReasoningEffort] {
				return fmt.Errorf("model '%s': unsupported reasoning_effort '%s' for profile '%s'", alias, *model.ReasoningEffort, model.CompatibilityProfile)
			}
		default:
			return fmt.Errorf("model '%s': unknown compatibility_profile '%s'", alias, model.CompatibilityProfile)
		}
	case ProviderCodex:
		if model.Endpoint != nil || model.ApiKey != nil {
			return fmt.Errorf("model '%s': endpoint and api_key must be omitted for provider '%s'", alias, ProviderCodex)
		}
		if model.CompatibilityProfile != "" {
			return fmt.Errorf("model '%s': compatibility_profile is not supported for provider '%s'", alias, ProviderCodex)
		}
		if model.MaxTokens != nil {
			return fmt.Errorf("model '%s': max_tokens is not supported for provider '%s'", alias, ProviderCodex)
		}
		if model.ReasoningEffort != nil {
			accepted, ok := codexEfforts[resolved.UpstreamModel]
			if !ok {
				return fmt.Errorf("model '%s': no reasoning_effort capability is defined for upstream model '%s'", alias, resolved.UpstreamModel)
			}
			if !accepted[*model.ReasoningEffort] {
				return fmt.Errorf("model '%s': unsupported reasoning_effort '%s' for upstream model '%s'", alias, *model.ReasoningEffort, resolved.UpstreamModel)
			}
		}
	default:
		return fmt.Errorf("model '%s': unknown provider '%s'", alias, resolved.Provider)
	}
	return nil
}
