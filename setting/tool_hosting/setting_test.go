package tool_hosting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLookupBindingLongestPrefix(t *testing.T) {
	SetToolHostingForTest(map[string]ToolHostingProvider{
		"a": {Kind: KindWebSearch, Type: "gemini", APIKey: "k"},
		"b": {Kind: KindWebSearch, Type: "tavily", APIKey: "k"},
	}, map[string]map[string]string{
		"muse-*":         {"web_search": "a"},
		"muse-spark-1.2": {"web_search": "b"},
		"deepseek-v4-*":  {"image_recognition": "a"},
	})
	require.Equal(t, "b", LookupBinding(KindWebSearch, "muse-spark-1.2"), "exact model beats shorter prefix")
	require.Equal(t, "a", LookupBinding(KindWebSearch, "muse-spark-2.0"), "prefix matches")
	require.Equal(t, "", LookupBinding(KindWebSearch, "other-model"), "unbound model")
	require.Equal(t, "", LookupBinding(KindRecognition, "muse-spark-1.2"), "kind not bound on the model")
	require.Equal(t, "a", LookupBinding(KindRecognition, "deepseek-v4-flash"), "recognition binding by prefix")
}

func TestValidateToolHostingProvidersJSON(t *testing.T) {
	valid := `{"search1":{"kind":"web_search","type":"tavily","api_key":"k"},
	           "vision":{"kind":"image_recognition","type":"gemini","api_key":"k","model":"gemini-2.5-flash"},
	           "img":{"kind":"image_generation","type":"openai_images","api_key":"k"},
	           "searx":{"kind":"web_search","type":"searxng","api_base":"https://searx.example"},
	           "ref":{"kind":"web_search","type":"channel","channel_id":6,"executor":"gemini"}}`
	require.NoError(t, ValidateToolHostingProvidersJSON(valid))

	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"audio","type":"gemini","api_key":"k"}}`), "invalid kind")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"web_search","type":"midjourney","api_key":"k"}}`), "type not valid for kind")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"web_search","type":"tavily"}}`), "keyed provider without key")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"web_search","type":"searxng"}}`), "searxng without api_base")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"web_search","type":"http_json","api_base":"https://e.com/q={query}"}}`), "http_json without result_path")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"web_search","type":"channel","executor":"gemini"}}`), "channel without channel_id")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"web_search","type":"channel","channel_id":6}}`), "channel search without executor")
	require.Error(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"image_generation","type":"channel","channel_id":6}}`), "channel imagegen without executor")
	require.NoError(t, ValidateToolHostingProvidersJSON(`{"x":{"kind":"image_recognition","type":"channel","channel_id":6}}`), "recognition channel ref needs no executor")
}

func TestValidateToolHostingBindingsJSON(t *testing.T) {
	SetToolHostingForTest(map[string]ToolHostingProvider{
		"p": {Kind: KindWebSearch, Type: "tavily", APIKey: "k"},
	}, map[string]map[string]string{})
	require.NoError(t, ValidateToolHostingBindingsJSON(`{"muse-spark-1.2":{"web_search":"p"}}`))
	require.Error(t, ValidateToolHostingBindingsJSON(`{"muse-spark-1.2":{"audio":"p"}}`), "invalid kind")
	require.Error(t, ValidateToolHostingBindingsJSON(`{"muse-spark-1.2":{"web_search":"missing"}}`), "provider does not exist")
}

func TestLoadAndPruneBindings(t *testing.T) {
	SetToolHostingForTest(map[string]ToolHostingProvider{
		"p": {Kind: KindWebSearch, Type: "tavily", APIKey: "k"},
	}, map[string]map[string]string{
		"muse-1.2": {"web_search": "p"},
	})
	LoadToolHostingProvidersFromJSONString(`{"other":{"kind":"web_search","type":"brave","api_key":"k"}}`)
	_, exists := GetProvider("p")
	require.False(t, exists, "removed provider")
	require.Equal(t, "", LookupBinding(KindWebSearch, "muse-1.2"), "binding pruned to removed provider")
	provider, ok := GetProvider("other")
	require.True(t, ok)
	require.Equal(t, "brave", provider.Type)
}

func TestGetProvidersAndBindings(t *testing.T) {
	SetToolHostingForTest(map[string]ToolHostingProvider{
		"p": {Kind: KindWebSearch, Type: "tavily", APIKey: "k"},
	}, map[string]map[string]string{
		"m": {"web_search": "p"},
	})
	require.Len(t, GetProviders(), 1)
	require.Len(t, GetBindings(), 1)
}
