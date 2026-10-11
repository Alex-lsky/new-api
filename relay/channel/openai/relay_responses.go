package openai

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	// read response body
	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	err = common.Unmarshal(responseBody, &responsesResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	// Textualized tool-call repair (channel repair_text_tool_call_models):
	// rewrite assistant messages that consist solely of "[tool call] name({json})"
	// into structured function_call items, before the bridge restore so a
	// repaired bridged tool is also restored to its native kind.
	if info.ResponsesRepairToolNames != nil {
		if repaired := repairTextualToolCallsBody(responseBody, info.ResponsesRepairToolNames, c); !bytes.Equal(repaired, responseBody) {
			responseBody = repaired
			// refresh the DTO so tool-call billing sees the repaired items
			_ = common.Unmarshal(responseBody, &responsesResponse)
		}
	}

	// Function-only upstreams (bridge_tool_types) spoke function tools; restore
	// the native custom/namespace/tool_search call kinds the client declared.
	if info.ClientToolBridge != nil {
		responseBody = restoreBridgedResponsesOutput(responseBody, info.ClientToolBridge)
		// refresh the DTO so tool-call billing sees restored original names
		_ = common.Unmarshal(responseBody, &responsesResponse)
	}

	// OpenCode zen/go strips the reasoning item from DeepSeek thinking-mode
	// responses. Codex then cannot pass reasoning_content back on the next
	// turn, which DeepSeek requires in thinking mode. Inject an empty
	// reasoning item only when the upstream is OpenCode and the response
	// genuinely lacks one; any other upstream or a response that already
	// carries reasoning is forwarded byte-for-byte untouched.
	if isOpencodeUpstream(info) && !hasReasoningOutput(responsesResponse.Output) {
		responseBody = injectEmptyReasoningBody(responseBody, responsesResponse)
	}
	info.ObserveResponseModel(responsesResponse.Model)
	responseBody = rewriteSGLangResponsesCreatedAt(info, responseBody, "created_at", responsesResponse.CreatedAt)

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	// compute usage
	usage := &dto.Usage{}
	service.ApplyResponsesUsage(usage, responsesResponse.Usage)
	// Count actual tool invocations from Output (not tool declarations).
	for _, output := range responsesResponse.Output {
		switch output.Type {
		case dto.BuildInCallWebSearchCall:
			info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
		case dto.BuildInCallFileSearchCall:
			info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
		case dto.BuildInCallFunctionCall:
			info.CountBillableToolCall(dto.BuildInCallFunctionCall, output.Name)
		}
	}
	info.ApplyVendorToolUsage(responseBody)

	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	for i := range responsesResponse.Output {
		imageCounter.Observe(&responsesResponse.Output[i], &i)
	}
	imageCounter.Commit(info)

	return usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, common.LogText("invalid response or response body"))
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)

	accumulator := service.NewResponsesUsageAccumulator(info)
	var responseTextBuilder strings.Builder

	// Event backfill state. Some upstreams (e.g. OpenCode zen/go forwarding
	// DeepSeek) emit only output_text.delta + completed, omitting the
	// output_item.added / content_part.added / *.done events codex relies on.
	// When the gap is detected, synthesize the standard event sequence before
	// forwarding; native full-event streams are unaffected.
	var evtItemAdded bool
	var evtItemDone bool
	var evtOutputIndex int
	var evtMsgID string
	// OpenCode zen/go strips reasoning events from DeepSeek thinking-mode
	// streams. Track whether the upstream actually emitted any reasoning event
	// so we only inject when it is genuinely missing; full-event streams pass
	// through untouched.
	var evtReasoningSeen bool
	var evtReasoningInjected bool
	var evtReasoningID string

	emitSynthetic := func(payload map[string]any) {
		if b, err := common.Marshal(payload); err == nil {
			_ = helper.ResponseChunkData(c, dto.ResponsesStreamResponse{}, string(b))
		}
	}

	// Restore bridged client tool kinds (custom/namespace/tool_search) on the
	// fly when the request bridged them for this function-only upstream.
	var streamBridge *responsesStreamToolBridge
	if info.ClientToolBridge != nil {
		streamBridge = newResponsesStreamToolBridge(info.ClientToolBridge)
	}

	// Textualized tool-call repair (channel repair_text_tool_call_models):
	// message events are held back until the leading text disproves the
	// "[tool call]" signature; confirmed textual calls are replaced by a
	// synthetic function_call sequence. Held/synthetic events re-enter this
	// same pipeline so backfill bookkeeping and the bridge restore apply to
	// them like to native events.
	var textRepair *responsesStreamTextRepair
	if info.ResponsesRepairToolNames != nil {
		textRepair = newResponsesStreamTextRepair(info.ResponsesRepairToolNames)
	}

	var processEvent func(data string, sr *helper.StreamResult)
	processEvent = func(data string, sr *helper.StreamResult) {
		if textRepair != nil {
			transformed, preEvents, forward := textRepair.processEvent([]byte(data), c)
			for _, pre := range preEvents {
				processEvent(string(pre), sr)
			}
			if !forward {
				return
			}
			data = string(transformed)
		}

		// 检查当前数据是否包含 completed 状态和 usage 信息
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			logger.LogError(c, common.LogText("failed to unmarshal stream response: %s", err.Error()))
			sr.Error(err)
			return
		}
		// Backfill missing lifecycle events before forwarding the real chunk so
		// the client observes a well-formed event sequence. Native full-event
		// streams (OpenAI et al.) announce output_item.added/content_part.added
		// themselves; we track those so we never duplicate them.
		switch streamResponse.Type {
		case "response.output_item.added":
			evtItemAdded = true
			if streamResponse.Item != nil && streamResponse.Item.ID != "" {
				evtMsgID = streamResponse.Item.ID
			}
			if streamResponse.OutputIndex != nil {
				evtOutputIndex = *streamResponse.OutputIndex
			}
			// A reasoning output item counts as upstream-provided reasoning.
			if streamResponse.Item != nil && streamResponse.Item.Type == "reasoning" {
				evtReasoningSeen = true
			}
		case "response.output_item.done":
			evtItemDone = true
		case "response.reasoning_summary_text.delta",
			"response.reasoning_summary_text.done",
			"response.reasoning_summary_part.added",
			"response.reasoning_summary_part.done",
			"response.reasoning_text.delta",
			"response.reasoning_text.done":
			evtReasoningSeen = true
		case "response.output_text.delta":
			if !evtItemAdded {
				if evtMsgID == "" {
					evtMsgID = "msg_" + common.GetUUID()
				}
				emitSynthetic(map[string]any{
					"type":         "response.output_item.added",
					"output_index": evtOutputIndex,
					"item": map[string]any{
						"id":      evtMsgID,
						"type":    "message",
						"status":  "in_progress",
						"role":    "assistant",
						"content": []any{},
					},
				})
				emitSynthetic(map[string]any{
					"type":          "response.content_part.added",
					"item_id":       evtMsgID,
					"output_index":  evtOutputIndex,
					"content_index": 0,
					"part": map[string]any{
						"type":        "output_text",
						"text":        "",
						"annotations": []any{},
					},
				})
				evtItemAdded = true
			}
		case "response.completed", "response.done":
			// OpenCode zen/go never emits reasoning events; DeepSeek thinking
			// mode requires codex to pass reasoning_content back on the next
			// turn. Inject an empty reasoning item sequence before the message
			// completion events (reasoning keeps output_index 0 and the message
			// shifts to 1) only when reasoning was genuinely absent. Any stream
			// that already carried reasoning is left untouched.
			if isOpencodeUpstream(info) && !evtReasoningSeen && !evtReasoningInjected && evtItemAdded && !evtItemDone {
				evtReasoningID = "reasoning_" + common.GetUUID()
				emitSynthetic(map[string]any{
					"type":         "response.output_item.added",
					"output_index": 0,
					"item": map[string]any{
						"id":      evtReasoningID,
						"type":    "reasoning",
						"status":  "in_progress",
						"content": []any{},
					},
				})
				emitSynthetic(map[string]any{
					"type":          "response.reasoning_summary_text.delta",
					"output_index":  0,
					"summary_index": 0,
					"delta":         "",
					"item_id":       evtReasoningID,
				})
				emitSynthetic(map[string]any{
					"type":          "response.reasoning_summary_text.done",
					"output_index":  0,
					"summary_index": 0,
					"item_id":       evtReasoningID,
					"part": map[string]any{
						"type": "summary_text",
						"text": "",
					},
				})
				emitSynthetic(map[string]any{
					"type":         "response.output_item.done",
					"output_index": 0,
					"item": map[string]any{
						"id":     evtReasoningID,
						"type":   "reasoning",
						"status": "completed",
						"content": []any{map[string]any{
							"type":        "summary_text",
							"text":        "",
							"annotations": []any{},
						}},
					},
				})
				evtReasoningInjected = true
				// reasoning owns output_index 0; the message events below shift to 1.
				evtOutputIndex = 1
			}
			if !evtItemDone && evtItemAdded {
				text := responseTextBuilder.String()
				emitSynthetic(map[string]any{
					"type":          "response.output_text.done",
					"item_id":       evtMsgID,
					"output_index":  evtOutputIndex,
					"content_index": 0,
					"text":          text,
					"annotations":   []any{},
				})
				emitSynthetic(map[string]any{
					"type":          "response.content_part.done",
					"item_id":       evtMsgID,
					"output_index":  evtOutputIndex,
					"content_index": 0,
					"part": map[string]any{
						"type":        "output_text",
						"text":        text,
						"annotations": []any{},
					},
				})
				emitSynthetic(map[string]any{
					"type":         "response.output_item.done",
					"output_index": evtOutputIndex,
					"item": map[string]any{
						"id":     evtMsgID,
						"type":   "message",
						"status": "completed",
						"role":   "assistant",
						"content": []any{map[string]any{
							"type":        "output_text",
							"text":        text,
							"annotations": []any{},
						}},
					},
				})
				evtItemDone = true
			}
		}
		outData := data
		if streamBridge != nil {
			transformed, preEvents, forward := streamBridge.transformEvent([]byte(data))
			for _, pre := range preEvents {
				var preResponse dto.ResponsesStreamResponse
				if err := common.UnmarshalJsonStr(string(pre), &preResponse); err == nil {
					_ = helper.ResponseChunkData(c, preResponse, string(pre))
				}
			}
			if !forward {
				return
			}
			outData = string(transformed)
		}
		if streamResponse.Response != nil {
			outData = string(rewriteSGLangResponsesCreatedAt(info, []byte(outData), "response.created_at", streamResponse.Response.CreatedAt))
		}
		sendResponsesStreamData(c, streamResponse, outData)
		accumulator.Observe(&streamResponse, common.StringToByteSlice(outData))
	}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		processEvent(data, sr)
	})

	common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, info.StreamStatus)
	info.StreamStatus.RequireTerminal()
	return accumulator.Finish(), nil
}

