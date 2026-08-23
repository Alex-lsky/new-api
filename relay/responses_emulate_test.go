package relay

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newTestBridgeWithWebSearch() *relaycommon.ResponsesClientToolBridge {
	bridge := relaycommon.NewResponsesClientToolBridge()
	bridge.Register("web_search", relaycommon.ResponsesClientToolSpec{Kind: relaycommon.ResponsesClientToolWebSearch, Name: "web_search"})
	return bridge
}

func testRelayInfoWithBridge() *relaycommon.RelayInfo {
	info := &relaycommon.RelayInfo{}
	info.ClientToolBridge = newTestBridgeWithWebSearch()
	return info
}

func emulateSet() map[string]struct{} {
	return map[string]struct{}{"web_search": {}}
}

func TestEmulateResponsesHostedToolsDeclaration(t *testing.T) {
	body := []byte(`{
		"model": "muse-spark-1.2",
		"input": [{"type":"message","role":"user","content":[{"type":"input_text","text":"latest go version"}]}],
		"tools": [
			{"type":"function","name":"shell","parameters":{"type":"object"}},
			{"type":"web_search"},
			{"type":"web_search_preview"},
			{"type":"image_generation"}
		]
	}`)
	bridge := relaycommon.NewResponsesClientToolBridge()
	out := emulateResponsesHostedTools(body, emulateSet(), bridge)

	tools := gjson.GetBytes(out, "tools").Array()
	require.Len(t, tools, 3, "two web_search declarations collapse into one function; image_generation untouched")
	assert.Equal(t, "function", tools[0].Get("type").String())
	assert.Equal(t, "web_search", tools[1].Get("name").String())
	assert.Equal(t, "function", tools[1].Get("type").String())
	assert.Equal(t, "string", gjson.GetBytes(out, `tools.#(name=="web_search").parameters.properties.query.type`).String())
	assert.Equal(t, "image_generation", tools[2].Get("type").String())
}

func TestEmulateResponsesHostedToolsRewritesPinnedChoice(t *testing.T) {
	tests := []struct {
		name       string
		toolType   string
		emulateSet map[string]struct{}
		wantName   string
	}{
		{name: "web search", toolType: "web_search", emulateSet: map[string]struct{}{"web_search": {}}, wantName: emulatedWebSearchFunctionName},
		{name: "web search preview", toolType: "web_search_preview", emulateSet: map[string]struct{}{"web_search": {}}, wantName: emulatedWebSearchFunctionName},
		{name: "image generation", toolType: "image_generation", emulateSet: map[string]struct{}{"image_generation": {}}, wantName: emulatedImageFunctionName},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"model":"m","tools":[{"type":"` + tt.toolType + `"}],"tool_choice":{"type":"` + tt.toolType + `"}}`)
			out := emulateResponsesHostedTools(body, tt.emulateSet, relaycommon.NewResponsesClientToolBridge())
			assert.Equal(t, "function", gjson.GetBytes(out, "tool_choice.type").String())
			assert.Equal(t, tt.wantName, gjson.GetBytes(out, "tool_choice.name").String())
		})
	}
}

func TestEmulateResponsesHostedToolsHistory(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"input": [
			{"type":"message","role":"user","content":[{"type":"input_text","text":"search go news"}]},
			{"type":"web_search_call","id":"ws_1","call_id":"ws_1","status":"completed","action":{"type":"search","query":"go news"}},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"here is the news"}]}
		],
		"tools": [{"type":"web_search"}]
	}`)
	out := emulateResponsesHostedTools(body, emulateSet(), relaycommon.NewResponsesClientToolBridge())
	assert.Equal(t, "function_call", gjson.GetBytes(out, "input.1.type").String())
	assert.Equal(t, "web_search", gjson.GetBytes(out, "input.1.name").String())
	assert.Equal(t, `{"query":"go news"}`, gjson.GetBytes(out, "input.1.arguments").String())
	assert.Equal(t, "function_call_output", gjson.GetBytes(out, "input.2.type").String(), "hosted call must be paired so the upstream never rejects a bare function_call")
	assert.Equal(t, "ws_1", gjson.GetBytes(out, "input.2.call_id").String())
	assert.Equal(t, "message", gjson.GetBytes(out, "input.3.type").String(), "unrelated history items untouched")
}

