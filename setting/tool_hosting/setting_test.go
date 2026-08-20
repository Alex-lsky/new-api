package tool_hosting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateToolHostingProvidersJSON(t *testing.T) {
	valid := `{"search1":{"kind":"web_search","type":"tavily","api_key":"k"},
	           "gemini-channel":{"kind":"web_search","type":"gemini","channel_id":6,"model":"gemini-2.5-flash"},
	           "vision":{"kind":"image_recognition","type":"gemini","api_key":"k","model":"gemini-2.5-flash"},
	           "img":{"kind":"image_generation","type":"openai_images","api_key":"k"},
	           "searx":{"kind":"web_search","type":"searxng","api_base":"https://searx.example"}}`
	require.NoError(t, ValidateToolHostingProvidersJSON(valid))

	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"audio","type":"gemini","api_key":"k"}}`), "invalid kind")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"web_search","type":"midjourney","api_key":"k"}}`), "type not valid for kind")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"web_search","type":"tavily"}}`), "keyed provider without key")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"web_search","type":"searxng"}}`), "searxng without api_base")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"web_search","type":"http_json","api_base":"https://e.com/q={query}"}}`), "http_json without result_path")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"web_search","type":"tavily","api_key":"k","channel_id":-1}}`), "negative channel_id")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"image_generation","type":"tavily","api_key":"k"}}`), "search type on image kind")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"image_recognition","type":"channel_bad"}}`), "unknown type")
}

func TestGetProviderAndChannelBacked(t *testing.T) {
	SetToolHostingForTest(map[string]ToolHostingProvider{
		"g": {Kind: KindWebSearch, Type: "gemini", ChannelID: 6, Model: "gemini-2.5-flash"},
		"t": {Kind: KindWebSearch, Type: "tavily", APIKey: "k"},
	})
	provider, ok := GetProvider("g")
	require.True(t, ok)
	require.Equal(t, int64(6), provider.ChannelID)
	require.Equal(t, "gemini-2.5-flash", provider.Model)

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
