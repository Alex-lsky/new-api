package openai

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func testBridge() *relaycommon.ResponsesClientToolBridge {
	bridge := relaycommon.NewResponsesClientToolBridge()
	bridge.Register("apply_patch", relaycommon.ResponsesClientToolSpec{Kind: relaycommon.ResponsesClientToolCustom, Name: "apply_patch"})
	bridge.Register("mcp__search", relaycommon.ResponsesClientToolSpec{Kind: relaycommon.ResponsesClientToolNamespace, Name: "search", Namespace: "mcp"})
	bridge.Register("tool_search", relaycommon.ResponsesClientToolSpec{Kind: relaycommon.ResponsesClientToolSearch, Name: "tool_search"})
	bridge.Register("web_search", relaycommon.ResponsesClientToolSpec{Kind: relaycommon.ResponsesClientToolWebSearch, Name: "web_search"})
	return bridge
}

func TestRestoreBridgedWebSearchCall(t *testing.T) {
	body := []byte(`{"output":[{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"web_search","arguments":"{\"query\":\"go 1.24 release date\"}"}]}`)
	out := restoreBridgedResponsesOutput(body, testBridge())
	assert.Equal(t, "web_search_call", gjson.GetBytes(out, "output.0.type").String())
	assert.Equal(t, "go 1.24 release date", gjson.GetBytes(out, "output.0.action.query").String())
	assert.Equal(t, "search", gjson.GetBytes(out, "output.0.action.type").String())
	assert.False(t, gjson.GetBytes(out, "output.0.arguments").Exists())

	s := newResponsesStreamToolBridge(testBridge())
	added, _, forward := s.transformEvent([]byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","status":"in_progress","call_id":"call_1","name":"web_search"}}`))
	require.True(t, forward)
	assert.Equal(t, "web_search_call", gjson.GetBytes(added, "item.type").String())

	_, _, forward = s.transformEvent([]byte(`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"{\"query\""}`))
	assert.False(t, forward, "web_search argument deltas are suppressed like custom tools")
}

func TestRestoreBridgedResponsesOutputNonStream(t *testing.T) {
	body := []byte(`{
		"id": "resp_1",
		"status": "completed",
		"model": "muse-spark-1.2",
		"service_tier": "default",
		"output": [
			{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"apply_patch","arguments":"{\"input\":\"*** Begin Patch\"}"},
			{"id":"fc_2","type":"function_call","status":"completed","call_id":"call_2","name":"mcp__search","arguments":"{\"q\":\"go\"}"},
			{"id":"fc_3","type":"function_call","status":"completed","call_id":"call_3","name":"tool_search","arguments":"{\"query\":\"docs\"}"},
			{"id":"fc_4","type":"function_call","status":"completed","call_id":"call_4","name":"shell","arguments":"{\"command\":[\"ls\"]}"},
			{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"done"}]}
		],
		"usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}
	}`)
	out := restoreBridgedResponsesOutput(body, testBridge())

	assert.Equal(t, "custom_tool_call", gjson.GetBytes(out, "output.0.type").String())
	assert.Equal(t, "*** Begin Patch", gjson.GetBytes(out, "output.0.input").String())
	assert.False(t, gjson.GetBytes(out, "output.0.arguments").Exists(), "custom_tool_call carries input, not arguments")
	assert.Equal(t, "fc_1", gjson.GetBytes(out, "output.0.id").String(), "ids stay stable so streamed items keep matching")

	assert.Equal(t, "function_call", gjson.GetBytes(out, "output.1.type").String())
	assert.Equal(t, "search", gjson.GetBytes(out, "output.1.name").String())
	assert.Equal(t, "mcp", gjson.GetBytes(out, "output.1.namespace").String())

	assert.Equal(t, "tool_search_call", gjson.GetBytes(out, "output.2.type").String())
	assert.Equal(t, "client", gjson.GetBytes(out, "output.2.execution").String())

	assert.Equal(t, "function_call", gjson.GetBytes(out, "output.3.type").String(), "unbridged calls pass through")
	assert.Equal(t, "shell", gjson.GetBytes(out, "output.3.name").String())
	assert.Equal(t, "message", gjson.GetBytes(out, "output.4.type").String())
	assert.Equal(t, "default", gjson.GetBytes(out, "service_tier").String(), "unknown fields survive the rewrite")
}

func TestRestoreBridgedResponsesOutputNothingToRestore(t *testing.T) {
	body := []byte(`{"output":[{"type":"function_call","name":"shell","arguments":"{}"}]}`)
	assert.Equal(t, string(body), string(restoreBridgedResponsesOutput(body, testBridge())))
}

func TestStreamToolBridgeCustomToolEvents(t *testing.T) {
	s := newResponsesStreamToolBridge(testBridge())

	// 1. output_item.added for a bridged custom tool
	added := []byte(`{"type":"response.output_item.added","output_index":1,"item":{"id":"fc_1","type":"function_call","status":"in_progress","call_id":"call_1","name":"apply_patch","arguments":""}}`)
	out, pre, forward := s.transformEvent(added)
	require.True(t, forward)
	assert.Empty(t, pre)
	assert.Equal(t, "custom_tool_call", gjson.GetBytes(out, "item.type").String())
	assert.Equal(t, "fc_1", gjson.GetBytes(out, "item.id").String())

	// 2. argument deltas are buffered, not forwarded
	delta := []byte(`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":1,"delta":"{\"input\":\"*** Begin"}`)
	_, _, forward = s.transformEvent(delta)
	assert.False(t, forward, "custom tool argument deltas must be suppressed")

	// 3. arguments.done is suppressed too
	doneArgs := []byte(`{"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":1,"arguments":"{\"input\":\"*** Begin Patch\"}"}`)
	_, _, forward = s.transformEvent(doneArgs)
	assert.False(t, forward)

	// 4. output_item.done emits the input events first, then the restored item
	itemDone := []byte(`{"type":"response.output_item.done","output_index":1,"item":{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"apply_patch","arguments":"{\"input\":\"*** Begin Patch\"}"}}`)
	out, pre, forward = s.transformEvent(itemDone)
	require.True(t, forward)
	require.Len(t, pre, 2, "custom_tool_call_input.delta + done precede the item done event")
	assert.Equal(t, "response.custom_tool_call_input.delta", gjson.GetBytes(pre[0], "type").String())
	assert.Equal(t, "*** Begin Patch", gjson.GetBytes(pre[0], "delta").String())
	assert.Equal(t, "fc_1", gjson.GetBytes(pre[0], "item_id").String())
	assert.Equal(t, "response.custom_tool_call_input.done", gjson.GetBytes(pre[1], "type").String())
	assert.Equal(t, "*** Begin Patch", gjson.GetBytes(pre[1], "input").String())
	assert.Equal(t, "custom_tool_call", gjson.GetBytes(out, "item.type").String())
	assert.Equal(t, "*** Begin Patch", gjson.GetBytes(out, "item.input").String())
	assert.False(t, gjson.GetBytes(out, "item.arguments").Exists())

	// 5. completed event's embedded output is restored as well
	completed := []byte(`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"id":"fc_1","type":"function_call","name":"apply_patch","call_id":"call_1","arguments":"{\"input\":\"*** Begin Patch\"}"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)
	out, pre, forward = s.transformEvent(completed)
	require.True(t, forward)
	assert.Empty(t, pre)
	assert.Equal(t, "custom_tool_call", gjson.GetBytes(out, "response.output.0.type").String())
	assert.Equal(t, "*** Begin Patch", gjson.GetBytes(out, "response.output.0.input").String())
}

func TestStreamToolBridgeNamespaceAndSearch(t *testing.T) {
	s := newResponsesStreamToolBridge(testBridge())

	added := []byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_9","type":"function_call","status":"in_progress","call_id":"call_9","name":"mcp__search","arguments":""}}`)
	out, _, forward := s.transformEvent(added)
	require.True(t, forward)
	assert.Equal(t, "function_call", gjson.GetBytes(out, "item.type").String())
	assert.Equal(t, "search", gjson.GetBytes(out, "item.name").String())
	assert.Equal(t, "mcp", gjson.GetBytes(out, "item.namespace").String())

	// namespace tools keep streaming ordinary argument events
	delta := []byte(`{"type":"response.function_call_arguments.delta","item_id":"fc_9","output_index":0,"delta":"{\"q\":"}`)
	outDelta, _, forward := s.transformEvent(delta)
	require.True(t, forward)
	assert.Equal(t, string(delta), string(outDelta), "namespace argument deltas pass through unchanged")

	searchDone := []byte(`{"type":"response.output_item.done","output_index":2,"item":{"id":"fc_10","type":"function_call","status":"completed","call_id":"call_10","name":"tool_search","arguments":"{\"query\":\"docs\"}"}}`)
	out, pre, forward := s.transformEvent(searchDone)
	require.True(t, forward)
	assert.Empty(t, pre, "tool_search keeps its arguments; no input events are synthesized")
	assert.Equal(t, "tool_search_call", gjson.GetBytes(out, "item.type").String())
	assert.Equal(t, "client", gjson.GetBytes(out, "item.execution").String())
	assert.Equal(t, `{"query":"docs"}`, gjson.GetBytes(out, "item.arguments").String())
}

func TestStreamToolBridgeUnrelatedEventsUntouched(t *testing.T) {
	s := newResponsesStreamToolBridge(testBridge())
	text := []byte(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"delta":"hello"}`)
	out, pre, forward := s.transformEvent(text)
	require.True(t, forward)
	assert.Empty(t, pre)
	assert.Equal(t, string(text), string(out))
}
