package relay

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStripResponsesToolTypes(t *testing.T) {
	webSearchBlacklist := map[string]struct{}{"web_search": {}}

	tests := []struct {
		name     string
		in       string
		blacklist map[string]struct{}
		unchanged bool
		want     func(t *testing.T, out string)
	}{
		{
			name: "codex request: web_search stripped, function and custom tools kept",
			in: `{
				"model": "muse-spark-1.2",
				"instructions": "You are a helpful assistant.",
				"input": [{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],
				"tools": [
					{"type":"function","name":"shell","description":"Run a shell command","parameters":{"type":"object"}},
					{"type":"web_search","name":"web_search"},
					{"type":"custom","name":"apply_patch","input_schema":{"type":"object"}}
				],
				"tool_choice": "auto",
				"parallel_tool_calls": false,
				"store": false
			}`,
			blacklist: webSearchBlacklist,
			want: func(t *testing.T, out string) {
				assert.Equal(t, "muse-spark-1.2", gjson.Get(out, "model").String())
				assert.Len(t, gjson.Get(out, "tools").Array(), 2)
				assert.Equal(t, "shell", gjson.Get(out, "tools.0.name").String())
				assert.Equal(t, "apply_patch", gjson.Get(out, "tools.1.name").String())
				assert.Equal(t, "auto", gjson.Get(out, "tool_choice").String())
				assert.Equal(t, "false", gjson.Get(out, "parallel_tool_calls").Raw)
			},
		},
		{
			name: "no blacklisted tool leaves body byte-identical",
			in: `{"model":"m","tools":[{"type":"function","name":"shell"}],"tool_choice":"auto"}`,
			blacklist: webSearchBlacklist,
			unchanged: true,
		},
		{
			name: "missing tools untouched",
			in: `{"model":"m","input":"hi"}`,
			blacklist: webSearchBlacklist,
			unchanged: true,
		},
		{
			name: "tools not an array untouched",
			in: `{"model":"m","tools":{"type":"function"}}`,
			blacklist: webSearchBlacklist,
			unchanged: true,
		},
		{
			name: "tool without type kept",
			in: `{"model":"m","tools":[{"name":"mystery"}]}`,
			blacklist: webSearchBlacklist,
			unchanged: true,
		},
		{
			name: "all tools stripped removes tools key",
			in: `{"model":"m","tools":[{"type":"web_search"}]}`,
			blacklist: webSearchBlacklist,
			want: func(t *testing.T, out string) {
				assert.False(t, gjson.Get(out, "tools").Exists(), "empty tools array must be removed, not left as []")
			},
		},
		{
			name: "tool_choice pinned to stripped tool removed",
			in: `{"model":"m","tools":[{"type":"web_search"},{"type":"function","name":"shell"}],"tool_choice":{"type":"web_search"}}`,
			blacklist: webSearchBlacklist,
			want: func(t *testing.T, out string) {
				assert.False(t, gjson.Get(out, "tool_choice").Exists())
				assert.Len(t, gjson.Get(out, "tools").Array(), 1)
			},
		},
		{
			name: "tool_choice function reference kept",
			in: `{"model":"m","tools":[{"type":"web_search"},{"type":"function","name":"shell"}],"tool_choice":{"type":"function","name":"shell"}}`,
			blacklist: webSearchBlacklist,
			want: func(t *testing.T, out string) {
				assert.Equal(t, "shell", gjson.Get(out, "tool_choice.name").String())
			},
		},
		{
			name: "include entries of stripped tool removed, unrelated kept",
			in: `{"model":"m","tools":[{"type":"web_search"}],"include":["reasoning.encrypted_content","web_search_call.results","message.input_image.image_url"]}`,
			blacklist: webSearchBlacklist,
			want: func(t *testing.T, out string) {
				include := gjson.Get(out, "include").Array()
				require.Len(t, include, 2)
				assert.Equal(t, "reasoning.encrypted_content", include[0].String())
				assert.Equal(t, "message.input_image.image_url", include[1].String())
			},
		},
		{
			name: "web_search_preview include prefix also stripped",
			in: `{"model":"m","tools":[{"type":"web_search_preview"}],"include":["web_search_call.results"]}`,
			blacklist: map[string]struct{}{"web_search_preview": {}},
			want: func(t *testing.T, out string) {
				assert.False(t, gjson.Get(out, "include").Exists(), "sole include entry removed with the tool")
			},
		},
		{
			name: "unknown tool type uses default _call prefix for include",
			in: `{"model":"m","tools":[{"type":"image_generation"}],"include":["image_generation_call.partial_image","reasoning.encrypted_content"]}`,
			blacklist: map[string]struct{}{"image_generation": {}},
			want: func(t *testing.T, out string) {
				include := gjson.Get(out, "include").Array()
				require.Len(t, include, 1)
				assert.Equal(t, "reasoning.encrypted_content", include[0].String())
			},
		},
		{
			name: "tool type matching tolerates surrounding whitespace",
			in: `{"model":"m","tools":[{"type":" web_search "}]}`,
			blacklist: webSearchBlacklist,
			want: func(t *testing.T, out string) {
				assert.False(t, gjson.Get(out, "tools").Exists())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := stripResponsesToolTypes([]byte(tt.in), tt.blacklist)
			if tt.unchanged {
				assert.Equal(t, tt.in, string(out), "body must stay byte-identical")
				return
			}
			require.NotEmpty(t, out)
			tt.want(t, string(out))
		})
	}
}

func TestStripResponsesToolTypesEmptyBlacklist(t *testing.T) {
	in := []byte(`{"model":"m","tools":[{"type":"web_search"}]}`)
	assert.Equal(t, in, stripResponsesToolTypes(in, nil), "nil blacklist must be a no-op")
	assert.Equal(t, in, stripResponsesToolTypes(in, map[string]struct{}{}), "empty blacklist must be a no-op")
}
