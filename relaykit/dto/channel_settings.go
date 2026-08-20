package dto

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/relaykit/types"
)

type ChannelSettings struct {
	ForceFormat            bool   `json:"force_format,omitempty"`
	ThinkingToContent      bool   `json:"thinking_to_content,omitempty"`
	Proxy                  string `json:"proxy"`
	PassThroughBodyEnabled bool   `json:"pass_through_body_enabled,omitempty"`
	SystemPrompt           string `json:"system_prompt,omitempty"`
	SystemPromptOverride   bool   `json:"system_prompt_override,omitempty"`
	// HTTPProtocol controls outbound HTTP version negotiation for this channel.
	// Accepted values: "", "auto" (default), "http1".
	HTTPProtocol string `json:"http_protocol,omitempty"`
	// HTTP2ConnectionShards spreads HTTP/2 traffic across N independent transports
	// (1-8). Zero/unset means 1. Ignored when HTTPProtocol is "http1".
	HTTP2ConnectionShards int `json:"http2_connection_shards,omitempty"`
	// StripToolTypes lists OpenAI Responses tool types (e.g. "web_search",
	// "image_generation") that are removed from requests before they are sent to
	// this channel. Some OpenAI-compatible upstreams expose /v1/responses but
	// reject OpenAI hosted tool carriers with a 400; strip those tool types here
	// so clients like Codex that always declare them keep working. Tool-choice
	// references and include entries tied to a stripped tool are dropped too.
	StripToolTypes []string `json:"strip_tool_types,omitempty"`
	// BridgeToolTypes lists OpenAI Responses tool types (e.g. "custom",
	// "namespace", "tool_search") that are rewritten into ordinary function
	// tools before the request reaches this channel, and restored to their
	// native kinds on the way back. Upstreams that only accept function tools
	// (e.g. opencode zen/go) thereby keep working with clients that rely on
	// client-executed tool kinds, such as Codex's apply_patch and MCP tools.
	// The tools in question are executed by the client, so no execution
	// capability is needed on the gateway.
	BridgeToolTypes []string `json:"bridge_tool_types,omitempty"`
	// EmulateToolTypes lists hosted OpenAI Responses tool types (currently
	// "web_search" / "web_search_preview" and "image_generation") that this
	// gateway executes itself: the declaration is rewritten into a function
	// tool for the upstream, and when the model calls it the gateway runs the
	// configured backend for that tool type (EmulatedToolBackends), feeds the
	// results back and iterates until the final answer. Clients see native
	// web_search_call / image_generation_call items without any client-side
	// configuration.
	EmulateToolTypes []string `json:"emulate_tool_types,omitempty"`
	// EmulatedToolBackends configures the executor per emulated tool type
	// ("web_search" and "image_generation" today).
	EmulatedToolBackends map[string]EmulatedToolBackend `json:"emulated_tool_backends,omitempty"`
}

// Web search executor providers for EmulatedToolBackend.Provider when the
// emulated tool type is web_search.
const (
	EmulatedSearchProviderGemini   = "gemini"   // Google AI Studio "Grounding with Google Search"
	EmulatedSearchProviderZhipu    = "zhipu"    // Zhipu (GLM) web_search API; Model selects search_engine
	EmulatedSearchProviderTavily   = "tavily"   // api.tavily.com
	EmulatedSearchProviderBrave    = "brave"    // Brave Search API
	EmulatedSearchProviderBocha    = "bocha"    // Bocha (博查) web search
	EmulatedSearchProviderSearXNG  = "searxng"  // self-hosted SearXNG, keyless
	EmulatedSearchProviderHTTPJSON = "http_json" // generic JSON endpoint, api_base template + Extra
)

// Image generation executor providers for EmulatedToolBackend.Provider when
// the emulated tool type is image_generation.
const (
	EmulatedImageProviderOpenAI = "openai_images" // any OpenAI-compatible /v1/images/generations endpoint
	EmulatedImageProviderGemini = "gemini_images" // Gemini image output via generateContent
)

// Emulated tool types that can be configured on a channel.
const (
	EmulatedToolTypeWebSearch = "web_search"
	EmulatedToolTypeImage     = "image_generation"
)

