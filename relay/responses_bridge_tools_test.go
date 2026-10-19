package relay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func bridgeAllKinds() map[string]struct{} {
	return map[string]struct{}{"custom": {}, "namespace": {}, "tool_search": {}}
}

func TestBridgeResponsesClientToolsCodexRequest(t *testing.T) {
	body := []byte(`{
		"model": "muse-spark-1.2",
		"input": [
			{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the bug"}]},
			{"type":"custom_tool_call","call_id":"call_1","name":"apply_patch","input":"*** Begin Patch\n+hello"},
			{"type":"custom_tool_call_output","call_id":"call_1","output":"patch applied"},
			{"type":"function_call","call_id":"call_2","name":"search","namespace":"mcp","arguments":"{\"q\":\"go\"}"},
			{"type":"function_call_output","call_id":"call_2","output":"found"}
		],
		"tools": [
			{"type":"function","name":"shell","description":"Run a command","parameters":{"type":"object"}},
			{"type":"custom","name":"apply_patch","description":"Apply a patch to files","input_schema":{"type":"object"}},
			{"type":"namespace","name":"mcp","tools":[
				{"type":"function","name":"search","description":"Search docs","parameters":{"type":"object","properties":{"q":{"type":"string"}}},"strict":false}
			]},
			{"type":"tool_search"},
			{"type":"web_search","name":"web_search"}
		],
		"tool_choice": "auto"
	}`)
	bridge := relaycommon.NewResponsesClientToolBridge()
	out := bridgeResponsesClientTools(body, bridgeAllKinds(), bridge)

	tools := gjson.GetBytes(out, "tools").Array()
	require.Len(t, tools, 5, "shell + apply_patch(function) + mcp__search + tool_search + untouched web_search")
	seen := map[string]string{}
	for _, tool := range tools {
		seen[tool.Get("name").String()] = tool.Get("type").String()
	}
	assert.Equal(t, "function", seen["shell"])
	assert.Equal(t, "function", seen["apply_patch"])
	assert.Equal(t, "function", seen["mcp__search"])
	assert.Equal(t, "function", seen["tool_search"])
	assert.Equal(t, "string", gjson.GetBytes(out, `tools.#(name=="apply_patch").parameters.properties.input.type`).String())
	assert.False(t, gjson.GetBytes(out, `tools.#(name=="mcp__search").strict`).Bool())
	assert.Contains(t, gjson.GetBytes(out, `tools.#(name=="apply_patch").description`).String(), "Apply a patch to files")

	// history rewritten
	assert.Equal(t, "function_call", gjson.GetBytes(out, "input.1.type").String())
	assert.Equal(t, "apply_patch", gjson.GetBytes(out, "input.1.name").String())
	assert.Equal(t, `{"input":"*** Begin Patch\n+hello"}`, gjson.GetBytes(out, "input.1.arguments").String())
	assert.Equal(t, "function_call_output", gjson.GetBytes(out, "input.2.type").String())
	assert.NotEmpty(t, gjson.GetBytes(out, "input.2.output").String())
	assert.Equal(t, "mcp__search", gjson.GetBytes(out, "input.3.name").String())
	assert.False(t, gjson.GetBytes(out, "input.3.namespace").Exists(), "namespace field must be folded into the name")
	assert.Equal(t, "function_call_output", gjson.GetBytes(out, "input.4.type").String())

	// registry
	spec, ok := bridge.Lookup("apply_patch")
	require.True(t, ok)
	assert.Equal(t, relaycommon.ResponsesClientToolCustom, spec.Kind)
	spec, ok = bridge.Lookup("mcp__search")
	require.True(t, ok)
	assert.Equal(t, relaycommon.ResponsesClientToolNamespace, spec.Kind)
	assert.Equal(t, "mcp", spec.Namespace)
	assert.Equal(t, "search", spec.Name)
	spec, ok = bridge.Lookup("tool_search")
	require.True(t, ok)
	assert.Equal(t, relaycommon.ResponsesClientToolSearch, spec.Kind)
}

func TestBridgeResponsesClientToolsNoKindsNoChange(t *testing.T) {
	body := []byte(`{"model":"m","tools":[{"type":"custom","name":"apply_patch"}],"input":[{"type":"custom_tool_call","call_id":"c","name":"apply_patch","input":"x"}]}`)
	assert.Equal(t, string(body), string(bridgeResponsesClientTools(body, nil, relaycommon.NewResponsesClientToolBridge())))
	assert.Equal(t, string(body), string(bridgeResponsesClientTools(body, map[string]struct{}{}, relaycommon.NewResponsesClientToolBridge())))
}