func TestEmulateResponsesHostedToolsNoChange(t *testing.T) {
	body := []byte(`{"model":"m","tools":[{"type":"web_search"}],"tool_choice":{"type":"web_search"}}`)
	assert.Equal(t, string(body), string(emulateResponsesHostedTools(body, nil, relaycommon.NewResponsesClientToolBridge())))
	assert.Equal(t, string(body), string(emulateResponsesHostedTools(body, map[string]struct{}{"image_generation": {}}, relaycommon.NewResponsesClientToolBridge())))
	noWebSearch := []byte(`{"model":"m","tools":[{"type":"function","name":"shell"}],"tool_choice":{"type":"web_search"}}`)
	assert.Equal(t, string(noWebSearch), string(emulateResponsesHostedTools(noWebSearch, emulateSet(), relaycommon.NewResponsesClientToolBridge())))
}

func TestApplyResponsesPassThroughTransformsComposeSequentially(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{}
	backends := map[string]*dto.EmulatedToolBackend{
		dto.EmulatedToolTypeWebSearch: {Provider: "test"},
	}
	body := []byte(`{
		"model":"m",
		"input":"search",
		"tools":[
			{"type":"computer_use_preview"},
			{"type":"custom","name":"apply_patch"},
			{"type":"web_search_preview"}
		],
		"tool_choice":{"type":"web_search_preview"}
	}`)

	out, changed := applyResponsesPassThroughTransforms(
		testCtx,
		info,
		body,
		map[string]struct{}{"computer_use_preview": {}},
		map[string]struct{}{"custom": {}},
		map[string]struct{}{"web_search": {}},
		backends,
	)

	require.True(t, changed)
	tools := gjson.GetBytes(out, "tools").Array()
	require.Len(t, tools, 2)
	assert.Equal(t, "apply_patch", tools[0].Get("name").String())
	assert.Equal(t, "function", tools[0].Get("type").String(), "bridge must run after strip")
	assert.Equal(t, emulatedWebSearchFunctionName, tools[1].Get("name").String())
	assert.Equal(t, "function", tools[1].Get("type").String(), "emulation must run after bridge")
	assert.Equal(t, "function", gjson.GetBytes(out, "tool_choice.type").String())
	assert.Equal(t, emulatedWebSearchFunctionName, gjson.GetBytes(out, "tool_choice.name").String())
	require.Equal(t, backends, info.EmulatedTools)
	require.NotNil(t, info.ClientToolBridge)
	_, bridged := info.ClientToolBridge.Lookup("apply_patch")
	assert.True(t, bridged)
	_, emulated := info.ClientToolBridge.Lookup(emulatedWebSearchFunctionName)
	assert.True(t, emulated)
}

func TestApplyResponsesPassThroughTransformsBridgeNameCollisionStillBlocksEmulation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{}
	backends := map[string]*dto.EmulatedToolBackend{
		dto.EmulatedToolTypeWebSearch: {Provider: "test"},
	}
	body := []byte(`{"model":"m","tools":[{"type":"function","name":"web_search"},{"type":"web_search"}],"tool_choice":{"type":"web_search"}}`)

	out, changed := applyResponsesPassThroughTransforms(
		testCtx,
		info,
		body,
		nil,
		map[string]struct{}{"custom": {}},
		map[string]struct{}{"web_search": {}},
		backends,
	)

	assert.False(t, changed)
	assert.Equal(t, body, out)
	assert.Nil(t, info.EmulatedTools, "a real function with the synthetic name must prevent emulation")
	require.NotNil(t, info.ClientToolBridge)
	spec, ok := info.ClientToolBridge.Lookup(emulatedWebSearchFunctionName)
	require.True(t, ok)
	assert.Equal(t, relaycommon.ResponsesClientToolFunction, spec.Kind)
}

func TestApplyResponsesPassThroughTransformsNoOpChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{}
	body := []byte(`{"model":"m","input":"hi","tools":[{"type":"web_search"}],"tool_choice":{"type":"web_search"}}`)

	out, changed := applyResponsesPassThroughTransforms(testCtx, info, body, nil, nil, nil, nil)

	assert.False(t, changed)
	assert.Equal(t, body, out)
	assert.Nil(t, info.ClientToolBridge)
	assert.Nil(t, info.EmulatedTools)
}

