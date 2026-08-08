package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newResponsesHandlerTestContext(t *testing.T, body string, isStream bool, baseURL string) (*gin.Context, *httptest.ResponseRecorder, *http.Response, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	if isStream {
		oldTimeout := constant.StreamingTimeout
		constant.StreamingTimeout = 30
		t.Cleanup(func() {
			constant.StreamingTimeout = oldTimeout
		})
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(common.RequestIdKey, "responses-reasoning-test")
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 1, UpstreamModelName: "deepseek-v4-flash"},
		DisablePing: true,
	}
	info.ChannelBaseUrl = baseURL
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	return c, w, resp, info
}

// opencode 精简流（只有 output_text.delta + completed，无 reasoning 事件）
// → 应注入空 reasoning 事件序列，且 message 的 output_index 提升到 1。
func TestOaiResponsesStreamHandlerInjectsEmptyReasoningForOpencode(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`,
		`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"hello"}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		``,
	}, "\n")
	c, w, resp, info := newResponsesHandlerTestContext(t, body, true, "https://opencode.ai/zen/go")

	usage, apiErr := OaiResponsesStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	out := w.Body.String()
	// 注入 4 个 reasoning 事件
	assert.Contains(t, out, `"type":"reasoning"`)
	assert.Contains(t, out, `"type":"response.reasoning_summary_text.delta"`)
	assert.Contains(t, out, `"type":"response.reasoning_summary_text.done"`)
	// reasoning item 的 added/done 事件（map marshal 字典序，item 在 output_index 前）
	assert.Equal(t, 1, strings.Count(out, `"item":{"content":[],"id":"reasoning_`))
	assert.Contains(t, out, `"type":"response.output_item.done"`)
	// message 事件提升到 output_index 1
	assert.Contains(t, out, `"item_id":"msg_`)
	assert.Contains(t, out, `"output_index":1`)
}

// opencode 完整流（上游已发 reasoning 事件）→ 不注入，原样透传。
func TestOaiResponsesStreamHandlerDoesNotInjectWhenReasoningPresent(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","status":"in_progress","content":[]}}`,
		`data: {"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"thinking"}`,
		`data: {"type":"response.reasoning_summary_text.done","output_index":0,"summary_index":0,"item_id":"rs_1","part":{"type":"summary_text","text":"thinking"}}`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","status":"completed","content":[{"type":"summary_text","text":"thinking"}]}}`,
		`data: {"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"hello"}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		``,
	}, "\n")
	c, w, resp, info := newResponsesHandlerTestContext(t, body, true, "https://opencode.ai/zen/go")

	usage, apiErr := OaiResponsesStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	out := w.Body.String()
	// 上游已有 reasoning → 注入逻辑不触发：reasoning_summary_text.delta 仍只有
	// 输入流里的 1 个（注入会产生第二个空 delta）。
	assert.Equal(t, 1, strings.Count(out, `"type":"response.reasoning_summary_text.delta"`))
	// reasoning item 保持原样（id 仍是 rs_1），没有额外 inserted reasoning item。
	assert.Contains(t, out, `"id":"rs_1"`)
	// output_item.added 只有输入的 reasoning 那 1 个：reasoning 的 added 已标记
	// message item 存在，后续 message delta 不再触发补全（注入逻辑也未触发）。
	assert.Equal(t, 1, strings.Count(out, `"type":"response.output_item.added"`))
}

// 非 opencode 渠道（CPA 本地代理）→ 不注入。
func TestOaiResponsesStreamHandlerDoesNotInjectForNonOpencode(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"hello"}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		``,
	}, "\n")
	c, w, resp, info := newResponsesHandlerTestContext(t, body, true, "http://10.0.0.104:8317")

	usage, apiErr := OaiResponsesStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	out := w.Body.String()
	assert.NotContains(t, out, `"type":"response.reasoning_summary_text.delta"`)
	assert.NotContains(t, out, `"type":"reasoning"`)
}

// opencode 非流式响应且 Output 无 reasoning → 注入空 reasoning item，
// 且未知字段（service_tier）保留。
func TestOaiResponsesHandlerInjectsEmptyReasoningForOpencode(t *testing.T) {
	rawBody := `{"id":"resp_1","object":"response","created_at":123,"status":"completed","model":"deepseek-v4-flash","service_tier":"flex","output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	c, w, resp, info := newResponsesHandlerTestContext(t, rawBody, false, "https://opencode.ai/zen/go")

	usage, apiErr := OaiResponsesHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	var out map[string]any
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &out))
	outputs, ok := out["output"].([]any)
	require.True(t, ok)
	require.Len(t, outputs, 2)
	first := outputs[0].(map[string]any)
	assert.Equal(t, "reasoning", first["type"])
	assert.Equal(t, "resp_1_reasoning_0", first["id"])
	// 未知字段保留
	assert.Equal(t, "flex", out["service_tier"])
	// 原 message 仍在
	second := outputs[1].(map[string]any)
	assert.Equal(t, "message", second["type"])
}

// opencode 非流式响应且已有 reasoning → 不注入。
func TestOaiResponsesHandlerDoesNotInjectWhenReasoningPresent(t *testing.T) {
	rawBody := `{"id":"resp_1","object":"response","created_at":123,"status":"completed","model":"deepseek-v4-flash","output":[{"type":"reasoning","id":"rs_1","status":"completed","content":[{"type":"summary_text","text":"thinking"}]},{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	c, w, resp, info := newResponsesHandlerTestContext(t, rawBody, false, "https://opencode.ai/zen/go")

	usage, apiErr := OaiResponsesHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	var out map[string]any
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &out))
	outputs, ok := out["output"].([]any)
	require.True(t, ok)
	require.Len(t, outputs, 2) // 未注入，仍只有 2 个
	assert.Equal(t, "reasoning", outputs[0].(map[string]any)["type"])
	assert.Equal(t, "rs_1", outputs[0].(map[string]any)["id"])
}

// 非 opencode 非流式 → 不注入。
func TestOaiResponsesHandlerDoesNotInjectForNonOpencode(t *testing.T) {
	rawBody := `{"id":"resp_1","object":"response","created_at":123,"status":"completed","model":"deepseek-v4-flash","output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	c, w, resp, info := newResponsesHandlerTestContext(t, rawBody, false, "http://10.0.0.104:8317")

	usage, apiErr := OaiResponsesHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	var out map[string]any
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &out))
	outputs, ok := out["output"].([]any)
	require.True(t, ok)
	require.Len(t, outputs, 1)
	assert.Equal(t, "message", outputs[0].(map[string]any)["type"])
}
