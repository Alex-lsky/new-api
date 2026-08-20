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
	assert.Equal(t, "message", gjson.GetBytes(out, "input.2.type").String(), "unrelated history items untouched")
}

func TestEmulateResponsesHostedToolsNoChange(t *testing.T) {
	body := []byte(`{"model":"m","tools":[{"type":"web_search"}]}`)
	assert.Equal(t, string(body), string(emulateResponsesHostedTools(body, nil, relaycommon.NewResponsesClientToolBridge())))
	assert.Equal(t, string(body), string(emulateResponsesHostedTools(body, map[string]struct{}{"image_generation": {}}, relaycommon.NewResponsesClientToolBridge())))
	noWebSearch := []byte(`{"model":"m","tools":[{"type":"function","name":"shell"}]}`)
	assert.Equal(t, string(noWebSearch), string(emulateResponsesHostedTools(noWebSearch, emulateSet(), relaycommon.NewResponsesClientToolBridge())))
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

	reasoning := reasoningItemsFromCaptured([]byte("event: response.output_item.done\ndata: "+`{"type":"response.output_item.done","item":{"id":"rs_1","type":"reasoning","encrypted_content":"abc"}}`+"\n\n"))
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
	}, baseBody, &dto.EmulatedToolBackend{Provider: "gemini", APIKey: "test-key", APIBase: geminiStub.URL})
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
		&dto.EmulatedToolBackend{Provider: "gemini", APIKey: "k", APIBase: geminiStub.URL})
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
