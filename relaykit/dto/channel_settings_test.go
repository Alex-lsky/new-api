package dto

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdvancedCustomValidateResponsesToChatConverterPath(t *testing.T) {
	valid := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
			},
		},
	}
	require.NoError(t, valid.Validate())

	validGemini := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
			},
		},
	}
	require.NoError(t, validGemini.Validate())

	tests := []struct {
		name         string
		incomingPath string
	}{
		{name: "chat completions", incomingPath: "/v1/chat/completions"},
		{name: "responses compact", incomingPath: "/v1/responses/compact"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &AdvancedCustomConfig{
				Routes: []AdvancedCustomRoute{
					{
						IncomingPath: tt.incomingPath,
						UpstreamPath: "/v1/chat/completions",
						Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
					},
				},
			}
			err := config.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "converter does not match incoming_path")
		})
	}
}

func TestAdvancedCustomValidateModelListRouteConstraints(t *testing.T) {
	valid := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: AdvancedCustomModelListPath,
				UpstreamPath: "https://upstream.example/custom/models",
				Converter:    advancedCustomConverterNone,
			},
		},
	}
	require.NoError(t, valid.Validate())

	tests := []struct {
		name   string
		routes []AdvancedCustomRoute
		want   string
	}{
		{
			name: "model matching rules",
			routes: []AdvancedCustomRoute{
				{
					IncomingPath: AdvancedCustomModelListPath,
					UpstreamPath: "/v1/models",
					Models:       []string{"gpt-4o"},
				},
			},
			want: "models must be empty",
		},
		{
			name: "converter",
			routes: []AdvancedCustomRoute{
				{
					IncomingPath: AdvancedCustomModelListPath,
					UpstreamPath: "/v1/models",
					Converter:    advancedCustomConverterOpenAIChatToOpenAIResponses,
				},
			},
			want: "converter must be none",
		},
		{
			name: "model placeholder",
			routes: []AdvancedCustomRoute{
				{
					IncomingPath: AdvancedCustomModelListPath,
					UpstreamPath: "/v1/models/{model}",
				},
			},
			want: "upstream_path must not contain {model}",
		},
		{
			name: "duplicate routes",
			routes: []AdvancedCustomRoute{
				{IncomingPath: AdvancedCustomModelListPath, UpstreamPath: "/v1/models"},
				{IncomingPath: AdvancedCustomModelListPath, UpstreamPath: "/provider/models"},
			},
			want: "duplicates the /v1/models route",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (&AdvancedCustomConfig{Routes: tt.routes}).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestAdvancedCustomModelListRouteRequiresExactIncomingPath(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/{model}",
				UpstreamPath: "/generic/{model}",
			},
			{
				IncomingPath: AdvancedCustomModelListPath,
				UpstreamPath: "/provider/models",
			},
		},
	}
	require.NoError(t, config.Validate())

	route, ok := config.ModelListRoute()
	require.True(t, ok)
	assert.Equal(t, "/provider/models", route.UpstreamPath)
}

func TestAdvancedCustomValidateBalanceRouteConstraints(t *testing.T) {
	valid := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{{
			IncomingPath: AdvancedCustomBalancePath,
			UpstreamPath: "/provider/balance",
			Converter:    advancedCustomConverterNone,
		}},
	}
	require.NoError(t, valid.Validate())

	route, ok := valid.BalanceRoute()
	require.True(t, ok)
	assert.Equal(t, "/provider/balance", route.UpstreamPath)

	tests := []struct {
		name   string
		routes []AdvancedCustomRoute
		want   string
	}{
		{
			name: "model matching rules",
			routes: []AdvancedCustomRoute{{
				IncomingPath: AdvancedCustomBalancePath,
				UpstreamPath: "/provider/balance",
				Models:       []string{"gpt-4o"},
			}},
			want: "models must be empty",
		},
		{
			name: "converter",
			routes: []AdvancedCustomRoute{{
				IncomingPath: AdvancedCustomBalancePath,
				UpstreamPath: "/provider/balance",
				Converter:    advancedCustomConverterOpenAIChatToOpenAIResponses,
			}},
			want: "converter must be none",
		},
		{
			name: "model placeholder",
			routes: []AdvancedCustomRoute{{
				IncomingPath: AdvancedCustomBalancePath,
				UpstreamPath: "/provider/{model}/balance",
			}},
			want: "upstream_path must not contain {model}",
		},
		{
			name: "duplicate routes",
			routes: []AdvancedCustomRoute{
				{IncomingPath: AdvancedCustomBalancePath, UpstreamPath: "/provider/balance"},
				{IncomingPath: AdvancedCustomBalancePath, UpstreamPath: "/provider/credits"},
			},
			want: "duplicates the /v1/dashboard/billing/credit_grants route",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (&AdvancedCustomConfig{Routes: tt.routes}).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestAdvancedCustomValidateDuplicateIncomingPathWithDisjointModels(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
				Models:       []string{"gpt-4o"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"gemini-2.5-flash"},
			},
		},
	}

	require.NoError(t, config.Validate())
}

