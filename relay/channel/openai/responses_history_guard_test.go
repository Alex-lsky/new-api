package openai

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Native Responses providers (real api.openai.com / Azure hostnames) must keep
// their standard function_call + output_text assistant history untouched. The
// history-rewrite workarounds are scoped to non-native OpenAI-compatible
// upstreams (e.g. CPA-proxied zen/go) that cannot handle that shape.
//
// Channel type is intentionally NOT the gate: users register CPA/zen/go
// upstreams as channel type OpenAI. The base URL is the only reliable signal.
func TestConvertOpenAIResponsesRequestPreservesNativeFunctionCallHistory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	input := []byte(`[{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{\"q\":\"x\"}"},{"type":"function_call_output","call_id":"c1","output":"42"}]`)

	nativeCases := []struct {
		name    string
		baseURL string
	}{
		{"openai-official", "https://api.openai.com"},
		{"openai-with-path", "https://api.openai.com/"},
		{"azure", "https://my-resource.openai.azure.com"},
		{"azure-cognitive", "https://my-resource.cognitiveservices.azure.com"},
	}
	for _, tc := range nativeCases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Adaptor{}
			// Deliberately channel type OpenAI (as CPA also is) to prove the URL,
			// not the type, decides nativeness.
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
			}
			info.ChannelBaseUrl = tc.baseURL
			req := dto.OpenAIResponsesRequest{Model: "gpt-test", Input: input}
			converted, err := a.ConvertOpenAIResponsesRequest(&gin.Context{}, info, req)
			require.NoError(t, err)
			out, ok := converted.(dto.OpenAIResponsesRequest)
			require.True(t, ok)
			assert.Equal(t, string(input), string(out.Input), "native upstream history must not be rewritten")
		})
	}
}

// Non-native upstreams — including those registered as channel type OpenAI —
// trigger both rewrites. Covers the real CPA deployment (type=1, local proxy
// http://10.0.0.104:8317) and opencode zen/go (type=1, opencode.ai host).
func TestConvertOpenAIResponsesRequestRewritesHistoryForNonNativeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	input := []byte(`[{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{\"q\":\"x\"}"},{"type":"function_call_output","call_id":"c1","output":"42"}]`)

	nonNativeCases := []struct {
		name    string
		baseURL string
	}{
		{"cpa-local-proxy", "http://10.0.0.104:8317"},
		{"opencode-zen-go", "https://opencode.ai/zen/go"},
		{"custom-host", "https://my-gateway.example.com"},
	}
	for _, tc := range nonNativeCases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Adaptor{}
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
			}
			info.ChannelBaseUrl = tc.baseURL
			req := dto.OpenAIResponsesRequest{Model: "gpt-test", Input: input}
			converted, err := a.ConvertOpenAIResponsesRequest(&gin.Context{}, info, req)
			require.NoError(t, err)
			out, ok := converted.(dto.OpenAIResponsesRequest)
			require.True(t, ok)
			assert.NotEqual(t, string(input), string(out.Input), "non-native upstream history should be rewritten")
			assert.Contains(t, string(out.Input), "[tool call]")
			assert.Contains(t, string(out.Input), "[tool result]")
		})
	}
}

// Hosted tool declarations like {"type":"web_search"} must never gain an
// artificial name: strict native Responses upstreams (api.openai.com, Azure,
// and OpenAI-compatible codex surfaces reached via CPA) reject the extra field
// with "Unknown parameter: 'tools[N].name'". zen/go-style upstreams that need
// every tool named are covered by emulate/strip channel config, which rewrites
// hosted declarations into named functions before the adaptor runs.
func TestConvertOpenAIResponsesRequestNeverInjectsHostedToolNames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tools := []byte(`[{"type":"web_search"},{"type":"web_search_preview"},{"type":"image_generation"}]`)

	for _, baseURL := range []string{
		"https://api.openai.com",
		"https://my-resource.openai.azure.com",
		"http://10.0.0.104:8317",
		"https://opencode.ai/zen/go",
		"https://my-gateway.example.com",
	} {
		t.Run(baseURL, func(t *testing.T) {
			a := &Adaptor{}
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
			}
			info.ChannelBaseUrl = baseURL
			req := dto.OpenAIResponsesRequest{Model: "gpt-test", Input: []byte(`"hi"`), Tools: tools}
			converted, err := a.ConvertOpenAIResponsesRequest(&gin.Context{}, info, req)
			require.NoError(t, err)
			out, ok := converted.(dto.OpenAIResponsesRequest)
			require.True(t, ok)
			assert.Equal(t, string(tools), string(out.Tools), "hosted tool declarations must pass through byte-identical")
		})
	}
}
