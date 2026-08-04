package oaichat

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatCompletionsResponseToResponsesPreservesTextToolCallsAndUsage(t *testing.T) {
	chat := &dto.OpenAITextResponse{
		Id:      "chatcmpl_1",
		Model:   "gpt-test",
		Created: 456,
		Choices: []dto.OpenAITextResponseChoice{
			{
				Message:      assistantMessageWithTool("I will call.", "call_1", "lookup", `{"q":"x"}`),
				FinishReason: "tool_calls",
			},
		},
		Usage: dto.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8},
	}

	resp, usage, err := ChatCompletionsResponseToResponsesResponse(chat, "resp_1")
	require.NoError(t, err)
	require.NotNil(t, usage)

	assert.Equal(t, "resp_1", resp.ID)
	assert.Equal(t, "response", resp.Object)
	assert.Equal(t, `"completed"`, string(resp.Status))
	assert.Equal(t, 3, resp.Usage.InputTokens)
	assert.Equal(t, 5, resp.Usage.OutputTokens)
	require.Len(t, resp.Output, 2)
	assert.Equal(t, responsesOutputTypeMessage, resp.Output[0].Type)
	assert.Equal(t, "I will call.", resp.Output[0].Content[0].Text)
	assert.Equal(t, responsesOutputTypeFunctionCall, resp.Output[1].Type)
	assert.Equal(t, "call_1", resp.Output[1].CallId)
	assert.Equal(t, "lookup", resp.Output[1].Name)
	assert.Equal(t, `"{\"q\":\"x\"}"`, string(resp.Output[1].Arguments))
}

func TestChatCompletionsResponseToResponsesMapsIncompleteFinishReasons(t *testing.T) {
	tests := []struct {
		name         string
		finishReason string
		wantReason   string
	}{
		{name: "length", finishReason: "length", wantReason: responsesIncompleteReasonMaxTokens},
		{name: "content filter", finishReason: "content_filter", wantReason: responsesIncompleteReasonContentFilter},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, _, err := ChatCompletionsResponseToResponsesResponse(&dto.OpenAITextResponse{
				Id:    "chatcmpl_1",
				Model: "gpt-test",
				Choices: []dto.OpenAITextResponseChoice{
					{
						Message:      dto.Message{Role: "assistant", Content: "partial"},
						FinishReason: tt.finishReason,
					},
				},
			}, "resp_1")
			require.NoError(t, err)

			assert.Equal(t, `"incomplete"`, string(resp.Status))
			require.NotNil(t, resp.IncompleteDetails)
			assert.Equal(t, tt.wantReason, resp.IncompleteDetails.Reason)
			require.Len(t, resp.Output, 1)
			assert.Equal(t, "incomplete", resp.Output[0].Status)
		})
	}
}

func TestChatCompletionsStreamToResponsesEventsAggregatesUsageAndToolArgs(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_1", "gpt-test")
	state.Created = 123
	toolIndex := 0

	var events []ChatToResponsesStreamEvent
	events = append(events, mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{
		Id:      "chatcmpl_1",
		Model:   "gpt-test",
		Created: 123,
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Role: "assistant"}},
		},
	})...)
	events = append(events, mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: lo.ToPtr("hello")}},
		},
	})...)
	events = append(events, mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{
				{Index: &toolIndex, ID: "call_1", Type: "function", Function: dto.FunctionResponse{Name: "lookup"}},
			}}},
		},
	})...)
	events = append(events, mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{
				{Index: &toolIndex, Function: dto.FunctionResponse{Arguments: `{"q":"x"}`}},
			}}},
		},
	})...)
	finishReason := "tool_calls"
	events = append(events, mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Index: 0, FinishReason: &finishReason},
		},
	})...)
	events = append(events, mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{
		Usage: &dto.Usage{PromptTokens: 2, CompletionTokens: 4, TotalTokens: 6},
	})...)
	events = append(events, FinalizeChatCompletionsStreamToResponses(state)...)

	require.Len(t, events, 10)
	assert.Equal(t, responsesEventCreated, events[0].Type)
	assert.Equal(t, responsesEventOutputTextDelta, events[2].Type)
	assert.Equal(t, "hello", events[2].Payload.Delta)
	assert.Equal(t, responsesEventFunctionArgsDelta, events[4].Type)
	assert.Equal(t, `{"q":"x"}`, events[4].Payload.Delta)
	assert.Equal(t, responsesEventCompleted, events[9].Type)
	require.NotNil(t, events[9].Payload.Response)
	assert.Equal(t, 6, events[9].Payload.Response.Usage.TotalTokens)
	require.Len(t, events[9].Payload.Response.Output, 2)
	assert.Equal(t, "hello", events[9].Payload.Response.Output[0].Content[0].Text)
	assert.Equal(t, `"{\"q\":\"x\"}"`, string(events[9].Payload.Response.Output[1].Arguments))
}