func TestAdvancedCustomValidateDuplicateIncomingPathRejectsOverlappingModels(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
				Models:       []string{"shared-model"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"shared-model"},
			},
		},
	}

	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "models overlaps")
}

func TestAdvancedCustomValidateDuplicateIncomingPathRejectsMultipleCatchAllRoutes(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
			},
		},
	}

	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "catch-all already exists")
}

func TestAdvancedCustomValidateDuplicateIncomingPathRequiresCatchAllLast(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"gemini-2.5-flash"},
			},
		},
	}

	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "catch-all route must be last")
}

func TestAdvancedCustomMatchPathForModel(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"gemini-2.5-flash"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
				Models:       []string{"gpt-4o"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/responses",
				Converter:    advancedCustomConverterNone,
			},
		},
	}
	require.NoError(t, config.Validate())

	geminiRoute, ok := config.MatchPathForModel("/v1/responses", "gemini-2.5-flash")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterOpenAIResponsesToGemini, geminiRoute.Converter)

	chatRoute, ok := config.MatchPathForModel("/v1/responses", "gpt-4o")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterOpenAIResponsesToOpenAIChat, chatRoute.Converter)

	fallbackRoute, ok := config.MatchPathForModel("/v1/responses", "unknown-model")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterNone, fallbackRoute.Converter)
}

func TestAdvancedCustomMatchPathForModelRegexRules(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"re:^gemini-"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
				Models:       []string{"re:(?i)^OAI-"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/responses",
				Converter:    advancedCustomConverterNone,
			},
		},
	}
	require.NoError(t, config.Validate())

	geminiRoute, ok := config.MatchPathForModel("/v1/responses", "gemini-2.5-flash")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterOpenAIResponsesToGemini, geminiRoute.Converter)

	chatRoute, ok := config.MatchPathForModel("/v1/responses", "oai-test")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterOpenAIResponsesToOpenAIChat, chatRoute.Converter)

	fallbackRoute, ok := config.MatchPathForModel("/v1/responses", "gpt-4o")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterNone, fallbackRoute.Converter)
}

func TestAdvancedCustomRouteModelRegexRulesAreCachedCompiled(t *testing.T) {
	require.True(t, matchAdvancedCustomRouteModelRule("re:^cache-probe-", "cache-probe-model"))

	cached, ok := advancedCustomModelRegexCache.Load("^cache-probe-")
	require.True(t, ok)
	require.NotNil(t, cached)
	_, isRegexp := cached.(*regexp.Regexp)
	require.True(t, isRegexp)

	// Invalid patterns never match and are cached as nil so they are not recompiled.
	require.False(t, matchAdvancedCustomRouteModelRule("re:(", "anything"))
	cached, ok = advancedCustomModelRegexCache.Load("(")
	require.True(t, ok)
	re, _ := cached.(*regexp.Regexp)
	require.Nil(t, re)

	// Cached entries keep matching correctly on subsequent calls.
	require.True(t, matchAdvancedCustomRouteModelRule("re:^cache-probe-", "cache-probe-other"))
	require.False(t, matchAdvancedCustomRouteModelRule("re:^cache-probe-", "other-model"))
}