// EmulatedToolBackend is the executor configuration for one emulated hosted
// tool. Which providers are valid depends on the tool type (web_search vs
// image_generation); api_base exists so tests can point executors at a stub.
type EmulatedToolBackend struct {
	Provider string `json:"provider,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
	Model    string `json:"model,omitempty"`
	APIBase  string `json:"api_base,omitempty"`
	// Extra carries provider-specific options. Today only the http_json
	// search provider uses it: "request_body" (JSON template with {query})
	// and "result_path" (gjson path into the response).
	Extra map[string]string `json:"extra,omitempty"`
}

func emulatedSearchProviders() map[string]struct{} {
	return map[string]struct{}{
		EmulatedSearchProviderGemini:   {},
		EmulatedSearchProviderZhipu:    {},
		EmulatedSearchProviderTavily:   {},
		EmulatedSearchProviderBrave:    {},
		EmulatedSearchProviderBocha:    {},
		EmulatedSearchProviderSearXNG:  {},
		EmulatedSearchProviderHTTPJSON: {},
	}
}

func emulatedImageProviders() map[string]struct{} {
	return map[string]struct{}{
		EmulatedImageProviderOpenAI: {},
		EmulatedImageProviderGemini: {},
	}
}

// backendUsable reports whether the backend can execute. SearXNG and the
// generic http_json endpoint can run without an API key; every other provider
// authenticates with one.
func backendUsable(toolType string, backend *EmulatedToolBackend) bool {
	if backend == nil {
		return false
	}
	switch toolType {
	case EmulatedToolTypeWebSearch:
		if _, ok := emulatedSearchProviders()[strings.ToLower(strings.TrimSpace(backend.Provider))]; !ok {
			return false
		}
	case EmulatedToolTypeImage:
		if _, ok := emulatedImageProviders()[strings.ToLower(strings.TrimSpace(backend.Provider))]; !ok {
			return false
		}
	default:
		return false
	}
	return backend.APIKey != "" ||
		strings.EqualFold(backend.Provider, EmulatedSearchProviderSearXNG) ||
		strings.EqualFold(backend.Provider, EmulatedSearchProviderHTTPJSON)
}

// StripToolTypeSet returns the normalized strip_tool_types blacklist as a set.
// Nil means no stripping is configured.
func (s *ChannelSettings) StripToolTypeSet() map[string]struct{} {
	if s == nil {
		return nil
	}
	return normalizeToolTypeSet(s.StripToolTypes)
}

// BridgeToolTypeSet returns the normalized bridge_tool_types set.
// Nil means no bridging is configured.
func (s *ChannelSettings) BridgeToolTypeSet() map[string]struct{} {
	if s == nil {
		return nil
	}
	return normalizeToolTypeSet(s.BridgeToolTypes)
}

// EmulateToolTypeSet returns the normalized emulate_tool_types set, with
// web_search_preview folded onto web_search (they share one executor).
// Nil means no emulation is configured.
func (s *ChannelSettings) EmulateToolTypeSet() map[string]struct{} {
	if s == nil {
		return nil
	}
	set := normalizeToolTypeSet(s.EmulateToolTypes)
	if set == nil {
		return nil
	}
	if _, ok := set["web_search_preview"]; ok {
		delete(set, "web_search_preview")
		set["web_search"] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// EmulatedWebSearchBackend returns the search backend configuration when
// web_search emulation is enabled for this channel.
func (s *ChannelSettings) EmulatedWebSearchBackend() *EmulatedToolBackend {
	if s == nil {
		return nil
	}
	if _, ok := s.EmulateToolTypeSet()[EmulatedToolTypeWebSearch]; !ok {
		return nil
	}
	backend := s.EmulatedToolBackends[EmulatedToolTypeWebSearch]
	if !backendUsable(EmulatedToolTypeWebSearch, &backend) {
		return nil
	}
	return &backend
}

// EmulatedImageBackend returns the image generation backend configuration
// when image_generation emulation is enabled for this channel.
func (s *ChannelSettings) EmulatedImageBackend() *EmulatedToolBackend {
	if s == nil {
		return nil
	}
	if _, ok := s.EmulateToolTypeSet()[EmulatedToolTypeImage]; !ok {
		return nil
	}
	backend := s.EmulatedToolBackends[EmulatedToolTypeImage]
	if !backendUsable(EmulatedToolTypeImage, &backend) {
		return nil
	}
	return &backend
}

// EmulatedBackendsForRequest returns the executor backends keyed by tool type
// for every emulated tool this channel configured. Empty map means the request
// must not run through the emulation loop.
func (s *ChannelSettings) EmulatedBackendsForRequest() map[string]*EmulatedToolBackend {
	if s == nil {
		return nil
	}
	var backends map[string]*EmulatedToolBackend
	if backend := s.EmulatedWebSearchBackend(); backend != nil {
		if backends == nil {
			backends = make(map[string]*EmulatedToolBackend, 2)
		}
		backends[EmulatedToolTypeWebSearch] = backend
	}
	if backend := s.EmulatedImageBackend(); backend != nil {
		if backends == nil {
			backends = make(map[string]*EmulatedToolBackend, 2)
		}
		backends[EmulatedToolTypeImage] = backend
	}
	return backends
}

// ValidateEmulatedTools validates save-time emulated tool settings: every
// enabled emulated tool needs a known, usable backend.
func (s *ChannelSettings) ValidateEmulatedTools() error {
	if s == nil {
		return nil
	}
	for _, toolType := range []string{EmulatedToolTypeWebSearch, "web_search_preview", EmulatedToolTypeImage} {
		if _, emulated := s.EmulateToolTypeSet()[toolType]; !emulated {
			continue
		}
		backend, configured := s.EmulatedToolBackends[toolType]
		if !configured {
			// web_search_preview folds onto the web_search executor
			if toolType == "web_search_preview" {
				backend, configured = s.EmulatedToolBackends[EmulatedToolTypeWebSearch]
			}
		}
		if !configured {
			return fmt.Errorf("emulate_tool_types includes %q but emulated_tool_backends.%s is not configured", toolType, toolType)
		}
		if strings.TrimSpace(backend.Provider) == "" {
			return fmt.Errorf("emulated_tool_backends.%s.provider is required", toolType)
		}
		if _, ok := emulatedSearchProviders()[strings.ToLower(strings.TrimSpace(backend.Provider))]; toolType == EmulatedToolTypeWebSearch && !ok {
			return fmt.Errorf("unknown web search provider %q (supported: gemini, zhipu, tavily, brave, bocha, searxng, http_json)", backend.Provider)
		}
		if _, ok := emulatedImageProviders()[strings.ToLower(strings.TrimSpace(backend.Provider))]; toolType == EmulatedToolTypeImage && !ok {
			return fmt.Errorf("unknown image generation provider %q (supported: openai_images, gemini_images)", backend.Provider)
		}
		if backend.APIKey == "" && !strings.EqualFold(backend.Provider, EmulatedSearchProviderSearXNG) && !strings.EqualFold(backend.Provider, EmulatedSearchProviderHTTPJSON) {
			return fmt.Errorf("emulated_tool_backends.%s.api_key is required for provider %q", toolType, backend.Provider)
		}
		if strings.EqualFold(backend.Provider, EmulatedSearchProviderSearXNG) && strings.TrimSpace(backend.APIBase) == "" {
			return fmt.Errorf("emulated_tool_backends.%s.api_base is required for searxng (your instance URL)", toolType)
		}
		if strings.EqualFold(backend.Provider, EmulatedSearchProviderHTTPJSON) {
			if strings.TrimSpace(backend.APIBase) == "" || !strings.Contains(backend.APIBase, "{query}") {
				return fmt.Errorf("emulated_tool_backends.%s.api_base must be a URL template containing {query} for http_json", toolType)
			}
			if strings.TrimSpace(backend.Extra["result_path"]) == "" {
				return fmt.Errorf("emulated_tool_backends.%s.extra.result_path is required for http_json", toolType)
			}
		}
	}
	return nil
}

func normalizeToolTypeSet(toolTypes []string) map[string]struct{} {
	if len(toolTypes) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(toolTypes))
	for _, toolType := range toolTypes {
		toolType = strings.ToLower(strings.TrimSpace(toolType))
		if toolType == "" {
			continue
		}
		set[toolType] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

const (
	HTTPProtocolAuto         = "auto"
	HTTPProtocolHTTP1        = "http1"
	MaxHTTP2ConnectionShards = 8
)

// ValidateHTTPTransport validates save-time HTTP transport channel settings.
func (s *ChannelSettings) ValidateHTTPTransport() error {
	if s == nil {
		return nil
	}
	protocol := strings.ToLower(strings.TrimSpace(s.HTTPProtocol))
	switch protocol {
	case "", HTTPProtocolAuto, HTTPProtocolHTTP1:
	default:
		return fmt.Errorf("invalid http_protocol: %s", s.HTTPProtocol)
	}
	if s.HTTP2ConnectionShards < 0 || s.HTTP2ConnectionShards > MaxHTTP2ConnectionShards {
		return fmt.Errorf("invalid http2_connection_shards: %d", s.HTTP2ConnectionShards)
	}
	if protocol == HTTPProtocolHTTP1 && s.HTTP2ConnectionShards > 1 {
		return fmt.Errorf("http2_connection_shards must be 1 when http_protocol is http1")
	}
	return nil
}

type VertexKeyType string

const (
	VertexKeyTypeJSON   VertexKeyType = "json"
	VertexKeyTypeAPIKey VertexKeyType = "api_key"
)

type AwsKeyType string

const (
	AwsKeyTypeAKSK   AwsKeyType = "ak_sk" // 默认
	AwsKeyTypeApiKey AwsKeyType = "api_key"
)

type ChannelOtherSettings struct {
	AzureResponsesVersion                 string                `json:"azure_responses_version,omitempty"`
	VertexKeyType                         VertexKeyType         `json:"vertex_key_type,omitempty"` // "json" or "api_key"
	OpenRouterEnterprise                  *bool                 `json:"openrouter_enterprise,omitempty"`
	ClaudeBetaQuery                       bool                  `json:"claude_beta_query,omitempty"`          // Claude 渠道是否强制追加 ?beta=true
	AllowServiceTier                      bool                  `json:"allow_service_tier,omitempty"`         // 是否允许 service_tier 透传（默认过滤以避免额外计费）
	AllowInferenceGeo                     bool                  `json:"allow_inference_geo,omitempty"`        // 是否允许 inference_geo 透传（仅 Claude，默认过滤以满足数据驻留合规
	AllowSpeed                            bool                  `json:"allow_speed,omitempty"`                // 是否允许 speed 透传（仅 Claude，默认过滤以避免意外切换推理速度模式）
	AllowSafetyIdentifier                 bool                  `json:"allow_safety_identifier,omitempty"`    // 是否允许 safety_identifier 透传（默认过滤以保护用户隐私）
	DisableStore                          bool                  `json:"disable_store,omitempty"`              // 是否禁用 store 透传（默认允许透传，禁用后可能导致 Codex 无法使用）
	AllowIncludeObfuscation               bool                  `json:"allow_include_obfuscation,omitempty"`  // 是否允许 stream_options.include_obfuscation 透传（默认过滤以避免关闭流混淆保护）
	DisableTaskPollingSleep               bool                  `json:"disable_task_polling_sleep,omitempty"` // 是否跳过异步任务轮询间隔
	AwsKeyType                            AwsKeyType            `json:"aws_key_type,omitempty"`
	UpstreamModelUpdateCheckEnabled       bool                  `json:"upstream_model_update_check_enabled,omitempty"`        // 是否检测上游模型更新
	UpstreamModelUpdateAutoSyncEnabled    bool                  `json:"upstream_model_update_auto_sync_enabled,omitempty"`    // 是否自动同步上游模型更新
	UpstreamModelUpdateLastCheckTime      int64                 `json:"upstream_model_update_last_check_time,omitempty"`      // 上次检测时间
	UpstreamModelUpdateLastDetectedModels []string              `json:"upstream_model_update_last_detected_models,omitempty"` // 上次检测到的可加入模型
	UpstreamModelUpdateLastRemovedModels  []string              `json:"upstream_model_update_last_removed_models,omitempty"`  // 上次检测到的可删除模型
	UpstreamModelUpdateIgnoredModels      []string              `json:"upstream_model_update_ignored_models,omitempty"`       // 手动忽略的模型
	AdvancedCustom                        *AdvancedCustomConfig `json:"advanced_custom,omitempty"`
}

func (s *ChannelOtherSettings) IsOpenRouterEnterprise() bool {
	if s == nil || s.OpenRouterEnterprise == nil {
		return false
	}
	return *s.OpenRouterEnterprise
}

const (
	advancedCustomConverterNone                        = "none"
	advancedCustomConverterClaudeMessagesToOpenAIChat  = "anthropic_messages_to_openai_chat_completions"
	advancedCustomConverterOpenAIChatToClaudeMessages  = "openai_chat_completions_to_anthropic_messages"
	advancedCustomConverterOpenAIChatToOpenAIResponses = "openai_chat_completions_to_openai_responses"
	advancedCustomConverterOpenAIResponsesToOpenAIChat = "openai_responses_to_openai_chat_completions"
	advancedCustomConverterOpenAIResponsesToGemini     = "openai_responses_to_gemini_generate_content"
	advancedCustomConverterGeminiContentToOpenAIChat   = "gemini_generate_content_to_openai_chat_completions"
	advancedCustomConverterOpenAIChatToGeminiContent   = "openai_chat_completions_to_gemini_generate_content"
)

const (
	AdvancedCustomAuthTypeNone   = "none"
	AdvancedCustomAuthTypeHeader = "header"
	AdvancedCustomAuthTypeQuery  = "query"
)

type AdvancedCustomConfig struct {
	Routes []AdvancedCustomRoute `json:"advanced_routes,omitempty"`
}

type AdvancedCustomRoute struct {
	IncomingPath string                   `json:"incoming_path,omitempty"`
	UpstreamPath string                   `json:"upstream_path,omitempty"`
	Converter    string                   `json:"converter,omitempty"`
	Models       []string                 `json:"models,omitempty"`
	Auth         *AdvancedCustomRouteAuth `json:"auth,omitempty"`
}

type AdvancedCustomRouteAuth struct {
	Type  string `json:"type,omitempty"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

const (
	advancedCustomModelPlaceholder = "{model}"
	advancedCustomModelRegexPrefix = "re:"
)

const (
	advancedCustomEndpointPathOpenAIChat             = "/v1/chat/completions"
	advancedCustomEndpointPathOpenAIResponses        = "/v1/responses"
	advancedCustomEndpointPathOpenAIResponsesCompact = "/v1/responses/compact"
	advancedCustomEndpointPathOpenAIAlphaSearch      = "/v1/alpha/search"
	advancedCustomEndpointPathClaudeMessages         = "/v1/messages"
	advancedCustomEndpointPathJinaRerank             = "/v1/rerank"
	advancedCustomEndpointPathImageGeneration        = "/v1/images/generations"
	advancedCustomEndpointPathEmbeddings             = "/v1/embeddings"
)

const (
	// AdvancedCustomModelListPath identifies the optional OpenAI Models discovery route.
	AdvancedCustomModelListPath = "/v1/models"
	// AdvancedCustomBalancePath identifies the optional balance lookup route used by channel management.
	AdvancedCustomBalancePath = "/v1/dashboard/billing/credit_grants"
)

// MatchPath returns the first route whose IncomingPath matches requestPath.
// Matching mirrors the relay adaptor: exact match, {model} placeholder, and
// :generateContent <-> :streamGenerateContent equivalence.
func (c *AdvancedCustomConfig) MatchPath(requestPath string) (AdvancedCustomRoute, bool) {
	if c == nil {
		return AdvancedCustomRoute{}, false
	}
	for _, route := range c.Routes {
		if matchAdvancedCustomIncomingPath(strings.TrimSpace(route.IncomingPath), requestPath) {
			return route, true
		}
	}
	return AdvancedCustomRoute{}, false
}

// MatchPathForModel returns the first route whose IncomingPath and Models match.
// An empty Models list is a catch-all fallback for that incoming path.
func (c *AdvancedCustomConfig) MatchPathForModel(requestPath string, model string) (AdvancedCustomRoute, bool) {
	if c == nil {
		return AdvancedCustomRoute{}, false
	}
	model = strings.TrimSpace(model)
	for _, route := range c.Routes {
		if matchAdvancedCustomIncomingPath(strings.TrimSpace(route.IncomingPath), requestPath) &&
			matchAdvancedCustomRouteModel(route.Models, model) {
			return route, true
		}
	}
	return AdvancedCustomRoute{}, false
}

// ModelListRoute returns the explicitly configured OpenAI Models discovery route.
// Template routes that merely happen to match /v1/models are not discovery routes.
func (c *AdvancedCustomConfig) ModelListRoute() (AdvancedCustomRoute, bool) {
	if c == nil {
		return AdvancedCustomRoute{}, false
	}
	for _, route := range c.Routes {
		if strings.TrimSpace(route.IncomingPath) == AdvancedCustomModelListPath {
			return route, true
		}
	}
	return AdvancedCustomRoute{}, false
}

// BalanceRoute returns the explicitly configured channel-management balance route.
func (c *AdvancedCustomConfig) BalanceRoute() (AdvancedCustomRoute, bool) {
	if c == nil {
		return AdvancedCustomRoute{}, false
	}
	for _, route := range c.Routes {
		if strings.TrimSpace(route.IncomingPath) == AdvancedCustomBalancePath {
			return route, true
		}
	}
	return AdvancedCustomRoute{}, false
}

// SupportsPath reports whether any route matches requestPath.
func (c *AdvancedCustomConfig) SupportsPath(requestPath string) bool {
	_, ok := c.MatchPath(requestPath)
	return ok
}

// SupportsPathForModel reports whether any route matches requestPath and model.
func (c *AdvancedCustomConfig) SupportsPathForModel(requestPath string, model string) bool {
	_, ok := c.MatchPathForModel(requestPath, model)
	return ok
}

func (c *AdvancedCustomConfig) SupportedEndpointTypesForModel(model string) []types.EndpointType {
	if c == nil {
		return nil
	}
	model = strings.TrimSpace(model)
	endpoints := make([]types.EndpointType, 0, len(c.Routes))
	seen := make(map[types.EndpointType]struct{}, len(c.Routes))
	for _, route := range c.Routes {
		if !matchAdvancedCustomRouteModel(route.Models, model) {
			continue
		}
		endpointType, ok := advancedCustomEndpointTypeFromIncomingPath(strings.TrimSpace(route.IncomingPath))
		if !ok {
			continue
		}
		if _, exists := seen[endpointType]; exists {
			continue
		}
		seen[endpointType] = struct{}{}
		endpoints = append(endpoints, endpointType)
	}
	return endpoints
}

func advancedCustomEndpointTypeFromIncomingPath(incomingPath string) (types.EndpointType, bool) {
	switch incomingPath {
	case advancedCustomEndpointPathOpenAIChat:
		return types.EndpointTypeOpenAI, true
	case advancedCustomEndpointPathOpenAIResponses:
		return types.EndpointTypeOpenAIResponse, true
	case advancedCustomEndpointPathOpenAIResponsesCompact:
		return types.EndpointTypeOpenAIResponseCompact, true
	case advancedCustomEndpointPathOpenAIAlphaSearch:
		return types.EndpointTypeOpenAIAlphaSearch, true
	case advancedCustomEndpointPathClaudeMessages:
		return types.EndpointTypeAnthropic, true
	case advancedCustomEndpointPathJinaRerank:
		return types.EndpointTypeJinaRerank, true
	case advancedCustomEndpointPathImageGeneration:
		return types.EndpointTypeImageGeneration, true
	case advancedCustomEndpointPathEmbeddings:
		return types.EndpointTypeEmbeddings, true
	default:
		if isAdvancedCustomGeminiIncomingPath(incomingPath) {
			return types.EndpointTypeGemini, true
		}
		return "", false
	}
}

func isAdvancedCustomGeminiIncomingPath(incomingPath string) bool {
	if !strings.HasPrefix(incomingPath, "/v1beta/models/") {
		return false
	}
	return strings.Contains(incomingPath, ":generateContent") || strings.Contains(incomingPath, ":streamGenerateContent")
}

func matchAdvancedCustomRouteModel(models []string, model string) bool {
	normalizedModels := normalizeAdvancedCustomRouteModels(models)
	if len(normalizedModels) == 0 {
		return true
	}
	for _, allowedModel := range normalizedModels {
		if matchAdvancedCustomRouteModelRule(allowedModel, model) {
			return true
		}
	}
	return false
}

// advancedCustomModelRegexCache caches compiled route model patterns. Route model
// matching runs on the request hot path (distributor affinity, ability filtering,
// channel cache filtering, adaptor resolve), so patterns must not be recompiled per
// request. Invalid patterns are cached as nil to avoid recompiling them as well.
var advancedCustomModelRegexCache sync.Map // pattern string -> *regexp.Regexp (nil when invalid)

func compileAdvancedCustomModelRegex(pattern string) *regexp.Regexp {
	if cached, ok := advancedCustomModelRegexCache.Load(pattern); ok {
		re, _ := cached.(*regexp.Regexp)
		return re
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		re = nil
	}
	advancedCustomModelRegexCache.Store(pattern, re)
	return re
}

func matchAdvancedCustomRouteModelRule(rule string, model string) bool {
	if !strings.HasPrefix(rule, advancedCustomModelRegexPrefix) {
		return rule == model
	}
	pattern := strings.TrimPrefix(rule, advancedCustomModelRegexPrefix)
	if pattern == "" {
		return false
	}
	re := compileAdvancedCustomModelRegex(pattern)
	return re != nil && re.MatchString(model)
}

func matchAdvancedCustomIncomingPath(configuredPath string, requestPath string) bool {
	if matchAdvancedCustomIncomingPathTemplate(configuredPath, requestPath) {
		return true
	}
	if strings.Contains(configuredPath, ":generateContent") {
		streamPath := strings.Replace(configuredPath, ":generateContent", ":streamGenerateContent", 1)
		return matchAdvancedCustomIncomingPathTemplate(streamPath, requestPath)
	}
	return false
}

func matchAdvancedCustomIncomingPathTemplate(configuredPath string, requestPath string) bool {
	if !strings.Contains(configuredPath, advancedCustomModelPlaceholder) {
		return configuredPath == requestPath
	}

	parts := strings.Split(configuredPath, advancedCustomModelPlaceholder)
	if len(parts) != 2 {
		return false
	}
	if !strings.HasPrefix(requestPath, parts[0]) || !strings.HasSuffix(requestPath, parts[1]) {
		return false
	}

	model := strings.TrimSuffix(strings.TrimPrefix(requestPath, parts[0]), parts[1])
	return model != "" && !strings.Contains(model, "/")
}

func IsAdvancedCustomConverterAllowed(converter string) bool {
	switch converter {
	case advancedCustomConverterNone,
		advancedCustomConverterClaudeMessagesToOpenAIChat,
		advancedCustomConverterOpenAIChatToClaudeMessages,
		advancedCustomConverterOpenAIChatToOpenAIResponses,
		advancedCustomConverterOpenAIResponsesToOpenAIChat,
		advancedCustomConverterOpenAIResponsesToGemini,
		advancedCustomConverterGeminiContentToOpenAIChat,
		advancedCustomConverterOpenAIChatToGeminiContent:
		return true
	default:
		return false
	}
}

func (c *AdvancedCustomConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("advanced_custom is required")
	}
	if len(c.Routes) == 0 {
		return fmt.Errorf("advanced_custom requires at least one route")
	}

	paths := make(map[string]*advancedCustomPathModelState, len(c.Routes))
	modelListRouteIndex := -1
	balanceRouteIndex := -1
	for i := range c.Routes {
		route := c.Routes[i]
		route.IncomingPath = strings.TrimSpace(route.IncomingPath)
		upstreamPath := strings.TrimSpace(route.UpstreamPath)
		route.Converter = strings.TrimSpace(route.Converter)
		if route.Converter == "" {
			route.Converter = advancedCustomConverterNone
		}

		if route.IncomingPath == "" {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].incoming_path is required", i)
		}
		if !strings.HasPrefix(route.IncomingPath, "/") {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].incoming_path must start with /", i)
		}
		if strings.Contains(route.IncomingPath, "?") {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].incoming_path must not include query", i)
		}
		if route.IncomingPath == AdvancedCustomModelListPath || route.IncomingPath == AdvancedCustomBalancePath {
			managementRouteName := route.IncomingPath
			previousIndex := modelListRouteIndex
			if route.IncomingPath == AdvancedCustomBalancePath {
				previousIndex = balanceRouteIndex
			}
			if previousIndex >= 0 {
				return fmt.Errorf("advanced_custom.advanced_routes[%d] duplicates the %s route at advanced_routes[%d]", i, managementRouteName, previousIndex)
			}
			if route.IncomingPath == AdvancedCustomModelListPath {
				modelListRouteIndex = i
			} else {
				balanceRouteIndex = i
			}
			if len(normalizeAdvancedCustomRouteModels(route.Models)) > 0 {
				return fmt.Errorf("advanced_custom.advanced_routes[%d].models must be empty for %s", i, managementRouteName)
			}
			if route.Converter != advancedCustomConverterNone {
				return fmt.Errorf("advanced_custom.advanced_routes[%d].converter must be none for %s", i, managementRouteName)
			}
			if strings.Contains(upstreamPath, advancedCustomModelPlaceholder) {
				return fmt.Errorf("advanced_custom.advanced_routes[%d].upstream_path must not contain %s for %s", i, advancedCustomModelPlaceholder, managementRouteName)
			}
		}
		if err := validateAdvancedCustomRouteModels(i, route.IncomingPath, route.Models, paths); err != nil {
			return err
		}

		if upstreamPath == "" {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].upstream_path is required", i)
		}
		if err := validateAdvancedCustomUpstreamTarget(i, upstreamPath); err != nil {
			return err
		}

		if !IsAdvancedCustomConverterAllowed(route.Converter) {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].converter is not registered: %s", i, route.Converter)
		}
		if err := validateAdvancedCustomConverterPath(i, route.IncomingPath, route.Converter); err != nil {
			return err
		}
		if err := validateAdvancedCustomRouteAuth(i, route.Auth); err != nil {
			return err
		}
	}

	return nil
}