func TestEmulatedCallsFromCapturedStream(t *testing.T) {
	info := testRelayInfoWithBridge()
	captured := []byte(strings.Join([]string{
		"event: response.created\ndata: " + `{"type":"response.created","response":{"id":"r","output":[]}}`,
		"event: response.output_item.done\ndata: " + `{"type":"response.output_item.done","output_index":0,"item":{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"web_search","arguments":"{\"query\":\"go\"}"}}`,
		"event: response.output_item.done\ndata: " + `{"type":"response.output_item.done","output_index":1,"item":{"id":"fc_2","type":"function_call","status":"completed","call_id":"call_2","name":"shell","arguments":"{}"}}`,
		"event: response.completed\ndata: " + `{"type":"response.completed","response":{"id":"r","output":[{"id":"fc_1","type":"function_call","call_id":"call_1","name":"web_search","arguments":"{\"query\":\"go\"}"}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}}`,
		"", "",
	}, "\n\n"))
	calls := emulatedCallsFromCaptured(captured, info)
	require.Len(t, calls, 1, "shell call ignored, web_search deduped across done+completed")
	assert.Equal(t, "call_1", calls[0].CallID)
	assert.Equal(t, `{"query":"go"}`, calls[0].Arguments)

	reasoning := reasoningItemsFromCaptured([]byte("event: response.output_item.done\ndata: " + `{"type":"response.output_item.done","item":{"id":"rs_1","type":"reasoning","encrypted_content":"abc"}}` + "\n\n"))
	require.Len(t, reasoning, 1)
	assert.Contains(t, string(reasoning[0]), "encrypted_content")
}

func TestAppendInputItemsWrapsString(t *testing.T) {
	body := []byte(`{"model":"m","input":"hello","tools":[{"type":"function","name":"web_search"}]}`)
	item := []byte(`{"type":"function_call","call_id":"c1","name":"web_search","arguments":"{}"}`)
	out, err := appendInputItems(body, [][]byte{item})
	require.NoError(t, err)
	input := gjson.GetBytes(out, "input").Array()
	require.Len(t, input, 2)
	assert.Equal(t, "message", input[0].Get("type").String())
	assert.Equal(t, "hello", input[0].Get("content").String())
	assert.Equal(t, "function_call", input[1].Get("type").String())
}

