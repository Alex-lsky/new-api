package relay

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeResponsesTools(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want func(t *testing.T, out string)
	}{
		{
			name: "codex nested function tools flattened",
			in: `[
				{"type":"function","function":{"name":"shell","description":"Run shell","parameters":{"type":"object","properties":{"cmd":{"type":"string","max_tokens":128}},"required":["cmd"]}}},
				{"type":"function","name":"web_search","description":"Search","max_results":5},
				{"type":"custom","name":"apply_patch","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}},
				{"type":"web_search"},
				{"type":"function","function":{"name":"view_image","description":"View image","parameters":{"type":"object","properties":{"file":{"type":"string"}},"required":["file"]}}}
			]`,
			want: func(t *testing.T, out string) {
				assert.Equal(t, "shell", gjson.Get(out, "0.name").String())
				assert.Equal(t, "Run shell", gjson.Get(out, "0.description").String())
				assert.Equal(t, "object", gjson.Get(out, "0.parameters.type").String())
				assert.Equal(t, "128", gjson.Get(out, "0.parameters.properties.cmd.max_tokens").Raw, "nested number must round-trip without float mangling")
				assert.False(t, gjson.Get(out, "0.function").Exists(), "nested function must be removed")

				assert.Equal(t, "web_search", gjson.Get(out, "1.name").String())
				assert.False(t, gjson.Get(out, "1.function").Exists())

				assert.Equal(t, "apply_patch", gjson.Get(out, "2.name").String())
				assert.True(t, gjson.Get(out, "2.input_schema").Exists(), "custom tool input_schema must be preserved")
				assert.False(t, gjson.Get(out, "2.function").Exists())

				assert.Equal(t, "web_search", gjson.Get(out, "3.type").String(), "nameless web_search passes through untouched")

				assert.Equal(t, "view_image", gjson.Get(out, "4.name").String())
				assert.Equal(t, "file", gjson.Get(out, "4.parameters.required.0").String())
				assert.False(t, gjson.Get(out, "4.function").Exists())
			},
		},
		{
			name: "already flat tools unchanged",
			in: `[
				{"type":"function","name":"read_file","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}},
				{"type":"web_search","name":"web_search","max_results":3}
			]`,
			want: func(t *testing.T, out string) {
				assert.Equal(t, "read_file", gjson.Get(out, "0.name").String())
				assert.Equal(t, "web_search", gjson.Get(out, "1.name").String())
				assert.False(t, gjson.Get(out, "0.function").Exists())
				assert.False(t, gjson.Get(out, "1.function").Exists())
			},
		},
		{
			name: "nested tool with existing top-level name untouched",
			in: `[
				{"type":"function","name":"already_named","function":{"name":"ignored","description":"d"}}
			]`,
			want: func(t *testing.T, out string) {
				assert.Equal(t, "already_named", gjson.Get(out, "0.name").String())
				assert.True(t, gjson.Get(out, "0.function").Exists(), "hybrid tool left as-is")
			},
		},
		{
			name: "nameless web_search and image_generation get type-based names",
			in: `[
				{"type":"function","function":{"name":"shell","description":"Run shell","parameters":{"type":"object"}}},
				{"type":"web_search","external_web_access":false},
				{"type":"image_generation","output_format":"jpeg"}
			]`,
			want: func(t *testing.T, out string) {
				assert.Equal(t, "shell", gjson.Get(out, "0.name").String())
				assert.False(t, gjson.Get(out, "0.function").Exists())
				assert.Equal(t, "web_search", gjson.Get(out, "1.name").String(), "nameless tool gets type as name")
				assert.Equal(t, "false", gjson.Get(out, "1.external_web_access").Raw)
				assert.Equal(t, "image_generation", gjson.Get(out, "2.name").String())
				assert.Equal(t, "jpeg", gjson.Get(out, "2.output_format").String())
			},
		},
		{
			name: "empty tools unchanged",
			in:   `[]`,
			want: func(t *testing.T, out string) {
				assert.Equal(t, `[]`, out)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := normalizeResponsesTools(json.RawMessage(tt.in))
			require.True(t, gjson.ValidBytes(out), "output must be valid JSON: %s", string(out))
			tt.want(t, string(out))
		})
	}
}

func TestNormalizeResponsesToolsRoundTripWithToolCalls(t *testing.T) {
	body := `{
		"model":"deepseek-v4-flash",
		"instructions":"You are a coding agent.",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"list files"}]},
			{"type":"function_call","call_id":"call_1","name":"list_files","arguments":"{\"path\":\"/tmp\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"[\"a.txt\"]"}
		],
		"tools":[
			{"type":"function","function":{"name":"list_files","description":"List files","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}},
			{"type":"custom","name":"apply_patch","input_schema":{"type":"object","properties":{"patch":{"type":"string"}}}}
		]
	}`

	var req dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(body), &req))

	req.Tools = normalizeResponsesTools(req.Tools)

	raw, err := common.Marshal(req)
	require.NoError(t, err)

	got := string(raw)
	assert.Equal(t, "list_files", gjson.Get(got, "tools.0.name").String())
	assert.False(t, gjson.Get(got, "tools.0.function").Exists(), "nested function must be flattened before upstream")
	assert.Equal(t, "apply_patch", gjson.Get(got, "tools.1.name").String())
	assert.Equal(t, "function_call", gjson.Get(got, "input.1.type").String(), "tool call item untouched")
	assert.Equal(t, "call_1", gjson.Get(got, "input.1.call_id").String())
	assert.Equal(t, "function_call_output", gjson.Get(got, "input.2.type").String(), "tool call output item untouched")
	assert.Equal(t, "[\"a.txt\"]", gjson.Get(got, "input.2.output").String())
}
