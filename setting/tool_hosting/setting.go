package tool_hosting

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// ---------------------------------------------------------------------------
// Global tool hosting: a registry of named tool-executor providers.
//
// DB key (option):
//   tool_hosting.providers → JSON object { "<provider name>": ToolHostingProvider }
//
// A provider names the executor Type for one hosted tool Kind (web_search,
// image_recognition, image_generation). Types that execute through a model
// (gemini grounding, gemini/openai vision, image generation) may borrow an
// existing channel's credentials via ChannelID and pick the Model to use;
// plain API providers (tavily, zhipu, brave, bocha) just carry an APIKey.
// Channels reference a provider by name from their emulated_tool_backends
// (EmulatedToolBackend.Ref); enabling tool substitution stays at the channel.
// ---------------------------------------------------------------------------

const ProvidersOptionKey = "tool_hosting.providers"

// Tool kind a hosted-tool provider can execute.
type ToolKind string

const (
	KindWebSearch       ToolKind = "web_search"
	KindRecognition     ToolKind = "image_recognition"
	KindImageGeneration ToolKind = "image_generation"
)

// ToolHostingProvider configures one named tool executor.
type ToolHostingProvider struct {
	Kind ToolKind `json:"kind"`
	// Type is the executor: gemini/zhipu/tavily/brave/bocha/searxng/http_json
	// for web_search; gemini/openai for image_recognition;
	// openai_images/gemini_images for image_generation.
	Type string `json:"type"`
	// ChannelID optionally borrows this channel's key/base_url as the
	// credentials (resolved at execution time). Used with model-executing
	// types, e.g. gemini grounding through a channel's model access.
	ChannelID int64 `json:"channel_id,omitempty"`
	// Model is the executor model (gemini grounding / vision / image model;
	// for zhipu it selects the search engine). Optional — providers pick a
	// default when empty.
	Model string `json:"model,omitempty"`
	// APIKey authenticates providers that are not channel-backed.
	APIKey string `json:"api_key,omitempty"`
	// APIBase overrides the provider's default endpoint (tests point it at a
	// stub).
	APIBase string `json:"api_base,omitempty"`
	// Extra carries provider-specific options (http_json request_body /
	// result_path).
	Extra map[string]string `json:"extra,omitempty"`
}

// ToolHostingSetting is managed by config.GlobalConfig.Register.
type ToolHostingSetting struct {
	Providers map[string]ToolHostingProvider `json:"providers"`
}

var toolHostingSetting = ToolHostingSetting{
	Providers: make(map[string]ToolHostingProvider),
}

func init() {
	config.GlobalConfig.Register("tool_hosting", &toolHostingSetting)
	RebuildToolHostingIndex()
}

// ---------------------------------------------------------------------------
// Precomputed lookup index (atomic pointer, lock-free on the request path)
// ---------------------------------------------------------------------------

type toolHostingIndex struct {
	providers map[string]ToolHostingProvider
}

var currentIndex atomic.Pointer[toolHostingIndex]

// RebuildToolHostingIndex rebuilds the lookup index from the current config.
// Called on init and after config updates.
func RebuildToolHostingIndex() {
	providers := make(map[string]ToolHostingProvider, len(toolHostingSetting.Providers))
	for name, provider := range toolHostingSetting.Providers {
		providers[strings.TrimSpace(name)] = provider
	}
	currentIndex.Store(&toolHostingIndex{providers: providers})
}

func loadIndex() *toolHostingIndex {
	idx := currentIndex.Load()
	if idx == nil {
		RebuildToolHostingIndex()
		idx = currentIndex.Load()
	}
	return idx
}

// GetProvider returns a named tool provider.
func GetProvider(name string) (ToolHostingProvider, bool) {
	idx := loadIndex()
	if idx == nil {
		return ToolHostingProvider{}, false
	}
	provider, ok := idx.providers[strings.TrimSpace(name)]
	return provider, ok
}

// GetProviders returns the full provider map for admin UI.
func GetProviders() map[string]ToolHostingProvider {
	setting := toolHostingSetting
	out := make(map[string]ToolHostingProvider, len(setting.Providers))
	for name, provider := range setting.Providers {
		out[name] = provider
	}
	return out
}

// ---------------------------------------------------------------------------
// Validation and loading
// ---------------------------------------------------------------------------

