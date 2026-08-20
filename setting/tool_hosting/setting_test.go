package tool_hosting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateToolHostingProvidersJSON(t *testing.T) {
	valid := `{"search1":{"kind":"web_search","type":"tavily","api_key":"k"},
			           "gemini-channel":{"kind":"web_search","type":"gemini","channel_id":5,"model":"gemini-2.5-flash"},
			           "code-plan-search":{"kind":"web_search","type":"zhipu_code_plan_search_mcp","channel_id":6},
			           "vision":{"kind":"image_recognition","type":"gemini","api_key":"k","model":"gemini-2.5-flash"},
			           "openai-vision-channel":{"kind":"image_recognition","type":"openai","channel_id":8,"model":"gpt-4o-mini"},
			           "code-plan-vision":{"kind":"image_recognition","type":"zhipu_code_plan_vision_mcp","channel_id":7},
			           "img":{"kind":"image_generation","type":"openai_images","api_key":"k"},
			           "img-channel":{"kind":"image_generation","type":"gemini_images","channel_id":9,"model":"gemini-2.5-flash-image"},
		           "searx":{"kind":"web_search","type":"searxng","api_base":"https://searx.example"}}`

	require.NoError(t, ValidateToolHostingProvidersJSON(valid))

	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"audio","type":"gemini","api_key":"k"}}`), "invalid kind")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"web_search","type":"midjourney","api_key":"k"}}`), "type not valid for kind")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"web_search","type":"tavily"}}`), "keyed provider without key")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"web_search","type":"searxng"}}`), "searxng without api_base")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"web_search","type":"http_json","api_base":"https://e.com/q={query}"}}`), "http_json without result_path")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"web_search","type":"tavily","api_key":"k","channel_id":-1}}`), "negative channel_id")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"web_search","type":"tavily","channel_id":6}}`), "plain API provider cannot borrow channel credentials")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"web_search","type":"zhipu_code_plan_search_mcp"}}`), "MCP search without key or channel")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"image_recognition","type":"zhipu_code_plan_vision_mcp"}}`), "MCP vision without key or channel")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"image_generation","type":"tavily","api_key":"k"}}`), "search type on image kind")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"image_generation","type":"openai","api_key":"k"}}`), "recognition executor on image generation kind")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"web_search","type":"openai_images","api_key":"k"}}`), "image executor on search kind")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"image_recognition","type":"channel_bad"}}`), "unknown type")
}

func TestGetProviderAndChannelBacked(t *testing.T) {
	SetToolHostingForTest(map[string]ToolHostingProvider{
		"g": {Kind: KindWebSearch, Type: ExecutorZhipuCodePlanSearchMCP, ChannelID: 6},
		"t": {Kind: KindWebSearch, Type: "tavily", APIKey: "k"},
	})
	provider, ok := GetProvider("g")
	require.True(t, ok)
	require.Equal(t, int64(6), provider.ChannelID)
	require.Equal(t, ExecutorZhipuCodePlanSearchMCP, provider.Type)

	_, ok = GetProvider("missing")
	require.False(t, ok)
	_, ok = GetProvider("  t  ")
	require.True(t, ok, "name trimmed in lookup")
	require.Len(t, GetProviders(), 2)
}

func TestLoadToolHostingProvidersFromJSONString(t *testing.T) {
	SetToolHostingForTest(map[string]ToolHostingProvider{
		"old": {Kind: KindWebSearch, Type: "tavily", APIKey: "k"},
	})
	// invalid legacy entries dropped individually, valid ones survive
	LoadToolHostingProvidersFromJSONString(`{"bad":{"kind":"nope","type":"x"},"ok":{"kind":"web_search","type":"brave","api_key":"k"}}`)
	_, hasBad := GetProvider("bad")
	require.False(t, hasBad)
	_, hasOk := GetProvider("ok")
	require.True(t, hasOk)
	_, hasOld := GetProvider("old")
	require.False(t, hasOld, "replaced wholesale")
}
