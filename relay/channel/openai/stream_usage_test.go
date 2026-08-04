package openai

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"

	"github.com/gin-gonic/gin"
)

func TestNormalizeUsage(t *testing.T) {
	t.Run("standard openai usage unchanged", func(t *testing.T) {
		u := &dto.Usage{
			PromptTokens:        100,
			CompletionTokens:    20,
			TotalTokens:         120,
			PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 90},
		}
		normalizeUsage(u)
		require.Equal(t, 100, u.PromptTokens)
		require.Equal(t, 20, u.CompletionTokens)
		require.Equal(t, 120, u.TotalTokens)
		require.Equal(t, 90, u.PromptTokensDetails.CachedTokens)
		require.Equal(t, 0, u.InputTokens)
		require.Equal(t, 0, u.OutputTokens)
	})

	t.Run("input_tokens and output_tokens mapped to standard fields", func(t *testing.T) {
		u := &dto.Usage{
			InputTokens:          100,
			OutputTokens:         20,
			PromptCacheHitTokens: 80,
		}
		normalizeUsage(u)
		require.Equal(t, 100, u.PromptTokens)
		require.Equal(t, 20, u.CompletionTokens)
		require.Equal(t, 120, u.TotalTokens)
		require.Equal(t, 80, u.PromptTokensDetails.CachedTokens)
	})

	t.Run("input_tokens_details cached_tokens mapped", func(t *testing.T) {
		u := &dto.Usage{
			InputTokens:        100,
			OutputTokens:       20,
			InputTokensDetails: &dto.InputTokenDetails{CachedTokens: 75},
		}
		normalizeUsage(u)
		require.Equal(t, 75, u.PromptTokensDetails.CachedTokens)
	})

	t.Run("nil usage no-op", func(t *testing.T) {
		normalizeUsage(nil)
	})
}

func TestOaiStreamHandlerForwardsStandardOpenAIStream(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"}}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world"}}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-4o","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":12,"total_tokens":21,"prompt_tokens_details":{"cached_tokens":6}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)

	usage, apiErr := OaiStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	require.Equal(t, 9, usage.PromptTokens)
	require.Equal(t, 12, usage.CompletionTokens)
	require.Equal(t, 21, usage.TotalTokens)
	require.Equal(t, 6, usage.PromptTokensDetails.CachedTokens)

	got := recorder.Body.String()
	require.Contains(t, got, `"content":"Hello"`)
	require.Contains(t, got, `"content":" world"`)
	require.Contains(t, got, `"finish_reason":"stop"`)
	require.Contains(t, got, `"usage":{"prompt_tokens":9,"completion_tokens":12,"total_tokens":21`)
	require.Contains(t, got, `data: [DONE]`)
}

func TestOaiStreamHandlerReadsUsageFromStreamChunks(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	header := `data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"deepseek-v4-flash","choices":[{"index":0,"delta":{"role":"assistant"}}]}`
	content := `data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"deepseek-v4-flash","choices":[{"index":0,"delta":{"content":"hi"}}]}`

	tests := []struct {
		name                      string
		body                      string
		prompt, completion, total int
		cached                    int
	}{
		{
			name: "standard usage on final chunk",
			body: strings.Join([]string{
				header,
				content,
				`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"deepseek-v4-flash","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10,"prompt_tokens_details":{"cached_tokens":5}}}`,
				`data: [DONE]`,
				``,
			}, "\n"),
			prompt: 7, completion: 3, total: 10, cached: 5,
		},
		{
			name: "non-standard usage on non-final chunk",
			body: strings.Join([]string{
				header,
				content,
				`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"deepseek-v4-flash","choices":[],"usage":{"input_tokens":100,"output_tokens":20,"prompt_cache_hit_tokens":80}}`,
				`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"deepseek-v4-flash","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
				``,
			}, "\n"),
			prompt: 100, completion: 20, total: 120, cached: 80,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _, resp, info := newResponsesChatTestContext(t, tt.body, true)
			info.ChannelType = constant.ChannelTypeDeepSeek

			usage, apiErr := OaiStreamHandler(c, info, resp)
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			require.Equal(t, tt.prompt, usage.PromptTokens)
			require.Equal(t, tt.completion, usage.CompletionTokens)
			require.Equal(t, tt.total, usage.TotalTokens)
			require.Equal(t, tt.cached, usage.PromptTokensDetails.CachedTokens)
		})
	}
}
