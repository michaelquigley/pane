package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/michaelquigley/df/dd"
)

// Round validates the entire chat-completions terminal boundary before
// returning calls that the shared loop may execute.
func (c *Client) Round(ctx context.Context, request RoundRequest, emit func(RoundEvent)) (RoundFinal, error) {
	chatRequest := &ChatRequest{
		Model: request.Model, Messages: request.Messages,
		Tools: request.Tools, MaxTokens: request.MaxTokens,
	}
	stream, err := c.StreamChat(ctx, chatRequest)
	if err != nil {
		return RoundFinal{}, &RoundError{Kind: "upstream", Err: err}
	}
	defer stream.Close()

	var content strings.Builder
	calls := make(map[int]*chatCall)
	var finish string
	var latestUsage *Usage
	for {
		chunk, err := stream.Recv()
		if err != nil {
			if err != io.EOF {
				kind := "transport"
				if errors.Is(err, errStreamClosedBeforeDone) {
					kind = "truncated"
				}
				if errors.Is(err, errStreamBudgetExceeded) {
					kind = "budget"
				}
				if ctx.Err() != nil {
					kind = "cancelled"
				}
				return RoundFinal{}, &RoundError{Kind: kind, Err: err}
			}
			break
		}
		if len(chunk.Choices) > 1 {
			return RoundFinal{}, roundProtocol("multiple choices")
		}
		if len(chunk.Choices) == 1 {
			choice := chunk.Choices[0]
			if choice.Index != 0 {
				return RoundFinal{}, roundProtocol("unexpected choice index")
			}
			if choice.Delta.DeprecatedFunctionCall || choice.DeprecatedFunctionCall {
				return RoundFinal{}, roundProtocol("deprecated function_call")
			}
			reasoning := choice.Delta.Reasoning
			if reasoning == nil {
				reasoning = choice.Delta.ReasoningContent
			}
			if finish != "" && (nonempty(choice.Delta.Content) || nonempty(reasoning) || len(choice.Delta.ToolCalls) > 0) {
				return RoundFinal{}, roundProtocol("delta after finish")
			}
			if nonempty(choice.Delta.Content) {
				content.WriteString(*choice.Delta.Content)
				emit(RoundEvent{Kind: "delta", Content: *choice.Delta.Content})
			}
			if nonempty(reasoning) {
				emit(RoundEvent{Kind: "thinking_delta", Content: *reasoning})
			}
			for _, delta := range choice.Delta.ToolCalls {
				if delta.Index == nil || *delta.Index < 0 {
					return RoundFinal{}, roundProtocol("missing tool call index")
				}
				index := *delta.Index
				call := calls[index]
				newCall := call == nil
				if call == nil {
					call = &chatCall{RoundCall: RoundCall{Index: index, ID: nextToolCallID(request.Iteration, index)}}
				}
				if delta.ID != "" {
					if call.upstreamID != "" && call.upstreamID != delta.ID {
						return RoundFinal{}, roundProtocol("conflicting tool call id")
					}
					call.upstreamID = delta.ID
				}
				if delta.Type != "" {
					if delta.Type != "function" || (call.callType != "" && call.callType != delta.Type) {
						return RoundFinal{}, roundProtocol("invalid tool call type")
					}
					call.callType = delta.Type
				}
				nameArrived := call.Name == "" && delta.Function.Name != ""
				if delta.Function.Name != "" {
					if call.Name != "" && call.Name != delta.Function.Name {
						return RoundFinal{}, roundProtocol("conflicting tool call name")
					}
					call.Name = delta.Function.Name
				}
				if newCall {
					calls[index] = call
				}
				if newCall || nameArrived {
					emit(RoundEvent{Kind: "tool_call_start", Call: call.RoundCall})
				}
				if delta.Function.Arguments != "" {
					call.Arguments += delta.Function.Arguments
					emit(RoundEvent{Kind: "tool_call_args", Call: RoundCall{Index: index, ID: call.ID, Arguments: delta.Function.Arguments}})
				}
			}
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				if finish != "" {
					return RoundFinal{}, roundProtocol("conflicting finish reasons")
				}
				finish = *choice.FinishReason
			}
		}
		if chunk.Usage != nil {
			latestUsage = chunk.Usage
		}
	}
	if ctx.Err() != nil {
		return RoundFinal{}, &RoundError{Kind: "cancelled", Err: ctx.Err()}
	}
	if finish == "length" || finish == "content_filter" {
		return RoundFinal{}, &RoundError{Kind: "incomplete", Reason: finish}
	}
	if finish != "stop" && finish != "tool_calls" {
		return RoundFinal{}, roundProtocol(fmt.Sprintf("invalid finish reason '%s'", finish))
	}
	if finish == "stop" && len(calls) != 0 || finish == "tool_calls" && len(calls) == 0 {
		return RoundFinal{}, roundProtocol("finish reason does not match tool calls")
	}
	if request.Intent == IntentFinal && len(calls) > 0 {
		return RoundFinal{}, &RoundError{Kind: "repeated_tool_failure", Reason: "model returned tool calls after repeated tool failures"}
	}
	final := RoundFinal{Finish: finish, Content: content.String()}
	for _, call := range calls {
		if call.Name == "" || call.callType != "function" || !objectJSON(call.Arguments) {
			return RoundFinal{}, roundProtocol("incomplete or malformed tool call")
		}
		final.Calls = append(final.Calls, call.RoundCall)
	}
	slices.SortFunc(final.Calls, func(a, b RoundCall) int { return a.Index - b.Index })
	if latestUsage != nil {
		emit(RoundEvent{Kind: "usage", Usage: latestUsage})
	}
	return final, nil
}

type chatCall struct {
	RoundCall
	upstreamID string
	callType   string
}

func nonempty(value *string) bool { return value != nil && *value != "" }

func objectJSON(value string) bool {
	if value == "" {
		return false
	}
	_, err := dd.DecodeStrictJSON([]byte(value))
	return err == nil
}

func roundProtocol(reason string) error { return &RoundError{Kind: "protocol", Reason: reason} }