func TestBridgeResponsesClientToolsNoMatchingToolsByteIdentical(t *testing.T) {
	body := []byte(`{"model":"m","tools":[{"type":"function","name":"shell","parameters":{"type":"object"}}],"input":"hi"}`)
	assert.Equal(t, string(body), string(bridgeResponsesClientTools(body, bridgeAllKinds(), relaycommon.NewResponsesClientToolBridge())))
}

func TestBridgeResponsesClientToolsFunctionNameCollision(t *testing.T) {
	// a real function tool named apply_patch wins; the custom tool is left
	// as-is so nothing silently changes meaning
	body := []byte(`{"model":"m","tools":[
		{"type":"function","name":"apply_patch","parameters":{"type":"object"}},
		{"type":"custom","name":"apply_patch"}
	]}`)
	bridge := relaycommon.NewResponsesClientToolBridge()
	out := bridgeResponsesClientTools(body, bridgeAllKinds(), bridge)
	assert.Equal(t, string(body), string(out), "nothing bridgeable, body stays byte-identical")
	spec, ok := bridge.Lookup("apply_patch")
	require.True(t, ok)
	assert.Equal(t, relaycommon.ResponsesClientToolFunction, spec.Kind, "the plain function occupies the name")
}

func TestBridgeResponsesClientToolsToolSearchOutputHistory(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"input": [
			{"type":"tool_search_call","call_id":"ts1","arguments":{"query":"docs"}},
			{"type":"tool_search_output","call_id":"ts1","tools":[
				{"type":"function","name":"fetch","namespace":"docs","description":"Fetch a doc","parameters":{"type":"object"},"strict":true}
			]}
		],
		"tools": [{"type":"tool_search"}]
	}`)
	bridge := relaycommon.NewResponsesClientToolBridge()
	out := bridgeResponsesClientTools(body, bridgeAllKinds(), bridge)

	assert.Equal(t, "function_call", gjson.GetBytes(out, "input.0.type").String())
	assert.Equal(t, "tool_search", gjson.GetBytes(out, "input.0.name").String())
	assert.Equal(t, "function_call_output", gjson.GetBytes(out, "input.1.type").String())

	tools := gjson.GetBytes(out, "tools").Array()
	require.Len(t, tools, 2)
	assert.Equal(t, "docs__fetch", tools[1].Get("name").String())
	assert.True(t, tools[1].Get("strict").Bool())
	spec, ok := bridge.Lookup("docs__fetch")
	require.True(t, ok)
	assert.Equal(t, relaycommon.ResponsesClientToolNamespace, spec.Kind)
}

func TestFlattenResponsesNamespacedToolName(t *testing.T) {
	assert.Equal(t, "ns__tool", flattenResponsesNamespacedToolName("ns", "tool"))
	long := flattenResponsesNamespacedToolName("a-very-long-namespace-name-exceeding-the-limit", "and-a-very-long-tool-name-too")
	require.LessOrEqual(t, len(long), 64)
	assert.NotEqual(t, "a-very-long-namespace-name-exceeding-the-limit__and-a-very-long-tool-name-too", long, "long names must be truncated with a hash suffix")
}

func TestBridgeResponsesClientToolsPreservesCapturedMagpieStrictFalse(t *testing.T) {
	dir := os.Getenv("SERVER104_CHAIN_DIR")
	if dir == "" {
		t.Skip("captured-fixture regression is explicit-only")
	}
	body, err := os.ReadFile(filepath.Join(dir, "magpie-offline-outbound.json"))
	require.NoError(t, err)

	bridge := relaycommon.NewResponsesClientToolBridge()
	out := bridgeResponsesClientTools(body, bridgeAllKinds(), bridge)

	spawn := gjson.GetBytes(out, `tools.#(name=="multi_agent_v1__spawn_agent")`)
	require.True(t, spawn.Exists(), "bridged spawn_agent must exist")
	assert.False(t, spawn.Get("strict").Bool())
	assert.True(t, spawn.Get("strict").Exists(), "explicit strict:false must not be dropped")
	assert.True(t, strings.Contains(spawn.Get("parameters").Raw, `"reasoning_effort"`))
	spec, ok := bridge.Lookup("multi_agent_v1__spawn_agent")
	require.True(t, ok)
	assert.Equal(t, relaycommon.ResponsesClientToolNamespace, spec.Kind)
	assert.Equal(t, "multi_agent_v1", spec.Namespace)
	assert.Equal(t, "spawn_agent", spec.Name)
}