func mustResponsesEventsFromChatChunk(t *testing.T, state *ChatToResponsesStreamState, chunk *dto.ChatCompletionsStreamResponse) []ChatToResponsesStreamEvent {
	t.Helper()
	events, err := ChatCompletionsStreamChunkToResponsesEvents(chunk, state)
	require.NoError(t, err)
	return events
}

// Regression for #3/#4: chat function calls restore native Responses tool
// kinds (custom_tool_call / tool_search_call / namespaced function_call) when
// the bridge recorded them on the request side.
func TestChatCompletionsResponseToResponsesRestoresCodexToolKinds(t *testing.T) {
	bridge := &convmeta.ResponsesChatBridgeContext{}
	bridge.Register("apply_patch", convmeta.ResponsesChatToolSpec{
		Kind: convmeta.ResponsesChatToolCustom,
		Name: "apply_patch",
	})
	longChatName := "mcp__very_long_namespace__read_file__b1e2c3d4e5f6"
	bridge.Register(longChatName, convmeta.ResponsesChatToolSpec{
		Kind:      convmeta.ResponsesChatToolNamespace,
		Name:      "read_file_with_a_deliberately_long_name",
		Namespace: "mcp__very_long_namespace",
	})
	bridge.Register("tool_search", convmeta.ResponsesChatToolSpec{
		Kind: convmeta.ResponsesChatToolSearch,
		Name: "tool_search",
	})

	message := dto.Message{Role: "assistant"}
	message.SetToolCalls([]dto.ToolCallRequest{
		{
			ID:   "call_patch",
			Type: "function",
			Function: dto.FunctionRequest{
				Name:      "apply_patch",
				Arguments: `{"input":"*** Begin Patch\n*** End Patch"}`,
			},
		},
		{
			ID:   "call_read",
			Type: "function",
			Function: dto.FunctionRequest{
				Name:      longChatName,
				Arguments: `{"path":"/tmp/a"}`,
			},
		},
		{
			ID:   "call_search",
			Type: "function",
			Function: dto.FunctionRequest{
				Name:      "tool_search",
				Arguments: `{"query":"gmail","limit":3}`,
			},
		},
	})
	resp, _, err := ChatCompletionsResponseToResponsesResponseWithContext(&dto.OpenAITextResponse{
		Id:      "chatcmpl_codex",
		Model:   "deepseek-v4-flash",
		Choices: []dto.OpenAITextResponseChoice{{Message: message, FinishReason: "tool_calls"}},
	}, "resp_codex", bridge)
	require.NoError(t, err)
	require.Len(t, resp.Output, 3)

	assert.Equal(t, "custom_tool_call", resp.Output[0].Type)
	assert.Equal(t, "ctc_call_patch", resp.Output[0].ID)
	assert.Equal(t, "call_patch", resp.Output[0].CallId)
	assert.Equal(t, "apply_patch", resp.Output[0].Name)
	require.NotNil(t, resp.Output[0].Input)
	assert.Equal(t, "*** Begin Patch\n*** End Patch", *resp.Output[0].Input)

	assert.Equal(t, "function_call", resp.Output[1].Type)
	assert.Equal(t, "read_file_with_a_deliberately_long_name", resp.Output[1].Name)
	assert.Equal(t, "mcp__very_long_namespace", resp.Output[1].Namespace)
	assert.Equal(t, `"{\"path\":\"/tmp/a\"}"`, string(resp.Output[1].Arguments))

	assert.Equal(t, "tool_search_call", resp.Output[2].Type)
	assert.Equal(t, "client", resp.Output[2].Execution)
	assert.JSONEq(t, `{"query":"gmail","limit":3}`, string(resp.Output[2].Arguments))
}

