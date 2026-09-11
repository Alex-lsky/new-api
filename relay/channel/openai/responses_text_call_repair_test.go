package openai

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/gin-gonic/gin"
)

// realTextualCall is a byte-for-byte sample of a muse-spark textualized
// exec_command call observed in production (2026-08-26).
const realTextualCall = `[tool call] exec_command({"cmd":"python3 - <<'PY'\nimport subprocess\n\ndef sh(cmd):\n    try: return subprocess.check_output(cmd, shell=True, text=True)\n    except: return \"\"\n\nprint(sh(\"du -sh ~/Library\"))\n\nPY","max_output_tokens":12000,"workdir":"/Users/liuhaotian/Documents/Codex/2026-08-25/wo","yield_time_ms":30000})`

func testRepairToolNames() map[string]struct{} {
	return map[string]struct{}{
		"exec_command": {},
		"apply_patch":  {},
		"web_search":   {},
	}
}

func testRepairContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	return c
}

func TestParseTextualToolCall(t *testing.T) {
	name, arguments, ok := parseTextualToolCall(realTextualCall)
	require.True(t, ok)
	assert.Equal(t, "exec_command", name)
	assert.JSONEq(t, `{"cmd":"python3 - <<'PY'\nimport subprocess\n\ndef sh(cmd):\n    try: return subprocess.check_output(cmd, shell=True, text=True)\n    except: return \"\"\n\nprint(sh(\"du -sh ~/Library\"))\n\nPY","max_output_tokens":12000,"workdir":"/Users/liuhaotian/Documents/Codex/2026-08-25/wo","yield_time_ms":30000}`, arguments)

	cases := []struct {
		name string
		text string
		ok   bool
	}{
		{"leading whitespace", "  \n [tool call] exec_command({})", true},
		{"empty args object", "[tool call] update_plan({})", true},
		{"nested braces and parens in strings", `[tool call] apply_patch({"input":"a } b ) c {\"x\"}"})`, true},
		{"prose only", "Here is my final answer.", false},
		{"prose before the call", "Running it now.\n[tool call] exec_command({})", false},
		{"prose after the call", "[tool call] exec_command({}) and then report", false},
		{"missing closing paren", "[tool call] exec_command({}", false},
		{"truncated json", "[tool call] exec_command({\"cmd\": ", false},
		{"non-object payload", "[tool call] exec_command([1,2])", false},
		{"no name", "[tool call] ({})", false},
		{"invalid name characters", "[tool call] not a name({})", false},
		{"no prefix marker", "exec_command({})", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, ok := parseTextualToolCall(tc.text)
			assert.Equal(t, tc.ok, ok)
		})
	}
}

func TestRepairTextualToolCallsBody(t *testing.T) {
	tools := testRepairToolNames()

	t.Run("replaces textual call message", func(t *testing.T) {
		body := []byte(`{"id":"resp_1","status":"completed","output":[` +
			`{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":` + mustMarshalString(t, realTextualCall) + `}]}` +
			`],"usage":{"input_tokens":10,"output_tokens":5}}`)
		out := repairTextualToolCallsBody(body, tools, testRepairContext())
		assert.Equal(t, "function_call", gjson.GetBytes(out, "output.0.type").String())
		assert.Equal(t, "exec_command", gjson.GetBytes(out, "output.0.name").String())
		assert.NotEmpty(t, gjson.GetBytes(out, "output.0.call_id").String())
		assert.Equal(t, "completed", gjson.GetBytes(out, "output.0.status").String())
		expectedArgs := realTextualCall[strings.Index(realTextualCall, "{") : strings.LastIndex(realTextualCall, "}")+1]
		assert.Equal(t, expectedArgs, gjson.GetBytes(out, "output.0.arguments").String(), "arguments is the raw JSON text")
		assert.False(t, gjson.GetBytes(out, "output.0.content").Exists(), "message-only fields are dropped")
		assert.Equal(t, int64(10), gjson.GetBytes(out, "usage.input_tokens").Int(), "usage untouched")
	})

	t.Run("leaves ordinary messages and undeclared tools unchanged", func(t *testing.T) {
		body := []byte(`{"output":[` +
			`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"最终答案：512G 足够。"}]},` +
			`{"id":"msg_2","type":"message","role":"assistant","content":[{"type":"output_text","text":"[tool call] unknown_tool({})"}]}` +
			`]}`)
		assert.Equal(t, string(body), string(repairTextualToolCallsBody(body, tools, testRepairContext())))
	})

	t.Run("repair feeds the bridge restore", func(t *testing.T) {
		// custom tool textualized as the bridged function name: repair first,
		// then the bridge restore turns it back into custom_tool_call
		body := []byte(`{"output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"[tool call] apply_patch({\"input\":\"*** Begin Patch\"})"}]}]}`)
		repaired := repairTextualToolCallsBody(body, tools, testRepairContext())
		restored := restoreBridgedResponsesOutput(repaired, testBridge())
		assert.Equal(t, "custom_tool_call", gjson.GetBytes(restored, "output.0.type").String())
		assert.Equal(t, "*** Begin Patch", gjson.GetBytes(restored, "output.0.input").String())
	})
}

func mustMarshalString(t *testing.T, s string) string {
	data, err := common.Marshal(s)
	require.NoError(t, err)
	return string(data)
}

// streamRepairHarness drives the stream state machine exactly the way the
// handler glue does: pre-events re-enter the state machine and everything
// forwarded lands in one ordered client-visible stream.
type streamRepairHarness struct {
	t       *testing.T
	repair  *responsesStreamTextRepair
	emitted []string
}