// runResponsesEmulationLoop end to end against fake adaptor closures and a
// stubbed Gemini backend: round 1 calls web_search, round 2 produces the final
// answer; the client must receive the search item spliced in with shifted
// indexes and the returned usage must be the sum.
func TestRunResponsesEmulationLoopStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	geminiStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"Go 1.24 is the latest release"}]},"groundingMetadata":{"groundingChunks":[{"web":{"uri":"https://go.dev/doc","title":"Go Release Notes"}}]}}]}`))
	}))
	defer geminiStub.Close()

	recorder := httptest.NewRecorder()
	testCtx, _ := gin.CreateTestContext(recorder)
	testCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := testRelayInfoWithBridge()

	round := 0
	var upstreamBodies []string
	doRequest := func(body io.Reader) (any, error) {
		data, _ := io.ReadAll(body)
		upstreamBodies = append(upstreamBodies, string(data))
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(nil))}, nil
	}
	doResponse := func(resp *http.Response) (*dto.Usage, *types.NewAPIError) {
		round++
		if round == 1 {
			writeSSE(testCtx, "response.created", `{"type":"response.created","sequence_number":0,"response":{"id":"r1","status":"in_progress","output":[]}}`)
			writeSSE(testCtx, "response.output_item.added", `{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"fc_1","type":"function_call","status":"in_progress","call_id":"call_1","name":"web_search","arguments":""}}`)
			writeSSE(testCtx, "response.output_item.done", `{"type":"response.output_item.done","sequence_number":2,"output_index":0,"item":{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"web_search","arguments":"{\"query\":\"latest go version\"}"}}`)
			writeSSE(testCtx, "response.completed", `{"type":"response.completed","sequence_number":3,"response":{"id":"r1","status":"completed","output":[{"id":"fc_1","type":"function_call","call_id":"call_1","name":"web_search","arguments":"{\"query\":\"latest go version\"}"}],"usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}}`)
			return &dto.Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14}, nil
		}
		writeSSE(testCtx, "response.created", `{"type":"response.created","sequence_number":0,"response":{"id":"r2","status":"in_progress","output":[]}}`)
		writeSSE(testCtx, "response.output_item.added", `{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"msg_1","type":"message","status":"in_progress","role":"assistant","content":[]}}`)
		writeSSE(testCtx, "response.output_text.delta", `{"type":"response.output_text.delta","sequence_number":2,"output_index":0,"delta":"Go 1.24"}`)
		writeSSE(testCtx, "response.completed", `{"type":"response.completed","sequence_number":3,"response":{"id":"r2","status":"completed","output":[],"usage":{"input_tokens":30,"output_tokens":6,"total_tokens":36}}}`)
		return &dto.Usage{PromptTokens: 30, CompletionTokens: 6, TotalTokens: 36}, nil
	}

	baseBody := []byte(`{"model":"muse-spark-1.2","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"latest go version"}]}],"tools":[{"type":"function","name":"web_search"}]}`)
	usage, apiErr := runResponsesEmulationLoop(testCtx, info, doRequest, func(resp *http.Response) (*dto.Usage, *types.NewAPIError) {
		u, e := doResponse(resp)
		return u, e
	}, baseBody, map[string]*dto.EmulatedToolBackend{
		dto.EmulatedToolTypeWebSearch: {Provider: "gemini", APIKey: "test-key", APIBase: geminiStub.URL},
	})
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 40, usage.PromptTokens, "usage summed across rounds")
	assert.Equal(t, 10, usage.CompletionTokens)

	require.Len(t, upstreamBodies, 2, "one round-trip per search")
	followUp := gjson.GetBytes([]byte(upstreamBodies[1]), "input").Array()
	assert.Equal(t, "function_call", followUp[1].Get("type").String(), "model call echoed back")
	assert.Equal(t, "function_call_output", followUp[2].Get("type").String())
	assert.Contains(t, followUp[2].Get("output").String(), "Go 1.24 is the latest release")
	assert.Contains(t, followUp[2].Get("output").String(), "go.dev/doc")

	client := recorder.Body.String()
	assert.Contains(t, client, `"type":"web_search_call"`, "search item restored for the client")
	assert.Contains(t, client, `"query":"latest go version"`)
	assert.Contains(t, client, "Go 1.24", "final answer forwarded")
	// the terminal completed event must carry the restored search item in its
	// output array too, so clients that rebuild from completed.output see it
	var completedOutput []string
	for _, block := range strings.Split(client, "\n\n") {
		if !strings.Contains(block, "event: response.completed") {
			continue
		}
		for _, l := range strings.Split(block, "\n") {
			if !strings.HasPrefix(l, "data: ") {
				continue
			}
			for _, t := range gjson.Get(strings.TrimPrefix(l, "data: "), "response.output.#.type").Array() {
				completedOutput = append(completedOutput, t.String())
			}
		}
	}
	assert.Equal(t, []string{"web_search_call"}, completedOutput, "restored item present in completed event output")
	// the final message events must be shifted past the spliced search item
	completedIdx := strings.Index(client, "response.completed")
	messageIdx := strings.Index(client, `"type":"message"`)
	assert.Greater(t, messageIdx, -1)
	assert.Greater(t, completedIdx, messageIdx)
}

func TestRunResponsesEmulationLoopNonStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	testCtx, _ := gin.CreateTestContext(recorder)
	testCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := testRelayInfoWithBridge()

	round := 0
	doResponse := func(resp *http.Response) (*dto.Usage, *types.NewAPIError) {
		round++
		if round == 1 {
			_, _ = testCtx.Writer.Write([]byte(`{"id":"r1","status":"completed","output":[{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"web_search","arguments":"{\"query\":\"go\"}"}],"usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}`))
			return &dto.Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14}, nil
		}
		_, _ = testCtx.Writer.Write([]byte(`{"id":"r2","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":8,"output_tokens":2,"total_tokens":10}}`))
		return &dto.Usage{PromptTokens: 8, CompletionTokens: 2, TotalTokens: 10}, nil
	}
	geminiStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"search result text"}]}}]}`))
	}))
	defer geminiStub.Close()

	usage, apiErr := runResponsesEmulationLoop(testCtx, info,
		func(body io.Reader) (any, error) {
			return &http.Response{StatusCode: http.StatusOK}, nil
		},
		doResponse,
		[]byte(`{"model":"m","input":"q","tools":[{"type":"function","name":"web_search"}]}`),
		map[string]*dto.EmulatedToolBackend{
			dto.EmulatedToolTypeWebSearch: {Provider: "gemini", APIKey: "k", APIBase: geminiStub.URL},
		})
	require.Nil(t, apiErr)
	assert.Equal(t, 18, usage.PromptTokens)

	client := recorder.Body.String()
	assert.Contains(t, client, `"type":"web_search_call"`)
	assert.Contains(t, client, `"query":"go"`)
	assert.NotContains(t, client, "search result text", "search output only goes upstream, never to the client")
	assert.Contains(t, client, `"type":"message"`)
}

func writeSSE(c *gin.Context, event string, data string) {
	_, _ = c.Writer.Write([]byte("event: " + event + "\ndata: " + data + "\n\n"))
}

func newTestBridgeWithImage() *relaycommon.ResponsesClientToolBridge {
	bridge := relaycommon.NewResponsesClientToolBridge()
	bridge.Register("image_generation", relaycommon.ResponsesClientToolSpec{Kind: relaycommon.ResponsesClientToolImageGeneration, Name: "image_generation"})
	return bridge
}

func TestEmulateResponsesImageDeclaration(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"input": [{"type":"message","role":"user","content":[{"type":"input_text","text":"draw a cat"}]}],
		"tools": [
			{"type":"web_search"},
			{"type":"image_generation"},
			{"type":"image_generation","size":"1024x1024"}
		]
	}`)
	bridge := relaycommon.NewResponsesClientToolBridge()
	out := emulateResponsesHostedTools(body, map[string]struct{}{"web_search": {}, "image_generation": {}}, bridge)

	tools := gjson.GetBytes(out, "tools").Array()
	require.Len(t, tools, 2, "duplicate image_generation collapses; hosted kinds replaced")
	assert.Equal(t, "function", tools[0].Get("type").String())
	assert.Equal(t, "web_search", tools[0].Get("name").String())
	assert.Equal(t, "function", tools[1].Get("type").String())
	assert.Equal(t, "image_generation", tools[1].Get("name").String())
	assert.Equal(t, "string", gjson.GetBytes(out, `tools.#(name=="image_generation").parameters.properties.prompt.type`).String())
}

func TestEmulateResponsesImageHistoryExpansion(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"input": [
			{"type":"message","role":"user","content":[{"type":"input_text","text":"draw a cat"}]},
			{"type":"image_generation_call","id":"ig_1","status":"completed","action":{"type":"generate","prompt":"a cat"},"result":"AAAA"},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"here you go"}]}
		],
		"tools": [{"type":"image_generation"}]
	}`)
	out := emulateResponsesHostedTools(body, map[string]struct{}{"image_generation": {}}, relaycommon.NewResponsesClientToolBridge())
	input := gjson.GetBytes(out, "input").Array()
	require.Len(t, input, 4, "image call expands into function_call + function_call_output")
	assert.Equal(t, "function_call", input[1].Get("type").String())
	assert.Equal(t, "image_generation", input[1].Get("name").String())
	assert.Contains(t, input[1].Get("arguments").String(), `"prompt":"a cat"`)
	assert.Equal(t, "function_call_output", input[2].Get("type").String())
	assert.Equal(t, "ig_1", input[2].Get("call_id").String())
	assert.Equal(t, "message", input[3].Get("type").String(), "following items preserved")
}

func TestEmulatedCallsFromCapturedImage(t *testing.T) {
	info := &relaycommon.RelayInfo{ClientToolBridge: newTestBridgeWithImage()}
	captured := []byte(`{"id":"r","output":[
		{"id":"fc_1","type":"function_call","call_id":"call_1","name":"image_generation","arguments":"{\"prompt\":\"a cat\",\"size\":\"1024x1024\"}"},
		{"id":"ig_2","type":"image_generation_call","call_id":"ig_2","status":"completed","action":{"type":"generate","prompt":"a dog"},"result":"BBBB"}
	]}`)
	calls := emulatedCallsFromCaptured(captured, info)
	require.Len(t, calls, 2)
	assert.Equal(t, relaycommon.ResponsesClientToolImageGeneration, calls[0].Kind)
	assert.Equal(t, "a cat", emulatedImagePrompt(calls[0].Arguments))
	assert.Equal(t, relaycommon.ResponsesClientToolImageGeneration, calls[1].Kind)
	assert.Equal(t, "a dog", emulatedImagePrompt(calls[1].Arguments))
}

// image emulation end to end: round 1 calls image_generation, round 2 answers.
// The base64 payload must never travel upstream, and the client must receive
// a native image_generation_call item carrying it.
func TestEmulateResponsesHostedToolsInjectsRecognition(t *testing.T) {
	// no tools at all: the injected image_recognition function is added so
	// text-only upstreams can still call a vision backend
	body := []byte(`{"model":"m","input":"look at my cat photo"}`)
	bridge := relaycommon.NewResponsesClientToolBridge()
	out := emulateResponsesHostedTools(body, map[string]struct{}{"image_recognition": {}}, bridge)
	tools := gjson.GetBytes(out, "tools").Array()
	require.Len(t, tools, 1, "recognition tool injected")
	assert.Equal(t, "function", tools[0].Get("type").String())
	assert.Equal(t, "image_recognition", tools[0].Get("name").String())
	assert.True(t, gjson.GetBytes(out, `tools.#(name=="image_recognition").parameters.properties.question`).Exists())

	body = []byte(`{"model":"m","input":"x","tools":[{"type":"function","name":"shell"}]}`)
	out = emulateResponsesHostedTools(body, map[string]struct{}{"image_recognition": {}}, relaycommon.NewResponsesClientToolBridge())
	tools = gjson.GetBytes(out, "tools").Array()
	require.Len(t, tools, 2)
	assert.Equal(t, "shell", tools[0].Get("name").String())

	assert.Equal(t, string(body), string(emulateResponsesHostedTools(body, nil, relaycommon.NewResponsesClientToolBridge())), "no emulation -> byte-identical")
}