func TestAdvancedCustomMatchPathForModelExactRuleDoesNotMatchPrefix(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"gemini"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/responses",
				Converter:    advancedCustomConverterNone,
			},
		},
	}
	require.NoError(t, config.Validate())

	fallbackRoute, ok := config.MatchPathForModel("/v1/responses", "gemini-2.5-flash")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterNone, fallbackRoute.Converter)
}

func TestAdvancedCustomValidateDuplicateIncomingPathRejectsInvalidRegexModels(t *testing.T) {
	tests := []struct {
		name   string
		models []string
		want   string
	}{
		{name: "empty regex", models: []string{"re:"}, want: "regex is empty"},
		{name: "invalid regex", models: []string{"re:["}, want: "regex is invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &AdvancedCustomConfig{
				Routes: []AdvancedCustomRoute{
					{
						IncomingPath: "/v1/responses",
						UpstreamPath: "/v1beta/models/{model}:generateContent",
						Converter:    advancedCustomConverterOpenAIResponsesToGemini,
						Models:       tt.models,
					},
				},
			}

			err := config.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestAdvancedCustomValidateDuplicateIncomingPathRejectsDuplicateRegexModels(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"re:^gemini-"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
				Models:       []string{"re:^gemini-"},
			},
		},
	}

	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "models overlaps")
}

func TestAdvancedCustomMatchPathForModelUsesFirstMatchingRegexRoute(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"re:^gemini-"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
				Models:       []string{"gemini-2.5-flash"},
			},
		},
	}
	require.NoError(t, config.Validate())

	route, ok := config.MatchPathForModel("/v1/responses", "gemini-2.5-flash")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterOpenAIResponsesToGemini, route.Converter)
}

func TestAdvancedCustomSupportedEndpointTypesForModel(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"re:^gemini-"},
			},
			{
				IncomingPath: "/v1beta/models/{model}:generateContent",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Models:       []string{"re:^gemini-"},
			},
			{
				IncomingPath: "/v1beta/models/{model}:streamGenerateContent",
				UpstreamPath: "/v1beta/models/{model}:streamGenerateContent",
				Models:       []string{"re:^gemini-"},
			},
			{
				IncomingPath: "/v1/chat/completions",
				UpstreamPath: "/v1/chat/completions",
				Models:       []string{"gpt-4o"},
			},
			{
				IncomingPath: "/v1/messages",
				UpstreamPath: "/v1/messages",
			},
			{
				IncomingPath: "/custom/endpoint",
				UpstreamPath: "/custom/endpoint",
			},
		},
	}
	require.NoError(t, config.Validate())

	assert.Equal(t, []types.EndpointType{
		types.EndpointTypeOpenAIResponse,
		types.EndpointTypeGemini,
		types.EndpointTypeAnthropic,
	}, config.SupportedEndpointTypesForModel("gemini-2.5-flash"))
	assert.Equal(t, []types.EndpointType{
		types.EndpointTypeOpenAI,
		types.EndpointTypeAnthropic,
	}, config.SupportedEndpointTypesForModel("gpt-4o"))
	assert.Equal(t, []types.EndpointType{
		types.EndpointTypeAnthropic,
	}, config.SupportedEndpointTypesForModel("other-model"))
}

func TestAdvancedCustomValidateAlphaSearchConverterPath(t *testing.T) {
	valid := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/alpha/search",
				UpstreamPath: "/v1/alpha/search",
				Converter:    advancedCustomConverterNone,
			},
		},
	}
	require.NoError(t, valid.Validate())
	assert.Equal(t, []types.EndpointType{
		types.EndpointTypeOpenAIAlphaSearch,
	}, valid.SupportedEndpointTypesForModel("gpt-5.1"))

	nonNoneConverters := []string{
		advancedCustomConverterClaudeMessagesToOpenAIChat,
		advancedCustomConverterOpenAIChatToClaudeMessages,
		advancedCustomConverterOpenAIChatToOpenAIResponses,
		advancedCustomConverterOpenAIResponsesToOpenAIChat,
		advancedCustomConverterOpenAIResponsesToGemini,
		advancedCustomConverterGeminiContentToOpenAIChat,
		advancedCustomConverterOpenAIChatToGeminiContent,
	}
	for _, converter := range nonNoneConverters {
		t.Run(converter, func(t *testing.T) {
			config := &AdvancedCustomConfig{
				Routes: []AdvancedCustomRoute{
					{
						IncomingPath: "/v1/alpha/search",
						UpstreamPath: "/v1/alpha/search",
						Converter:    converter,
					},
				},
			}
			err := config.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "converter does not match incoming_path")
		})
	}
}