func newStreamRepairHarness(t *testing.T, toolNames map[string]struct{}) *streamRepairHarness {
	return &streamRepairHarness{t: t, repair: newResponsesStreamTextRepair(toolNames)}
}

func (h *streamRepairHarness) send(data string) {
	out, pre, forward := h.repair.processEvent([]byte(data), testRepairContext())
	for _, p := range pre {
		h.send(string(p))
	}
	if forward {
		h.emitted = append(h.emitted, string(out))
	}
}

func (h *streamRepairHarness) emittedTypes() []string {
	types := make([]string, 0, len(h.emitted))
	for _, data := range h.emitted {
		types = append(types, gjson.Get(data, "type").String())
	}
	return types
}

func TestStreamTextRepairNativeSequence(t *testing.T) {
	h := newStreamRepairHarness(t, testRepairToolNames())
	// native event sequence for a message whose text is a textual call
	h.send(`{"type":"response.created","response":{"id":"resp_1"}}`)
	h.send(`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","status":"in_progress","role":"assistant","content":[]}}`)
	h.send(`{"type":"response.content_part.added","item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`)
	h.send(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"[tool call] exec_"}`)
	h.send(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"command({\"cmd\":\"ls\"})"}`)
	h.send(`{"type":"response.output_text.done","item_id":"msg_1","output_index":0,"content_index":0,"text":"[tool call] exec_command({\"cmd\":\"ls\"})"}`)
	h.send(`{"type":"response.content_part.done","item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"[tool call] exec_command({\"cmd\":\"ls\"})"}}`)
	h.send(`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"[tool call] exec_command({\"cmd\":\"ls\"})"}]}}`)

	// the message lifecycle never reaches the client; the function_call
	// sequence replaces it in order
	assert.Equal(t, []string{
		"response.created",
		"response.output_item.added",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.output_item.done",
	}, h.emittedTypes())
	assert.Equal(t, "function_call", gjson.Get(h.emitted[1], "item.type").String())
	assert.Equal(t, "exec_command", gjson.Get(h.emitted[1], "item.name").String())
	assert.Equal(t, `{"cmd":"ls"}`, gjson.Get(h.emitted[2], "delta").String())
	assert.Equal(t, "completed", gjson.Get(h.emitted[4], "item.status").String())
	callID := gjson.Get(h.emitted[1], "item.call_id").String()
	assert.NotEmpty(t, callID)
	assert.Equal(t, callID, gjson.Get(h.emitted[4], "item.call_id").String(), "added/done carry the same call_id")

	completed := `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"[tool call] exec_command({\"cmd\":\"ls\"})"}]}],"usage":{"input_tokens":10,"output_tokens":5}}}`
	h.send(completed)
	assert.Equal(t, "response.completed", h.emittedTypes()[len(h.emittedTypes())-1])
	completedIdx := len(h.emitted) - 1
	assert.Equal(t, "function_call", gjson.Get(h.emitted[completedIdx], "response.output.0.type").String(), "completed output array is rewritten to match")
	assert.Equal(t, "exec_command", gjson.Get(h.emitted[completedIdx], "response.output.0.name").String())
}

func TestStreamTextRepairNormalTextPassesThrough(t *testing.T) {
	h := newStreamRepairHarness(t, testRepairToolNames())
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","status":"in_progress","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"512G 足"}`,
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"够用。"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"512G 够用。"}]}}`,
	}
	for _, e := range events {
		h.send(e)
	}
	// the first delta releases the hold: everything flows on, in order and
	// byte-identical
	assert.Equal(t, []string{
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
		"response.output_text.delta",
		"response.output_item.done",
	}, h.emittedTypes())
	for i, e := range events {
		assert.JSONEq(t, e, h.emitted[i])
	}
}

func TestStreamTextRepairMinimalZenSequence(t *testing.T) {
	h := newStreamRepairHarness(t, testRepairToolNames())
	// zen/go variant: no added/part/done lifecycle events, only deltas and
	// completed — the repair must still fire from the completed output
	h.send(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"[tool call] web_"}`)
	h.send(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"search({\"query\":\"go 1.27\"})"}`)
	h.send(`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"[tool call] web_search({\"query\":\"go 1.27\"})"}]}],"usage":{"input_tokens":9,"output_tokens":2}}}`)

	assert.Equal(t, []string{
		"response.output_item.added",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.output_item.done",
		"response.completed",
	}, h.emittedTypes())
	assert.Equal(t, "web_search", gjson.Get(h.emitted[4], "response.output.0.name").String())
	assert.Equal(t, "function_call", gjson.Get(h.emitted[4], "response.output.0.type").String())
	assert.Equal(t, `{"query":"go 1.27"}`, gjson.Get(h.emitted[4], "response.output.0.arguments").String())
}

func TestStreamTextRepairFailedStreamFlushes(t *testing.T) {
	h := newStreamRepairHarness(t, testRepairToolNames())
	added := `{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","status":"in_progress","role":"assistant","content":[]}}`
	h.send(added)
	h.send(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"[tool ca"}`)
	h.send(`{"type":"response.failed","response":{"id":"resp_1","status":"failed","output":[],"error":{"message":"boom"}}}`)

	// held events come back out; nothing is repaired on a failed response
	assert.Equal(t, []string{
		"response.output_item.added",
		"response.output_text.delta",
		"response.failed",
	}, h.emittedTypes())
}
