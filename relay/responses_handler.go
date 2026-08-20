package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func ResponsesHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)
	if info.RelayMode == relayconstant.RelayModeResponsesCompact &&
		!common.SupportsResponsesCompact(info.ChannelType, info.ApiType) {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("unsupported endpoint %q for api type %d", "/v1/responses/compact", info.ApiType),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

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

	request, err := common.DeepCopy(responsesReq)
	if err != nil {
		return types.NewError(fmt.Errorf("failed to copy request to GeneralOpenAIRequest: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	err = helper.ModelMappedHelper(c, info, request)
	if err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	request.Tools = normalizeResponsesTools(request.Tools)

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)
	var requestBody io.Reader
	var emulatedBaseBody []byte
	bridgeKinds := info.ChannelSetting.BridgeToolTypeSet()
	// hosted tools the gateway executes itself need to survive strip lists
	emulateBackends := info.ChannelSetting.EmulatedBackendsForRequest()
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return types.NewError(err, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
		}
		if stripToolTypes := exemptEmulatedFromStrip(info.ChannelSetting.StripToolTypeSet(), emulateBackends); stripToolTypes != nil {
			raw, err := storage.Bytes()
			if err == nil {
				stripped := stripResponsesToolTypes(raw, stripToolTypes)
				if !bytes.Equal(stripped, raw) {
					logger.LogDebug(c, "requestBody after strip_tool_types: %s", stripped)
					body, closer, err := relaycommon.NewOutboundJSONBody(stripped)
					if err != nil {
						return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
					}
					defer closer.Close()
					requestBody = body
				}
			}
		}
		if bridgeKinds != nil && requestBody == nil {
			raw, err := storage.Bytes()
			if err == nil {
				bridge := relaycommon.NewResponsesClientToolBridge()
				if bridged := bridgeResponsesClientTools(raw, bridgeKinds, bridge); !bytes.Equal(bridged, raw) {
					info.ClientToolBridge = bridge
					logger.LogDebug(c, "requestBody after bridge_tool_types: %s", bridged)
					body, closer, err := relaycommon.NewOutboundJSONBody(bridged)
					if err != nil {
						return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
					}
					defer closer.Close()
					requestBody = body
				}
			}
		}
		// hosted tools the gateway executes itself: rewrite declarations into
		// functions and remember the backends so the request runs through the
		// emulation loop instead of a single shot
		if len(emulateBackends) > 0 && requestBody == nil {
			raw, err := storage.Bytes()
			if err == nil {
				bridge := info.ClientToolBridge
				if bridge == nil {
					bridge = relaycommon.NewResponsesClientToolBridge()
				}
				if emulated := emulateResponsesHostedTools(raw, info.ChannelSetting.EmulateToolTypeSet(), bridge); !bytes.Equal(emulated, raw) {
					logger.LogDebug(c, "requestBody after emulate_tool_types: %s", emulated)
					info.ClientToolBridge = bridge
					info.EmulatedTools = emulateBackends
					emulatedBaseBody = emulated
				}
			}
		}
		if requestBody == nil && info.EmulatedTools == nil {
			requestBody = common.NewReplayableBodyReader(storage)
		}
	} else {
		convertedRequest, err := adaptor.ConvertOpenAIResponsesRequest(c, info, *request)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		relaycommon.AppendRequestConversionFromRequest(info, convertedRequest)
		jsonData, err := common.Marshal(convertedRequest)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}

		// remove disabled fields for OpenAI Responses API
		jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}

		// apply param override
		if len(info.ParamOverride) > 0 {
			jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
			if err != nil {
				return newAPIErrorFromParamOverride(err)
			}
		}

		// strip channel-blacklisted tool types last so nothing re-introduces
		// a tool the upstream rejects; emulated tools are exempt because the
		// gateway rewrites them into functions it can execute itself
		if stripToolTypes := exemptEmulatedFromStrip(info.ChannelSetting.StripToolTypeSet(), emulateBackends); stripToolTypes != nil {
			jsonData = stripResponsesToolTypes(jsonData, stripToolTypes)
		}

		// bridge client-executed tool kinds (custom/namespace/tool_search) into
		// function tools for function-only upstreams, recording the mapping for
		// the response-side restore
		if bridgeKinds != nil {
			bridge := relaycommon.NewResponsesClientToolBridge()
			if bridged := bridgeResponsesClientTools(jsonData, bridgeKinds, bridge); !bytes.Equal(bridged, jsonData) {
				jsonData = bridged
				if bridge.Len() > 0 {
					info.ClientToolBridge = bridge
				}
			}
		}

		// hosted tools the gateway executes itself: rewrite the declarations
		// into functions and remember the backends so the request runs through
		// the emulation loop instead of a single shot
		if len(emulateBackends) > 0 {
			bridge := info.ClientToolBridge
			if bridge == nil {
				bridge = relaycommon.NewResponsesClientToolBridge()
			}
			if emulated := emulateResponsesHostedTools(jsonData, info.ChannelSetting.EmulateToolTypeSet(), bridge); !bytes.Equal(emulated, jsonData) {
				jsonData = emulated
				info.ClientToolBridge = bridge
				info.EmulatedTools = emulateBackends
				emulatedBaseBody = jsonData
			}
		}

		if info.EmulatedTools == nil {
			logger.LogDebug(c, "requestBody: %s", jsonData)
			body, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
			if err != nil {
				return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			}
			defer closer.Close()
			jsonData = nil
			requestBody = body
		}
	}

	var httpResp *http.Response
	statusCodeMappingStr := c.GetString("status_code_mapping")

	var usageDto *dto.Usage
	if info.EmulatedTools != nil {
		// gateway-executed hosted tools: iterate upstream rounds, execute the
		// tool calls in between, and only the final round reaches the client
		usage, loopErr := runResponsesEmulationLoop(c, info,
			func(body io.Reader) (any, error) {
				return adaptor.DoRequest(c, info, body)
			},
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
		resp, err := adaptor.DoRequest(c, info, requestBody)
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

	if strings.HasPrefix(info.OriginModelName, "gpt-4o-audio") {
		service.PostAudioConsumeQuota(c, info, usageDto, "")
	} else {
		service.PostTextConsumeQuota(c, info, usageDto, nil)
	}
	return nil
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
		if !gjson.GetBytes(result, fmt.Sprintf("%d.name", i)).Exists() {
			var err error
			result, err = sjson.SetBytes(result, fmt.Sprintf("%d.name", i), tool.Get("type").String())
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