func TestChannelSettingsHTTPTransportJSONRoundTrip(t *testing.T) {
	legacy := `{"proxy":"http://127.0.0.1:8080","force_format":true}`
	var settings ChannelSettings
	require.NoError(t, json.Unmarshal([]byte(legacy), &settings))
	assert.Equal(t, "http://127.0.0.1:8080", settings.Proxy)
	assert.True(t, settings.ForceFormat)
	assert.Empty(t, settings.HTTPProtocol)
	assert.Zero(t, settings.HTTP2ConnectionShards)

	encoded, err := json.Marshal(settings)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "http_protocol")
	assert.NotContains(t, string(encoded), "http2_connection_shards")

	explicit := ChannelSettings{
		Proxy:                 "socks5://127.0.0.1:1080",
		HTTPProtocol:          HTTPProtocolHTTP1,
		HTTP2ConnectionShards: 1,
	}
	encoded, err = json.Marshal(explicit)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"http_protocol":"http1"`)

	var decoded ChannelSettings
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, explicit.HTTPProtocol, decoded.HTTPProtocol)
	assert.Equal(t, 1, decoded.HTTP2ConnectionShards)

	sharded := ChannelSettings{HTTP2ConnectionShards: 4}
	encoded, err = json.Marshal(sharded)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"http2_connection_shards":4`)
	assert.NotContains(t, string(encoded), "http_protocol")
}

func TestChannelSettingsValidateHTTPTransport(t *testing.T) {
	require.NoError(t, (&ChannelSettings{}).ValidateHTTPTransport())
	require.NoError(t, (&ChannelSettings{HTTPProtocol: "AUTO"}).ValidateHTTPTransport())
	require.NoError(t, (&ChannelSettings{HTTPProtocol: "http1"}).ValidateHTTPTransport())
	require.NoError(t, (&ChannelSettings{HTTP2ConnectionShards: 8}).ValidateHTTPTransport())

	err := (&ChannelSettings{HTTPProtocol: "http2"}).ValidateHTTPTransport()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http_protocol")

	err = (&ChannelSettings{HTTP2ConnectionShards: -1}).ValidateHTTPTransport()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http2_connection_shards")

	err = (&ChannelSettings{HTTP2ConnectionShards: 9}).ValidateHTTPTransport()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http2_connection_shards")

	err = (&ChannelSettings{HTTPProtocol: "http1", HTTP2ConnectionShards: 2}).ValidateHTTPTransport()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http2_connection_shards")
}

func TestChannelSettingsStripToolTypeSet(t *testing.T) {
	require.Nil(t, (&ChannelSettings{}).StripToolTypeSet())
	require.Nil(t, (*ChannelSettings)(nil).StripToolTypeSet())
	require.Nil(t, (&ChannelSettings{StripToolTypes: []string{"  ", ""}}).StripToolTypeSet(), "blank entries must leave the blacklist empty")

	set := (&ChannelSettings{StripToolTypes: []string{" Web_Search ", "image_generation", "web_search"}}).StripToolTypeSet()
	require.Len(t, set, 2)
	assert.Contains(t, set, "web_search")
	assert.Contains(t, set, "image_generation")
}