func executorSet(kind ToolKind) map[string]struct{} {
	base := map[string]struct{}{
		"gemini": {},
		"openai": {},
	}
	switch kind {
	case KindWebSearch:
		for _, executor := range []string{"zhipu", "tavily", "brave", "bocha", "searxng", "http_json", "openai_images", "gemini_images"} {
			base[executor] = struct{}{}
		}
	case KindRecognition:
		// gemini / openai
	case KindImageGeneration:
		base["openai_images"] = struct{}{}
		base["gemini_images"] = struct{}{}
	}
	return base
}

// keylessExecutors run without an API key.
func keylessExecutors() map[string]struct{} {
	return map[string]struct{}{"searxng": {}, "http_json": {}}
}

func isValidToolKind(kind string) bool {
	switch ToolKind(kind) {
	case KindWebSearch, KindRecognition, KindImageGeneration:
		return true
	default:
		return false
	}
}

// ValidateToolHostingProvidersJSON validates an operator-supplied complete
// providers map: unique names (by construction), a valid Kind, a Type valid
// for that Kind, and the required fields per Type.
func ValidateToolHostingProvidersJSON(value string) error {
	_, err := decodeToolHostingProvidersJSON(value, false)
	return err
}

func decodeToolHostingProvidersJSON(value string, ignoreInvalidEntries bool) (map[string]ToolHostingProvider, error) {
	var providers map[string]ToolHostingProvider
	if err := common.UnmarshalJsonStr(value, &providers); err != nil {
		return nil, fmt.Errorf("解析工具托管提供商失败: %w", err)
	}
	if providers == nil {
		providers = make(map[string]ToolHostingProvider)
	}
	for name, provider := range providers {
		entryErr := validateToolHostingProvider(name, provider)
		if entryErr == nil {
			continue
		}
		if !ignoreInvalidEntries {
			return nil, entryErr
		}
		common.SysError(entryErr.Error())
		delete(providers, name)
	}
	return providers, nil
}

func validateToolHostingProvider(name string, provider ToolHostingProvider) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("工具托管提供商名称不能为空")
	}
	if !isValidToolKind(string(provider.Kind)) {
		return fmt.Errorf("工具托管提供商 %q 的 kind 无效: %q", name, provider.Kind)
	}
	executors := executorSet(provider.Kind)
	providerType := strings.ToLower(strings.TrimSpace(provider.Type))
	if providerType == "" {
		return fmt.Errorf("工具托管提供商 %q 未填写 type", name)
	}
	if _, ok := executors[providerType]; !ok {
		return fmt.Errorf("工具托管提供商 %q 的 type %q 不适用于 kind %q", name, provider.Type, provider.Kind)
	}
	if provider.ChannelID < 0 {
		return fmt.Errorf("工具托管提供商 %q 的 channel_id 无效", name)
	}
	if provider.ChannelID > 0 {
		// channel-backed: credentials come from the channel at execution time
		return nil
	}
	if _, keyless := keylessExecutors()[providerType]; !keyless && strings.TrimSpace(provider.APIKey) == "" {
		return fmt.Errorf("工具托管提供商 %q 缺少 API 密钥（或填写 channel_id 借用渠道凭证）", name)
	}
	if providerType == "searxng" && strings.TrimSpace(provider.APIBase) == "" {
		return fmt.Errorf("工具托管提供商 %q (searxng) 需填写 api_base（实例地址）", name)
	}
	if providerType == "http_json" {
		if strings.TrimSpace(provider.APIBase) == "" || !strings.Contains(provider.APIBase, "{query}") {
			return fmt.Errorf("工具托管提供商 %q (http_json) 的 api_base 需包含 {query} 占位", name)
		}
		if strings.TrimSpace(provider.Extra["result_path"]) == "" {
			return fmt.Errorf("工具托管提供商 %q (http_json) 需填写 extra.result_path", name)
		}
	}
	return nil
}

// LoadToolHostingProvidersFromJSONString replaces the providers and rebuilds
// the index. Invalid legacy entries are dropped individually.
func LoadToolHostingProvidersFromJSONString(value string) {
	providers, err := decodeToolHostingProvidersJSON(value, true)
	if err != nil {
		common.SysError("加载工具托管提供商失败: " + err.Error())
		providers = make(map[string]ToolHostingProvider)
	}
	toolHostingSetting.Providers = providers
	RebuildToolHostingIndex()
}

// SetToolHostingForTest seeds providers for tests.
func SetToolHostingForTest(providers map[string]ToolHostingProvider) {
	if providers == nil {
		providers = make(map[string]ToolHostingProvider)
	}
	toolHostingSetting.Providers = providers
	RebuildToolHostingIndex()
}