func TestEmulateResponsesRecognitionHistoryExpansion(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"input": [
			{"type":"message","role":"user","content":[{"type":"input_text","text":"analyze"}]},
			{"type":"image_recognition_call","id":"ir_1","status":"completed","action":{"type":"recognize","question":"what animal"},"summary":"a cat"},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}
		],
		"tools": [{"type":"function","name":"image_recognition"}]
	}`)
	out := emulateResponsesHostedTools(body, map[string]struct{}{"image_recognition": {}}, relaycommon.NewResponsesClientToolBridge())
	input := gjson.GetBytes(out, "input").Array()
	require.Len(t, input, 4, "recognition call expands into function_call + function_call_output")
	assert.Equal(t, "function_call", input[1].Get("type").String())
	assert.Equal(t, "image_recognition", input[1].Get("name").String())
	assert.Contains(t, input[1].Get("arguments").String(), `"question":"what animal"`)
	assert.Equal(t, "function_call_output", input[2].Get("type").String())
	assert.Equal(t, "message", input[3].Get("type").String())
}

func TestEmulatedCallsFromCapturedRecognition(t *testing.T) {
	info := &relaycommon.RelayInfo{ClientToolBridge: newTestBridgeWithRecognition()}
	captured := []byte(`{"id":"r","output":[
		{"id":"fc_1","type":"function_call","call_id":"call_1","name":"image_recognition","arguments":"{\"question\":\"what is it\"}"},
		{"id":"ir_2","type":"image_recognition_call","call_id":"ir_2","status":"completed","action":{"type":"recognize","question":"how tall"},"summary":"10m"}
	]}`)
	calls := emulatedCallsFromCaptured(captured, info)
	require.Len(t, calls, 2)
	assert.Equal(t, relaycommon.ResponsesClientToolImageRecognition, calls[0].Kind)
	assert.Equal(t, relaycommon.ResponsesClientToolImageRecognition, calls[1].Kind)
	assert.Contains(t, calls[1].Arguments, "how tall")
}

func newTestBridgeWithRecognition() *relaycommon.ResponsesClientToolBridge {
	bridge := relaycommon.NewResponsesClientToolBridge()
	bridge.Register("image_recognition", relaycommon.ResponsesClientToolSpec{Kind: relaycommon.ResponsesClientToolImageRecognition, Name: "image_recognition"})
	return bridge
}

// recognition emulation end to end: the upstream calls the injected
// image_recognition function, the gateway runs the vision backend (openai
// vision stub), and the client receives a restored image_recognition_call.
func TestRunResponsesEmulationLoopRecognition(t *testing.T) {
	gin.SetMode(gin.TestMode)
	visionStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"It is a fluffy cat."}}]}`))
	}))
	defer visionStub.Close()

	recorder := httptest.NewRecorder()
	testCtx, _ := gin.CreateTestContext(recorder)
	testCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{ClientToolBridge: newTestBridgeWithRecognition()}

	round := 0
	var upstreamBodies []string
	doResponse := func(resp *http.Response) (*dto.Usage, *types.NewAPIError) {
		round++
		if round == 1 {
			_, _ = testCtx.Writer.Write([]byte(`{"id":"r1","status":"completed","output":[{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"image_recognition","arguments":"{\"question\":\"what animal\"}"}],"usage":{"input_tokens":8,"output_tokens":3,"total_tokens":11}}`))
			return &dto.Usage{PromptTokens: 8, CompletionTokens: 3, TotalTokens: 11}, nil
		}
		_, _ = testCtx.Writer.Write([]byte(`{"id":"r2","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"it is a cat"}]}],"usage":{"input_tokens":6,"output_tokens":2,"total_tokens":8}}`))
		return &dto.Usage{PromptTokens: 6, CompletionTokens: 2, TotalTokens: 8}, nil
	}

	backends := map[string]*dto.EmulatedToolBackend{
		dto.EmulatedToolTypeRecognition: {Provider: dto.EmulatedRecognitionProviderOpenAI, APIKey: "vk", APIBase: visionStub.URL},
	}
	baseBody := []byte(`{"model":"m","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"what animal is in the photo"},{"type":"input_image","image_url":"https://example.com/cat.png"}]}],"tools":[{"type":"function","name":"image_recognition"}]}`)
	usage, apiErr := runResponsesEmulationLoop(testCtx, info,
		func(body io.Reader) (any, error) {
			data, _ := io.ReadAll(body)
			upstreamBodies = append(upstreamBodies, string(data))
			return &http.Response{StatusCode: http.StatusOK}, nil
		},
		doResponse,
		baseBody, backends)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 14, usage.PromptTokens, "usage summed across rounds")

	require.Len(t, upstreamBodies, 2)
	followUp := gjson.GetBytes([]byte(upstreamBodies[1]), "input").Array()
	output := followUp[2].Get("output").String()
	assert.Contains(t, output, "fluffy cat", "vision result fed back upstream")

	client := recorder.Body.String()
	assert.Contains(t, client, `"type":"image_recognition_call"`, "native recognition item restored")
	assert.Contains(t, client, `"summary":"It is a fluffy cat."`)
	assert.Contains(t, client, `"question":"what animal"`)
	assert.Contains(t, client, "it is a cat", "final answer forwarded")
}

