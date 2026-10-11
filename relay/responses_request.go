package relay

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// PrepareResponsesRequest applies the same model, conversion and channel rules
// for HTTP and WebSocket requests. The caller closes closer after the attempt;
// passthrough bodies remain owned by the incoming request's BodyStorage.
// The returned adaptor retains route/conversion state for DoRequest/DoResponse.
func PrepareResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, req *dto.OpenAIResponsesRequest) (relaychannel.Adaptor, common.ReplayableBody, io.Closer, []byte, *types.NewAPIError) {
	info.InitChannelMeta(c)
	if info.RelayMode == relayconstant.RelayModeResponsesCompact &&
		!common.SupportsResponsesCompact(info.ChannelType, info.ApiType) {
		return nil, nil, nil, nil, types.NewErrorWithStatusCode(
			fmt.Errorf("unsupported endpoint %q for api type %d", "/v1/responses/compact", info.ApiType),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	request, err := common.DeepCopy(req)
	if err != nil {
		return nil, nil, nil, nil, types.NewError(fmt.Errorf("failed to copy responses request: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	if err := helper.ModelMappedHelper(c, info, request); err != nil {
		return nil, nil, nil, nil, types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}
	if err := helper.ApplyReasoningModelSuffix(c, info, request); err != nil {
		return nil, nil, nil, nil, newConvertRequestFailedError(c, info, err)
	}
	request.Tools = normalizeResponsesTools(request.Tools)

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return nil, nil, nil, nil, types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)
	var requestBody common.ReplayableBody
	var closer io.Closer = io.NopCloser(nil)
	var emulatedBaseBody []byte
	bridgeKinds := info.ChannelSetting.BridgeToolTypeSet()
	// hosted tools the gateway executes itself need to survive strip lists.
	// Enabling is channel-level: the channel's own emulate_tool_types config,
	// with named global providers (EmulatedToolBackend.Ref) resolved here.
	emulateBackends := resolveEmulatedToolRefs(info.ChannelSetting.EmulatedBackendsForRequest())
	emulateSet := emulatedTypeSetFromBackends(emulateBackends)
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return nil, nil, nil, nil, types.NewError(err, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
		}
		// Transforms are all no-ops when their channel config is absent and
		// nothing needs rewriting, so running them unconditionally keeps
		// tool-name sanitization universal for passthrough channels.
		stripToolTypes := exemptEmulatedFromStrip(info.ChannelSetting.StripToolTypeSet(), emulateBackends)
		raw, rawErr := storage.Bytes()
		if rawErr == nil {
			transformed, changed := applyResponsesPassThroughTransforms(c, info, raw, stripToolTypes, bridgeKinds, emulateSet, emulateBackends)
			if info.EmulatedTools != nil {
				emulatedBaseBody = transformed
			} else if changed {
				body, bodyCloser, err := relaycommon.NewOutboundJSONBody(transformed)
				if err != nil {
					return nil, nil, nil, nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
				}
				requestBody = body
				closer = bodyCloser
			}
		}
		if requestBody == nil && info.EmulatedTools == nil {
			requestBody = common.NewReplayableBodyReader(storage)
		}
	} else {
		convertedRequest, err := adaptor.ConvertOpenAIResponsesRequest(c, info, *request)
		if err != nil {
			return nil, nil, nil, nil, newConvertRequestFailedError(c, info, err)
		}
		relaycommon.AppendRequestConversionFromRequest(info, convertedRequest)
		jsonData, err := common.Marshal(convertedRequest)
		if err != nil {
			return nil, nil, nil, nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
		if err != nil {
			return nil, nil, nil, nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		if len(info.ParamOverride) > 0 {
			jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
			if err != nil {
				return nil, nil, nil, nil, newAPIErrorFromParamOverride(err)
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

		// tool names longer than 64 characters are hard-rejected by strict
		// upstreams; truncate deterministically and restore originals on the
		// response, before the emulation loop snapshots the outbound body
		{
			bridge := info.ClientToolBridge
			if bridge == nil {
				bridge = relaycommon.NewResponsesClientToolBridge()
			}
			if sanitized := sanitizeResponsesToolNames(jsonData, bridge); !bytes.Equal(sanitized, jsonData) {
				jsonData = sanitized
				info.ClientToolBridge = bridge
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
			if emulated := emulateResponsesHostedTools(jsonData, emulateSet, bridge); !bytes.Equal(emulated, jsonData) {
				jsonData = emulated
				info.ClientToolBridge = bridge
				info.EmulatedTools = emulateBackends
				emulatedBaseBody = jsonData
			}
		}

		if info.EmulatedTools == nil {
			logger.LogDebug(c, "requestBody: %s", jsonData)
			body, bodyCloser, err := relaycommon.NewOutboundJSONBody(jsonData)
			if err != nil {
				return nil, nil, nil, nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			}
			requestBody = body
			closer = bodyCloser
		}
	}

	// Textualized tool-call repair (channel repair_text_tool_call_models):
	// collect every tool name the outbound request declared — the client's
	// names plus whatever the bridge renamed them to — so the response side
	// only repairs textual calls to tools this request actually carries.
	if repairModels := info.ChannelSetting.RepairTextToolCallModelSet(); repairModels != nil {
		if _, ok := repairModels[strings.TrimSpace(info.OriginModelName)]; ok {
			toolNames := make(map[string]struct{})
			for _, tool := range gjson.ParseBytes(request.Tools).Array() {
				if name := strings.TrimSpace(tool.Get("name").String()); name != "" {
					toolNames[name] = struct{}{}
				}
			}
			for _, name := range info.ClientToolBridge.Names() {
				toolNames[name] = struct{}{}
			}
			info.ResponsesRepairToolNames = toolNames
		}
	}

	return adaptor, requestBody, closer, emulatedBaseBody, nil
}