func rewriteSGLangResponsesCreatedAt(info *relaycommon.RelayInfo, payload []byte, path string, createdAt dto.IntValue) []byte {
	if info.GetChannelType() != constant.ChannelTypeSGLang {
		return payload
	}
	if !gjson.GetBytes(payload, path).Exists() {
		return payload
	}
	patched, err := sjson.SetBytes(payload, path, int(createdAt))
	if err != nil {
		return payload
	}
	return patched
}

// isOpencodeUpstream reports whether the channel points at OpenCode zen/go.
// Only this upstream is known to strip reasoning from DeepSeek thinking-mode
// responses, so the empty-reasoning injection is scoped to it exclusively;
// every other upstream (including other non-native gateways like CPA) is
// forwarded untouched.
func isOpencodeUpstream(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ChannelMeta == nil {
		return false
	}
	baseURL := strings.TrimSpace(info.ChannelBaseUrl)
	if baseURL == "" {
		return false
	}
	host := baseURL
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		host = u.Host
	}
	host = strings.ToLower(host)
	return host == "opencode.ai" || strings.HasSuffix(host, ".opencode.ai")
}

// hasReasoningOutput reports whether the response output already carries a
// reasoning item. If it does, nothing is injected.
func hasReasoningOutput(outputs []dto.ResponsesOutput) bool {
	for i := range outputs {
		if outputs[i].Type == "reasoning" {
			return true
		}
	}
	return false
}