func TestChannelSettingsEmulateToolTypes(t *testing.T) {
	require.Nil(t, (&ChannelSettings{}).EmulateToolTypeSet())
	require.Nil(t, (*ChannelSettings)(nil).EmulateToolTypeSet())

	set := (&ChannelSettings{EmulateToolTypes: []string{"web_search_preview"}}).EmulateToolTypeSet()
	require.Len(t, set, 1)
	assert.Contains(t, set, "web_search", "web_search_preview folds onto web_search")

	require.Nil(t, (&ChannelSettings{EmulateToolTypes: []string{"web_search"}}).EmulatedWebSearchBackend(), "no backend configured")
	require.Nil(t, (&ChannelSettings{
		EmulateToolTypes:     []string{"web_search"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{"web_search": {Provider: "gemini"}},
	}).EmulatedWebSearchBackend(), "backend without api key is unusable")
	backend := (&ChannelSettings{
		EmulateToolTypes:     []string{"web_search"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{"web_search": {Provider: "gemini", APIKey: "k", Model: "gemini-2.5-flash"}},
	}).EmulatedWebSearchBackend()
	require.NotNil(t, backend)
	assert.Equal(t, "gemini", backend.Provider)

	searxng := (&ChannelSettings{
		EmulateToolTypes:     []string{"web_search"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{"web_search": {Provider: "searxng", APIBase: "http://localhost:8888"}},
	}).EmulatedWebSearchBackend()
	require.NotNil(t, searxng, "searxng runs without an api key")

	require.NotNil(t, (&ChannelSettings{
		EmulateToolTypes:     []string{"image_generation"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{"image_generation": {Provider: "openai_images", APIKey: "k"}},
	}).EmulatedImageBackend())

	backends := (&ChannelSettings{
		EmulateToolTypes: []string{"web_search", "image_generation"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{
			"web_search":       {Provider: "zhipu", APIKey: "k"},
			"image_generation": {Provider: "gemini_images", APIKey: "k"},
		},
	}).EmulatedBackendsForRequest()
	require.Len(t, backends, 2)
	assert.Equal(t, "zhipu", backends["web_search"].Provider)
	assert.Equal(t, "gemini_images", backends["image_generation"].Provider)
}

func TestChannelSettingsValidateEmulatedTools(t *testing.T) {
	require.NoError(t, (*ChannelSettings)(nil).ValidateEmulatedTools())
	require.NoError(t, (&ChannelSettings{}).ValidateEmulatedTools(), "nothing emulated, nothing to validate")
	require.NoError(t, (&ChannelSettings{
		EmulateToolTypes:     []string{"web_search", "web_search_preview"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{"web_search": {Provider: "gemini", APIKey: "k"}},
	}).ValidateEmulatedTools(), "web_search_preview folds onto the web_search backend")

	assert.Error(t, (&ChannelSettings{EmulateToolTypes: []string{"web_search"}}).ValidateEmulatedTools(), "emulated without a backend")
	assert.Error(t, (&ChannelSettings{
		EmulateToolTypes:     []string{"web_search"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{"web_search": {Provider: "google", APIKey: "k"}},
	}).ValidateEmulatedTools(), "unknown provider")
	assert.Error(t, (&ChannelSettings{
		EmulateToolTypes:     []string{"web_search"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{"web_search": {Provider: "tavily"}},
	}).ValidateEmulatedTools(), "keyed provider without a key")
	assert.Error(t, (&ChannelSettings{
		EmulateToolTypes:     []string{"web_search"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{"web_search": {Provider: "searxng"}},
	}).ValidateEmulatedTools(), "searxng without api_base")
	assert.Error(t, (&ChannelSettings{
		EmulateToolTypes:     []string{"web_search"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{"web_search": {Provider: "http_json", APIBase: "https://example.com/search?q={query}"}},
	}).ValidateEmulatedTools(), "http_json without result_path")
	assert.Error(t, (&ChannelSettings{
		EmulateToolTypes:     []string{"image_generation"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{"image_generation": {Provider: "tavily", APIKey: "k"}},
	}).ValidateEmulatedTools(), "search provider on an image tool")

	require.NoError(t, (&ChannelSettings{
		EmulateToolTypes:     []string{"web_search"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{"web_search": {Provider: "http_json", APIBase: "https://example.com/search?q={query}", Extra: map[string]string{"result_path": "items"}}},
	}).ValidateEmulatedTools(), "http_json fully configured")
	require.NoError(t, (&ChannelSettings{
		EmulateToolTypes:     []string{"image_generation"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{"image_generation": {Provider: "openai_images", APIKey: "k"}},
	}).ValidateEmulatedTools())
}

func TestChannelSettingsEmulatedRecognitionAndChannel(t *testing.T) {
	require.Nil(t, (&ChannelSettings{EmulateToolTypes: []string{"image_recognition"}}).EmulatedRecognitionBackend(), "no backend configured")

	backend := (&ChannelSettings{
		EmulateToolTypes:     []string{"image_recognition"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{"image_recognition": {Provider: "gemini", APIKey: "k"}},
	}).EmulatedRecognitionBackend()
	require.NotNil(t, backend)
	assert.Equal(t, "gemini", backend.Provider)

	chBackend := (&ChannelSettings{
		EmulateToolTypes:     []string{"web_search"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{"web_search": {Provider: "channel", ChannelID: 6, Executor: "gemini"}},
	}).EmulatedWebSearchBackend()
	require.NotNil(t, chBackend)

	require.NoError(t, (&ChannelSettings{
		EmulateToolTypes:     []string{"image_recognition"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{"image_recognition": {Provider: "openai", APIKey: "k", Model: "gpt-4o-mini"}},
	}).ValidateEmulatedTools())
	require.Error(t, (&ChannelSettings{
		EmulateToolTypes:     []string{"web_search"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{"web_search": {Provider: "channel"}},
	}).ValidateEmulatedTools(), "channel without channel_id")
	require.Error(t, (&ChannelSettings{
		EmulateToolTypes:     []string{"image_recognition"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{"image_recognition": {Provider: "tavily", APIKey: "k"}},
	}).ValidateEmulatedTools(), "search provider on recognition kind")
	require.Error(t, (&ChannelSettings{
		EmulateToolTypes:     []string{"image_recognition"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{"image_recognition": {Provider: "gemini"}},
	}).ValidateEmulatedTools(), "keyed recognition provider without key")
}

func TestChannelSettingsEmulatedRefBackend(t *testing.T) {
	// ref backends are usable and pass validation (existence is checked at
	// channel save time in the root module)
	backend := (&ChannelSettings{
		EmulateToolTypes:     []string{"web_search"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{"web_search": {Ref: "g-search"}},
	}).EmulatedWebSearchBackend()
	require.NotNil(t, backend)
	assert.Equal(t, "g-search", backend.Ref)

	require.NoError(t, (&ChannelSettings{
		EmulateToolTypes: []string{"web_search"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{
			"web_search": {Ref: "g-search"},
		},
	}).ValidateEmulatedTools(), "ref backend validates without inline creds")

	// provider with channel_id needs no api_key
	require.NoError(t, (&ChannelSettings{
		EmulateToolTypes: []string{"web_search"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{
			"web_search": {Provider: EmulatedSearchProviderZhipuCodePlanSearchMCP, ChannelID: 6},
		},
	}).ValidateEmulatedTools(), "MCP executor can borrow only a channel key")

	require.NoError(t, (&ChannelSettings{
		EmulateToolTypes: []string{"image_recognition"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{
			"image_recognition": {Provider: EmulatedRecognitionProviderZhipuCodePlanVisionMCP, ChannelID: 7},
		},
	}).ValidateEmulatedTools(), "MCP vision executor can borrow only a channel key")

	require.NoError(t, (&ChannelSettings{
		EmulateToolTypes: []string{"web_search"},
		EmulatedToolBackends: map[string]EmulatedToolBackend{
			"web_search": {Provider: EmulatedSearchProviderGemini, ChannelID: 6},
		},
	}).ValidateEmulatedTools(), "relaykit preserves generic channel-backed validation independently of root credential policy")
}

func TestRepairTextToolCallModelSet(t *testing.T) {
	var nilSettings *ChannelSettings
	assert.Nil(t, nilSettings.RepairTextToolCallModelSet())

	empty := &ChannelSettings{}
	assert.Nil(t, empty.RepairTextToolCallModelSet())

	blank := &ChannelSettings{RepairTextToolCallModels: []string{"  ", ""}}
	assert.Nil(t, blank.RepairTextToolCallModelSet())

	configured := &ChannelSettings{RepairTextToolCallModels: []string{" muse-spark-1.2-contributor ", "deepseek-v4-pro"}}
	set := configured.RepairTextToolCallModelSet()
	assert.Len(t, set, 2)
	assert.Contains(t, set, "muse-spark-1.2-contributor")
	assert.Contains(t, set, "deepseek-v4-pro")
	assert.NotContains(t, set, "Muse-Spark-1.2-Contributor", "model names match case-sensitively")
}