type advancedCustomPathModelState struct {
	catchAllIndex int
	modelIndexes  map[string]int
}

func validateAdvancedCustomRouteModels(index int, incomingPath string, models []string, paths map[string]*advancedCustomPathModelState) error {
	state := paths[incomingPath]
	if state == nil {
		state = &advancedCustomPathModelState{
			catchAllIndex: -1,
			modelIndexes:  make(map[string]int),
		}
		paths[incomingPath] = state
	}

	normalizedModels := normalizeAdvancedCustomRouteModels(models)
	if len(normalizedModels) == 0 {
		if state.catchAllIndex >= 0 {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].models catch-all already exists for incoming_path: %s", index, incomingPath)
		}
		state.catchAllIndex = index
		return nil
	}

	if state.catchAllIndex >= 0 {
		return fmt.Errorf("advanced_custom.advanced_routes[%d].models catch-all route must be last for incoming_path: %s", index, incomingPath)
	}

	seenInRoute := make(map[string]struct{}, len(normalizedModels))
	for _, model := range normalizedModels {
		if err := validateAdvancedCustomRouteModelRule(index, incomingPath, model); err != nil {
			return err
		}
		if _, exists := seenInRoute[model]; exists {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].models contains duplicate model for incoming_path %s: %s", index, incomingPath, model)
		}
		seenInRoute[model] = struct{}{}
		if existingIndex, exists := state.modelIndexes[model]; exists {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].models overlaps with advanced_routes[%d] for incoming_path %s: %s", index, existingIndex, incomingPath, model)
		}
		state.modelIndexes[model] = index
	}
	return nil
}

