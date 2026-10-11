package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func ResponsesHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	var responsesReq *dto.OpenAIResponsesRequest
	switch req := info.Request.(type) {
	case *dto.OpenAIResponsesRequest:
		responsesReq = req
	case *dto.OpenAIResponsesCompactionRequest:
		// Only fields documented for POST /v1/responses/compact are forwarded:
		// model, input, instructions, previous_response_id, prompt_cache_key,
		// prompt_cache_options, prompt_cache_retention, service_tier.
		// Undocumented Codex-parity fields (tools, reasoning, text) are parsed
		// for client compatibility but intentionally not sent upstream.
		responsesReq = &dto.OpenAIResponsesRequest{
			Model:                req.Model,
			Input:                req.Input,
			Instructions:         req.Instructions,
			PreviousResponseID:   req.PreviousResponseID,
			ParallelToolCalls:    req.ParallelToolCalls,
			ServiceTier:          req.ServiceTier,
			PromptCacheKey:       req.PromptCacheKey,
			PromptCacheOptions:   req.PromptCacheOptions,
			PromptCacheRetention: req.PromptCacheRetention,
		}
	default:
		return types.NewErrorWithStatusCode(
			fmt.Errorf("invalid request type, expected dto.OpenAIResponsesRequest or dto.OpenAIResponsesCompactionRequest, got %T", info.Request),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	adaptor, requestBody, closer, emulatedBaseBody, apiErr := PrepareResponsesRequest(c, info, responsesReq)
	if apiErr != nil {
		return apiErr
	}
	defer closer.Close()

	var httpResp *http.Response
	statusCodeMappingStr := c.GetString("status_code_mapping")

	var usageDto *dto.Usage
	if info.EmulatedTools != nil {
		// gateway-executed hosted tools: iterate upstream rounds, execute the
		// tool calls in between, and only the final round reaches the client
		usage, loopErr := runResponsesEmulationLoop(c, info,
			responsesRetryDoRequest(c, func(body io.Reader) (any, error) {
				return adaptor.DoRequest(c, info, body)
			}),
			func(roundResp *http.Response) (*dto.Usage, *types.NewAPIError) {
				usageAny, apiErr := adaptor.DoResponse(c, roundResp, info)
				if apiErr != nil {
					return nil, apiErr
				}
				roundUsage, _ := usageAny.(*dto.Usage)
				if roundUsage == nil {
					roundUsage = &dto.Usage{}
				}
				return roundUsage, nil
			},
			emulatedBaseBody, info.EmulatedTools)
		if loopErr != nil {
			service.ResetStatusCode(loopErr, statusCodeMappingStr)
			return loopErr
		}
		usageDto = usage
	} else {
		resp, err := responsesRetryDoRequest(c, func(body io.Reader) (any, error) {
			return adaptor.DoRequest(c, info, body)
		})(requestBody)
		if err != nil {
			return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
		}

		if resp != nil {
			httpResp = resp.(*http.Response)

			if httpResp.StatusCode != http.StatusOK {
				newAPIError = service.RelayErrorHandler(c.Request.Context(), httpResp, false)
				// reset status code 重置状态码
				service.ResetStatusCode(newAPIError, statusCodeMappingStr)
				return newAPIError
			}
		}

		usage, respErr := adaptor.DoResponse(c, httpResp, info)
		if respErr != nil {
			// reset status code 重置状态码
			service.ResetStatusCode(respErr, statusCodeMappingStr)
			return respErr
		}
		usageDto = usage.(*dto.Usage)
	}

	if info.RelayMode == relayconstant.RelayModeResponsesCompact {
		originModelName := info.OriginModelName
		originPriceData := info.PriceData

		_, err := helper.ModelPriceHelper(c, info, info.GetEstimatePromptTokens(), &types.TokenCountMeta{})
		if err != nil {
			info.OriginModelName = originModelName
			info.PriceData = originPriceData
			return types.NewError(err, types.ErrorCodeModelPriceError, types.ErrOptionWithSkipRetry(), types.ErrOptionWithStatusCode(http.StatusBadRequest))
		}
		service.PostTextConsumeQuota(c, info, usageDto, nil)

		info.OriginModelName = originModelName
		info.PriceData = originPriceData
		return nil
	}

	ConsumeResponsesQuota(c, info, usageDto)
	return nil
}

func applyResponsesPassThroughTransforms(c *gin.Context, info *relaycommon.RelayInfo, body []byte, stripToolTypes map[string]struct{}, bridgeKinds map[string]struct{}, emulateSet map[string]struct{}, emulateBackends map[string]*dto.EmulatedToolBackend) ([]byte, bool) {
	result := body
	changed := false

	if stripToolTypes != nil {
		if stripped := stripResponsesToolTypes(result, stripToolTypes); !bytes.Equal(stripped, result) {
			result = stripped
			changed = true
			logger.LogDebug(c, "requestBody after strip_tool_types: %s", result)
		}
	}

	if bridgeKinds != nil {
		bridge := relaycommon.NewResponsesClientToolBridge()
		bridged := bridgeResponsesClientTools(result, bridgeKinds, bridge)
		if bridge.Len() > 0 {
			info.ClientToolBridge = bridge
		}
		if !bytes.Equal(bridged, result) {
			result = bridged
			changed = true
			logger.LogDebug(c, "requestBody after bridge_tool_types: %s", result)
		}
	}

	if len(emulateBackends) > 0 {
		bridge := info.ClientToolBridge
		if bridge == nil {
			bridge = relaycommon.NewResponsesClientToolBridge()
		}
		if emulated := emulateResponsesHostedTools(result, emulateSet, bridge); !bytes.Equal(emulated, result) {
			result = emulated
			changed = true
			info.ClientToolBridge = bridge
			info.EmulatedTools = emulateBackends
			logger.LogDebug(c, "requestBody after emulate_tool_types: %s", result)
		}
	}

	// tool names longer than 64 characters are hard-rejected by strict
	// upstreams; truncate deterministically and restore originals on the
	// response. Runs last so it also covers names re-introduced above.
	{
		bridge := info.ClientToolBridge
		if bridge == nil {
			bridge = relaycommon.NewResponsesClientToolBridge()
		}
		if sanitized := sanitizeResponsesToolNames(result, bridge); !bytes.Equal(sanitized, result) {
			result = sanitized
			changed = true
			info.ClientToolBridge = bridge
			logger.LogDebug(c, "requestBody after tool name sanitize: %s", result)
		}
	}

	return result, changed
}

func normalizeResponsesTools(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	result := raw
	changed := false
	tools := gjson.ParseBytes(raw).Array()
	for i, tool := range tools {
		fn := tool.Get("function")
		if fn.IsObject() && !tool.Get("name").Exists() {
			for _, key := range []string{"name", "description", "parameters", "strict"} {
				if !fn.Get(key).Exists() {
					continue
				}
				var err error
				result, err = sjson.SetRawBytes(result, fmt.Sprintf("%d.%s", i, key), []byte(fn.Get(key).Raw))
				if err != nil {
					return raw
				}
			}
			var err error
			result, err = sjson.DeleteBytes(result, fmt.Sprintf("%d.function", i))
			if err != nil {
				return raw
			}
			changed = true
		}
	}
	if !changed {
		return raw
	}
	return result
}

// ConsumeResponsesQuota applies the same settlement dispatch to HTTP and
// WebSocket Responses usage. Compact requests keep their separate repricing.
func ConsumeResponsesQuota(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage) {
	if strings.HasPrefix(info.OriginModelName, "gpt-4o-audio") {
		service.PostAudioConsumeQuota(c, info, usage, nil)
		return
	}
	service.PostTextConsumeQuota(c, info, usage, nil)
}
