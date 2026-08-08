package openai

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
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

	// OpenCode zen/go strips the reasoning item from DeepSeek thinking-mode
	// responses. Codex then cannot pass reasoning_content back on the next
	// turn, which DeepSeek requires in thinking mode. Inject an empty
	// reasoning item only when the upstream is OpenCode and the response
	// genuinely lacks one; any other upstream or a response that already
	// carries reasoning is forwarded byte-for-byte untouched.
	if isOpencodeUpstream(info) && !hasReasoningOutput(responsesResponse.Output) {
		responseBody = injectEmptyReasoningBody(responseBody, responsesResponse)
	}

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	// compute usage
	usage := dto.Usage{}
	if responsesResponse.Usage != nil {
		usage.PromptTokens = responsesResponse.Usage.InputTokens
		usage.CompletionTokens = responsesResponse.Usage.OutputTokens
		usage.TotalTokens = responsesResponse.Usage.TotalTokens
		if responsesResponse.Usage.InputTokensDetails != nil {
			usage.PromptTokensDetails.CachedTokens = responsesResponse.Usage.InputTokensDetails.CachedTokens
			usage.PromptTokensDetails.CacheWriteTokens = responsesResponse.Usage.InputTokensDetails.CacheWriteTokens
		}
	}
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

	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	if !relaycommon.IsNonBillableResponsesStatus(responsesResponse.Status) {
		for i := range responsesResponse.Output {
			idx := i
			imageCounter.Observe(&responsesResponse.Output[i], &idx)
		}
	}
	imageCounter.Commit(info)

	return &usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)

	var usage = &dto.Usage{}
	var responseTextBuilder strings.Builder
	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	imageCommitted := false

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

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {

		// 检查当前数据是否包含 completed 状态和 usage 信息
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
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
		sendResponsesStreamData(c, streamResponse, data)
		switch streamResponse.Type {
		case "response.completed", "response.done":
			if streamResponse.Response != nil {
				if streamResponse.Response.Usage != nil {
					if streamResponse.Response.Usage.InputTokens != 0 {
						usage.PromptTokens = streamResponse.Response.Usage.InputTokens
					}
					if streamResponse.Response.Usage.OutputTokens != 0 {
						usage.CompletionTokens = streamResponse.Response.Usage.OutputTokens
					}
					if streamResponse.Response.Usage.TotalTokens != 0 {
						usage.TotalTokens = streamResponse.Response.Usage.TotalTokens
					}
					if streamResponse.Response.Usage.InputTokensDetails != nil {
						usage.PromptTokensDetails.CachedTokens = streamResponse.Response.Usage.InputTokensDetails.CachedTokens
						usage.PromptTokensDetails.CacheWriteTokens = streamResponse.Response.Usage.InputTokensDetails.CacheWriteTokens
					}
				}
				if !imageCommitted {
					if relaycommon.IsNonBillableResponsesStatus(streamResponse.Response.Status) {
						imageCounter.Reset()
						imageCounter.Commit(info)
						imageCommitted = true
					} else {
						for i := range streamResponse.Response.Output {
							idx := i
							imageCounter.Observe(&streamResponse.Response.Output[i], &idx)
						}
						imageCounter.Commit(info)
						imageCommitted = true
					}
				}
			} else if !imageCommitted {
				imageCounter.Commit(info)
				imageCommitted = true
			}
		case "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
			if !imageCommitted {
				imageCounter.Reset()
				imageCounter.Commit(info)
				imageCommitted = true
			}
		case "response.output_text.delta":
			// 处理输出文本
			responseTextBuilder.WriteString(streamResponse.Delta)
		case dto.ResponsesOutputTypeItemDone:
			if streamResponse.Item != nil {
				switch streamResponse.Item.Type {
				case dto.BuildInCallWebSearchCall:
					info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
				case dto.BuildInCallFileSearchCall:
					info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
				case dto.BuildInCallFunctionCall:
					info.CountBillableToolCall(dto.BuildInCallFunctionCall, streamResponse.Item.Name)
				case dto.ResponsesOutputTypeImageGenerationCall:
					if !imageCommitted {
						imageCounter.Observe(streamResponse.Item, streamResponse.OutputIndex)
					}
				}
			}
		}
	})

	if usage.CompletionTokens == 0 {
		// 计算输出文本的 token 数量
		tempStr := responseTextBuilder.String()
		if len(tempStr) > 0 {
			// 非正常结束，使用输出文本的 token 数量
			completionTokens := service.CountTextToken(tempStr, info.UpstreamModelName)
			usage.CompletionTokens = completionTokens
		}
	}

	if usage.PromptTokens == 0 && usage.CompletionTokens != 0 {
		usage.PromptTokens = info.GetEstimatePromptTokens()
	}

	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

	return usage, nil
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