// Regression for #2: prompt_cache_hit_tokens must be carried into
// input_tokens_details.cached_tokens so Responses clients see cache accounting.
func TestUsageFromChatUsageMapsPromptCacheHitTokens(t *testing.T) {
	usage := UsageFromChatUsage(&dto.Usage{
		PromptTokens:        100,
		CompletionTokens:     20,
		PromptCacheHitTokens: 80,
	})
	require.NotNil(t, usage.InputTokensDetails)
	assert.Equal(t, 80, usage.InputTokensDetails.CachedTokens)
	assert.Equal(t, 100, usage.PromptTokens)
	assert.Equal(t, 20, usage.CompletionTokens)

	// Canonical chat cached_tokens still wins over the legacy field.
	usage2 := UsageFromChatUsage(&dto.Usage{
		PromptTokens:        100,
		PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 50},
		PromptCacheHitTokens: 80,
	})
	require.NotNil(t, usage2.InputTokensDetails)
	assert.Equal(t, 50, usage2.InputTokensDetails.CachedTokens)
}

// Regression for #4/#5 streaming: custom tool calls emit
// custom_tool_call_input delta+done (not function_call_arguments), and the done
// item restores the input value.
func TestChatCompletionsStreamToResponsesRestoresCustomToolEvents(t *testing.T) {
	bridge := &convmeta.ResponsesChatBridgeContext{}
	bridge.Register("apply_patch", convmeta.ResponsesChatToolSpec{
		Kind: convmeta.ResponsesChatToolCustom,
		Name: "apply_patch",
	})
	state := NewChatToResponsesStreamState("resp_custom", "deepseek-v4-flash")
	state.SetResponsesChatBridge(bridge)
	toolIndex := 0

	events := mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Index: 0,
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{
				Index: &toolIndex,
				ID:    "call_patch",
				Type:  "function",
				Function: dto.FunctionResponse{
					Name:      "apply_patch",
					Arguments: `{"input":"*** Begin Patch\n*** End Patch"}`,
				},
			}}},
		}},
	})
	finishReason := "tool_calls"
	events = append(events, mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{{Index: 0, FinishReason: &finishReason}},
	})...)
	events = append(events, FinalizeChatCompletionsStreamToResponses(state)...)

	require.Len(t, events, 6)
	assert.Equal(t, responsesEventCreated, events[0].Type)
	assert.Equal(t, responsesEventOutputItemAdded, events[1].Type)
	require.NotNil(t, events[1].Payload.Item)
	assert.Equal(t, responsesOutputTypeCustomToolCall, events[1].Payload.Item.Type)
	assert.Equal(t, "ctc_call_patch", events[1].Payload.Item.ID)
	assert.Equal(t, responsesEventCustomToolInputDelta, events[2].Type)
	assert.Equal(t, "*** Begin Patch\n*** End Patch", events[2].Payload.Delta)
	assert.Equal(t, "ctc_call_patch", events[2].Payload.ItemID)
	assert.Equal(t, responsesEventCustomToolInputDone, events[3].Type)
	require.NotNil(t, events[3].Payload.Input)
	assert.Equal(t, "*** Begin Patch\n*** End Patch", *events[3].Payload.Input)
	assert.Equal(t, responsesEventOutputItemDone, events[4].Type)
	require.NotNil(t, events[4].Payload.Item)
	require.NotNil(t, events[4].Payload.Item.Input)
	assert.Equal(t, "*** Begin Patch\n*** End Patch", *events[4].Payload.Item.Input)
	assert.Equal(t, responsesEventCompleted, events[5].Type)
	require.Len(t, events[5].Payload.Response.Output, 1)
	assert.Equal(t, responsesOutputTypeCustomToolCall, events[5].Payload.Response.Output[0].Type)
}