func TestRunResponsesEmulationLoopImage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const imagePayload = "aWNvbi1pbWFnZS1ieXRlcw=="
	imageStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/images/generations", r.URL.Path)
		assert.Equal(t, "Bearer img-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + imagePayload + `","revised_prompt":"a fluffy cat"}]}`))
	}))
	defer imageStub.Close()

	recorder := httptest.NewRecorder()
	testCtx, _ := gin.CreateTestContext(recorder)
	testCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{ClientToolBridge: newTestBridgeWithImage()}

	round := 0
	var upstreamBodies []string
	doResponse := func(resp *http.Response) (*dto.Usage, *types.NewAPIError) {
		round++
		if round == 1 {
			_, _ = testCtx.Writer.Write([]byte(`{"id":"r1","status":"completed","output":[{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"image_generation","arguments":"{\"prompt\":\"a cat\",\"size\":\"1024x1024\"}"}],"usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}`))
			return &dto.Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14}, nil
		}
		_, _ = testCtx.Writer.Write([]byte(`{"id":"r2","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"cat drawn"}]}],"usage":{"input_tokens":8,"output_tokens":2,"total_tokens":10}}`))
		return &dto.Usage{PromptTokens: 8, CompletionTokens: 2, TotalTokens: 10}, nil
	}

	usage, apiErr := runResponsesEmulationLoop(testCtx, info,
		func(body io.Reader) (any, error) {
			data, _ := io.ReadAll(body)
			upstreamBodies = append(upstreamBodies, string(data))
			return &http.Response{StatusCode: http.StatusOK}, nil
		},
		doResponse,
		[]byte(`{"model":"m","input":"draw a cat","tools":[{"type":"function","name":"image_generation"}]}`),
		map[string]*dto.EmulatedToolBackend{
			dto.EmulatedToolTypeImage: {Provider: "openai_images", APIKey: "img-key", Model: "gpt-image-1", APIBase: imageStub.URL},
		})
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 18, usage.PromptTokens, "usage summed across rounds")

	require.Len(t, upstreamBodies, 2)
	followUp := gjson.GetBytes([]byte(upstreamBodies[1]), "input").Array()
	output := followUp[2].Get("output").String()
	assert.Contains(t, output, "Image generated", "model sees a text marker")
	assert.NotContains(t, followUp[2].Get("output").String(), imagePayload, "image bytes never travel upstream")

	client := recorder.Body.String()
	assert.Contains(t, client, `"type":"image_generation_call"`, "native image item restored")
	assert.Contains(t, client, `"result":"`+imagePayload+`"`)
	assert.Contains(t, client, `"prompt":"a cat"`)
	assert.Contains(t, client, "cat drawn", "final answer forwarded")
}

