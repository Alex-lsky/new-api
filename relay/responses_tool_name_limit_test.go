package relay

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func overLongToolName() string {
	// 66 characters, as emitted by ChatGPT/Codex MCP app connectors.
	return "mcp__codex_apps__codex_document_control___execute_document_command"
}

func TestTruncateResponsesToolName(t *testing.T) {
	assert.Equal(t, "short_name", truncateResponsesToolName("short_name"))
	long := overLongToolName()
	short := truncateResponsesToolName(long)
	require.Len(t, short, 64)
	assert.True(t, gjson.Get(`{"n":"`+short+`"}`, "n").Exists(), "short name must survive a JSON round trip")
	assert.Equal(t, short, truncateResponsesToolName(long), "truncation must be deterministic")
}

func TestSanitizeResponsesToolNamesDeclarationAndHistory(t *testing.T) {
	long := overLongToolName()
	body := []byte(`{"model":"m","tool_choice":{"type":"function","name":"` + long + `"},` +
		`"tools":[` +
		`{"type":"function","name":"` + long + `"},` +
		`{"type":"function","name":"short_tool"},` +
		`{"type":"custom","name":"` + long + `"}],` +
		`"input":[{"type":"message","role":"user","content":"hi"},` +
		`{"type":"function_call","name":"` + long + `","call_id":"c1","arguments":"{}"},` +
		`{"type":"function_call_output","call_id":"c1","output":"done"}]}`)

	bridge := relaycommon.NewResponsesClientToolBridge()
	out := sanitizeResponsesToolNames(body, bridge)

	tools := gjson.GetBytes(out, "tools").Array()
	require.Len(t, tools, 3)
	short := tools[0].Get("name").String()
	require.Len(t, short, 64)
	assert.NotEqual(t, long, short)
	assert.Equal(t, "short_tool", tools[1].Get("name").String())
	assert.Equal(t, short, tools[2].Get("name").String(), "same original must map to the same short name")

	input := gjson.GetBytes(out, "input").Array()
	assert.Equal(t, short, input[1].Get("name").String())
	assert.Equal(t, "c1", input[1].Get("call_id").String())
	assert.Equal(t, short, gjson.GetBytes(out, "tool_choice.name").String())

	spec, ok := bridge.Lookup(short)
	require.True(t, ok)
	assert.Equal(t, relaycommon.ResponsesClientToolRenamed, spec.Kind)
	assert.Equal(t, long, spec.Name)
	assert.Equal(t, 1, bridge.Len(), "one original registers exactly one rename")
}

func TestSanitizeResponsesToolNamesLegacyNestedShape(t *testing.T) {
	long := overLongToolName()
	body := []byte(`{"tools":[{"type":"function","function":{"name":"` + long + `","parameters":{"type":"object"}}}]}`)
	bridge := relaycommon.NewResponsesClientToolBridge()
	out := sanitizeResponsesToolNames(body, bridge)
	name := gjson.GetBytes(out, "tools.0.function.name").String()
	require.Len(t, name, 64)
	_, ok := bridge.Lookup(name)
	require.True(t, ok)
}

func TestSanitizeResponsesToolNamesNoopWhenAllFit(t *testing.T) {
	body := []byte(`{"model":"m","tools":[{"type":"function","name":"fitting_name"}],"input":[{"type":"function_call","name":"fitting_name","call_id":"c1"}]}`)
	bridge := relaycommon.NewResponsesClientToolBridge()
	out := sanitizeResponsesToolNames(body, bridge)
	assert.Equal(t, body, out, "a body whose names all fit must be byte-identical")
	assert.Equal(t, 0, bridge.Len())
}

func TestSanitizeResponsesToolNamesSkipsNamespaceDeclarations(t *testing.T) {
	body := []byte(`{"tools":[{"type":"namespace","name":"codex_apps","tools":[{"type":"function","name":"child"}]}]}`)
	bridge := relaycommon.NewResponsesClientToolBridge()
	out := sanitizeResponsesToolNames(body, bridge)
	assert.Equal(t, body, out)
	assert.Equal(t, 0, bridge.Len())
}

func TestSanitizeResponsesToolNamesNilBridgeNoop(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","name":"` + overLongToolName() + `"}]}`)
	assert.Equal(t, body, sanitizeResponsesToolNames(body, nil))
}