func validateAdvancedCustomRouteModelRule(index int, incomingPath string, model string) error {
	if !strings.HasPrefix(model, advancedCustomModelRegexPrefix) {
		return nil
	}
	pattern := strings.TrimPrefix(model, advancedCustomModelRegexPrefix)
	if pattern == "" {
		return fmt.Errorf("advanced_custom.advanced_routes[%d].models regex is empty for incoming_path %s: %s", index, incomingPath, model)
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("advanced_custom.advanced_routes[%d].models regex is invalid for incoming_path %s: %s", index, incomingPath, model)
	}
	return nil
}

func normalizeAdvancedCustomRouteModels(models []string) []string {
	if len(models) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model != "" {
			normalized = append(normalized, model)
		}
	}
	return normalized
}

func validateAdvancedCustomUpstreamTarget(index int, upstreamPath string) error {
	if strings.HasPrefix(upstreamPath, "/") {
		if strings.HasPrefix(upstreamPath, "//") {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].upstream_path must be a full URL or a path starting with /", index)
		}
		return nil
	}

	parsedURL, err := url.Parse(upstreamPath)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return fmt.Errorf("advanced_custom.advanced_routes[%d].upstream_path must be a full URL or a path starting with /", index)
	}
	if !strings.EqualFold(parsedURL.Scheme, "http") && !strings.EqualFold(parsedURL.Scheme, "https") {
		return fmt.Errorf("advanced_custom.advanced_routes[%d].upstream_path must use http or https", index)
	}
	return nil
}