func TestEmulateResponsesHostedToolsStripsInputImages(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"input": [
			{"type":"input_image","image_url":"https://cdn.example/a.png"},
			{"type":"message","role":"user","content":[
				{"type":"input_text","text":"what is this"},
				{"type":"input_image","image_url":{"url":"https://cdn.example/b.png"},"detail":"high"}
			]}
		],
		"tools": [{"type":"function","name":"shell","parameters":{"type":"object"}}]
	}`)
	bridge := relaycommon.NewResponsesClientToolBridge()
	out := emulateResponsesHostedTools(body, map[string]struct{}{"image_recognition": {}}, bridge)

	assert.NotContains(t, string(out), "input_image", "image parts never reach the upstream")
	assert.NotContains(t, string(out), "cdn.example", "image URLs stripped along with the parts")
	assert.Contains(t, string(out), "Image 2 of 2 attached but not visible", "marker names the attachment reference")
	assert.Contains(t, string(out), `attachment://1`, "first image gets a reference")
	assert.Equal(t, "https://cdn.example/b.png", bridge.LastAttachedImageURL(), "most recent image kept for the executor")
	assert.Equal(t, "https://cdn.example/a.png", bridge.ResolveAttachmentURL("attachment://1"), "references resolve to the originals")
	assert.Equal(t, "https://cdn.example/b.png", bridge.ResolveAttachmentURL("attachment://2"), "object-form image_url registered too")
	assert.Equal(t, "function", gjson.GetBytes(out, `tools.#(name=="image_recognition").type`).String(), "recognition tool injected")
	assert.Equal(t, "input_text", gjson.GetBytes(out, "input.0.content.0.type").String(), "bare input_image item becomes a message")
}

func TestEmulateResponsesHostedToolsKeepsImagesWithoutRecognitionEmulation(t *testing.T) {
	body := []byte(`{"model":"m","input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"https://cdn.example/a.png"}]}]}`)
	out := emulateResponsesHostedTools(body, map[string]struct{}{"web_search": {}}, relaycommon.NewResponsesClientToolBridge())
	assert.Contains(t, string(out), "input_image", "no recognition emulation: images pass through untouched")
}

func TestEmulatedCallsFromCapturedJSONWithDataURLArguments(t *testing.T) {
	// a non-stream JSON body whose tool-call arguments embed a data: URL must
	// not be mistaken for an SSE stream (which would drop the call and break
	// the emulation loop)
	info := testRelayInfoWithBridge()
	info.ClientToolBridge.Register(emulatedRecognitionFunctionName, relaycommon.ResponsesClientToolSpec{Kind: relaycommon.ResponsesClientToolImageRecognition, Name: emulatedRecognitionFunctionName})
	captured := []byte(`{"output":[{"type":"reasoning"},{"type":"function_call","name":"image_recognition","call_id":"c1","arguments":"{\"image_url\":\"data:image/png;base64,iVBOR\"}"}]}`)
	calls := emulatedCallsFromCaptured(captured, info)
	require.Len(t, calls, 1)
	assert.Equal(t, emulatedRecognitionFunctionName, calls[0].Name)
	assert.False(t, isLikelySSE(captured))
	assert.True(t, isLikelySSE([]byte("event: x\ndata: {\"type\":\"response.completed\"}\n\n")), "real SSE still detected")
}