// buildEmptyReasoningOutput constructs the empty reasoning item in generic map
// form so it can be spliced into the raw response body without re-marshaling
// the DTO (which would drop unknown upstream fields).
func buildEmptyReasoningOutput(respID string, status string) map[string]any {
	return map[string]any{
		"type":   "reasoning",
		"id":     respID + "_reasoning_0",
		"status": status,
		"content": []any{map[string]any{
			"type":        "summary_text",
			"text":        "",
			"annotations": []any{},
		}},
	}
}

// injectEmptyReasoningBody splices an empty reasoning item at the front of the
// output array of a raw non-stream response body. Operates on the generic map
// so all unknown upstream fields (service_tier, expires_at, ...) survive;
// only the output array is touched.
func injectEmptyReasoningBody(body []byte, resp dto.OpenAIResponsesResponse) []byte {
	var raw map[string]any
	if err := common.Unmarshal(body, &raw); err != nil {
		return body
	}
	outputs, ok := raw["output"].([]any)
	if !ok {
		return body
	}
	status := "completed"
	if relaycommon.IsNonBillableResponsesStatus(resp.Status) {
		status = "incomplete"
	}
	reasoningItem := buildEmptyReasoningOutput(resp.ID, status)
	raw["output"] = append([]any{reasoningItem}, outputs...)
	updated, err := common.Marshal(raw)
	if err != nil {
		return body
	}
	return updated
}