func validateAdvancedCustomConverterPath(index int, incomingPath string, converter string) error {
	if incomingPath == advancedCustomEndpointPathOpenAIAlphaSearch {
		if converter == advancedCustomConverterNone {
			return nil
		}
		return fmt.Errorf("advanced_custom.advanced_routes[%d].converter does not match incoming_path: %s", index, converter)
	}
	switch converter {
	case advancedCustomConverterNone:
		return nil
	case advancedCustomConverterClaudeMessagesToOpenAIChat:
		if incomingPath == "/v1/messages" {
			return nil
		}
	case advancedCustomConverterOpenAIChatToClaudeMessages,
		advancedCustomConverterOpenAIChatToOpenAIResponses,
		advancedCustomConverterOpenAIChatToGeminiContent:
		if incomingPath == "/v1/chat/completions" {
			return nil
		}
	case advancedCustomConverterOpenAIResponsesToOpenAIChat:
		if incomingPath == "/v1/responses" {
			return nil
		}
	case advancedCustomConverterOpenAIResponsesToGemini:
		if incomingPath == "/v1/responses" {
			return nil
		}
	case advancedCustomConverterGeminiContentToOpenAIChat:
		if strings.Contains(incomingPath, ":generateContent") || strings.Contains(incomingPath, ":streamGenerateContent") {
			return nil
		}
	}
	return fmt.Errorf("advanced_custom.advanced_routes[%d].converter does not match incoming_path: %s", index, converter)
}

func validateAdvancedCustomRouteAuth(index int, auth *AdvancedCustomRouteAuth) error {
	if auth == nil {
		return nil
	}
	authType := strings.TrimSpace(auth.Type)
	switch authType {
	case AdvancedCustomAuthTypeNone:
		return nil
	case AdvancedCustomAuthTypeHeader, AdvancedCustomAuthTypeQuery:
		if strings.TrimSpace(auth.Name) == "" {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].auth.name is required", index)
		}
		if strings.TrimSpace(auth.Value) == "" {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].auth.value is required", index)
		}
		return nil
	default:
		return fmt.Errorf("advanced_custom.advanced_routes[%d].auth.type is invalid: %s", index, auth.Type)
	}
}
