package tool_hosting

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// ---------------------------------------------------------------------------
// Global tool hosting: named tool-executor providers plus per-model bindings.
//
// DB keys (options):
//   tool_hosting.providers  → JSON object { "<provider name>": ToolHostingProvider }
//   tool_hosting.bindings   → JSON object { "<model or prefix*>": { "<kind>": "<provider name>" } }
//
// Providers are single-purpose: each has one Kind (web_search,
// image_recognition, or image_generation). A provider's Type is either a
// standard executor name (e.g. tavily, gemini) or "channel", which borrows the
// credentials of an existing channel (ChannelID) at execution time.
//
// Bindings select, per model (exact name or longest matching "*" prefix), which
// provider executes each hosted tool kind. Channels inherit the global binding
// unless they pin the tool locally or opt out; unmatched models are untouched.
// ---------------------------------------------------------------------------

const (
	ProvidersOptionKey = "tool_hosting.providers"
	BindingsOptionKey  = "tool_hosting.bindings"
)

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
	// Type is a standard executor name (gemini/zhipu/tavily/brave/bocha/
	// searxng/http_json for kind web_search; gemini/openai for
	// image_recognition; openai_images/gemini_images for image_generation)
	// or "channel".
	Type string `json:"type"`
	// ChannelID selects the channel whose key/base_url back this provider
	// when Type is "channel".
	ChannelID int64 `json:"channel_id,omitempty"`
	// Executor is the standard executor used with the referenced channel's
	// credentials (required for web_search/image_generation when Type is
	// "channel"; optional for image_recognition, defaulting to the channel's
	// native chat endpoint).
	Executor string            `json:"executor,omitempty"`
	APIKey   string            `json:"api_key,omitempty"`
	Model    string            `json:"model,omitempty"`
	APIBase  string            `json:"api_base,omitempty"`
	Extra    map[string]string `json:"extra,omitempty"`
}

// ToolHostingSetting is managed by config.GlobalConfig.Register.
type ToolHostingSetting struct {
	Providers map[string]ToolHostingProvider `json:"providers"`
	Bindings  map[string]map[string]string   `json:"bindings"`
}

var toolHostingSetting = ToolHostingSetting{
	Providers: make(map[string]ToolHostingProvider),
	Bindings:  make(map[string]map[string]string),
}

func init() {
	config.GlobalConfig.Register("tool_hosting", &toolHostingSetting)
	RebuildToolHostingIndex()
}

// ---------------------------------------------------------------------------
// Precomputed lookup index (atomic pointer, lock-free on the request path)
// ---------------------------------------------------------------------------

type bindEntry struct {
	modelKey string
	kinds    map[string]string
}

type toolHostingIndex struct {
	providers map[string]ToolHostingProvider
	bindings  []bindEntry // sorted by modelKey length desc
}

var currentIndex atomic.Pointer[toolHostingIndex]

// RebuildToolHostingIndex rebuilds the lookup index from the current config.
// Called on init and after config updates.
func RebuildToolHostingIndex() {
	providers := make(map[string]ToolHostingProvider, len(toolHostingSetting.Providers))
	for name, provider := range toolHostingSetting.Providers {
		providers[strings.TrimSpace(name)] = provider
	}
	entries := make([]bindEntry, 0, len(toolHostingSetting.Bindings))
	for modelKey, kinds := range toolHostingSetting.Bindings {
		modelKey = strings.TrimSpace(modelKey)
		if modelKey == "" || len(kinds) == 0 {
			continue
		}
		entry := bindEntry{modelKey: strings.TrimSuffix(modelKey, "*"), kinds: make(map[string]string, len(kinds))}
		for kind, providerName := range kinds {
			if kind == "" || providerName == "" {
				continue
			}
			entry.kinds[kind] = providerName
		}
		if len(entry.kinds) > 0 {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if len(entries[i].modelKey) == len(entries[j].modelKey) {
			return entries[i].modelKey < entries[j].modelKey
		}
		return len(entries[i].modelKey) > len(entries[j].modelKey)
	})
	currentIndex.Store(&toolHostingIndex{providers: providers, bindings: entries})
}

func loadIndex() *toolHostingIndex {
	idx := currentIndex.Load()
	if idx == nil {
		RebuildToolHostingIndex()
		idx = currentIndex.Load()
	}
	return idx
}

// LookupBinding returns the provider name bound to kind for modelName, using
// the longest model-key prefix (an exact model name wins over shorter
// prefixes). Empty means no binding.
func LookupBinding(kind ToolKind, modelName string) string {
	idx := loadIndex()
	if idx == nil || len(idx.bindings) == 0 || modelName == "" {
		return ""
	}
	kindStr := string(kind)
	for _, entry := range idx.bindings {
		if !strings.HasPrefix(modelName, entry.modelKey) {
			continue
		}
		if providerName, ok := entry.kinds[kindStr]; ok {
			return providerName
		}
	}
	return ""
}

// GetProvider returns a named tool provider, normalized by lookup.
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

// GetBindings returns the full bindings map for admin UI.
func GetBindings() map[string]map[string]string {
	setting := toolHostingSetting
	out := make(map[string]map[string]string, len(setting.Bindings))
	for modelKey, kinds := range setting.Bindings {
		copied := make(map[string]string, len(kinds))
		for kind, providerName := range kinds {
			copied[kind] = providerName
		}
		out[modelKey] = copied
	}
	return out
}

// ---------------------------------------------------------------------------
// Validation and loading
// ---------------------------------------------------------------------------

func executorSet(kind ToolKind) map[string]struct{} {
	base := map[string]struct{}{
		"channel": {},
		"gemini":  {},
		"openai":  {},
	}
	switch kind {
	case KindWebSearch:
		for _, executor := range []string{"zhipu", "tavily", "brave", "bocha", "searxng", "http_json", "openai_images", "gemini_images"} {
			base[executor] = struct{}{}
		}
	case KindRecognition:
		// gemini / openai / channel
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
// providers map: unique names (by construction), valid kind, a Type valid for
// that kind, and required fields per Type.
func ValidateToolHostingProvidersJSON(value string) error {
	_, err := decodeToolHostingProvidersJSON(value, false)
	return err
}

// ValidateToolHostingBindingsJSON validates a complete bindings map: valid
// kinds and provider names that exist in the current providers.
func ValidateToolHostingBindingsJSON(value string) error {
	_, err := decodeToolHostingBindingsJSON(value, false)
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

	if providerType == "channel" {
		if provider.ChannelID <= 0 {
			return fmt.Errorf("工具托管提供商 %q 引用渠道但未填写 channel_id", name)
		}
		if provider.Kind != KindRecognition && strings.TrimSpace(provider.Executor) == "" {
			return fmt.Errorf("工具托管提供商 %q 引用渠道时需指定 executor（使用的标准执行器）", name)
		}
		if executor := strings.ToLower(strings.TrimSpace(provider.Executor)); executor != "" && executor != "channel" {
			if _, ok := executors[executor]; !ok {
				return fmt.Errorf("工具托管提供商 %q 的 executor %q 无效", name, provider.Executor)
			}
		}
		return nil
	}

	if _, keyless := keylessExecutors()[providerType]; !keyless && strings.TrimSpace(provider.APIKey) == "" {
		return fmt.Errorf("工具托管提供商 %q 缺少 API 密钥", name)
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
	if strings.EqualFold(provider.Type, "openai") && provider.Kind == KindRecognition && strings.TrimSpace(provider.Model) == "" && provider.ChannelID == 0 {
		return fmt.Errorf("工具托管提供商 %q (openai 视觉) 需填写模型名", name)
	}
	return nil
}

func decodeToolHostingBindingsJSON(value string, ignoreInvalidEntries bool) (map[string]map[string]string, error) {
	var bindings map[string]map[string]string
	if err := common.UnmarshalJsonStr(value, &bindings); err != nil {
		return nil, fmt.Errorf("解析工具托管绑定失败: %w", err)
	}
	if bindings == nil {
		bindings = make(map[string]map[string]string)
	}
	for modelKey, kinds := range bindings {
		modelKey = strings.TrimSpace(modelKey)
		if modelKey == "" {
			if !ignoreInvalidEntries {
				return nil, fmt.Errorf("工具托管绑定的模型键不能为空")
			}
			delete(bindings, modelKey)
			continue
		}
		filtered := make(map[string]string, len(kinds))
		for kind, providerName := range kinds {
			if !isValidToolKind(kind) {
				if !ignoreInvalidEntries {
					return nil, fmt.Errorf("工具托管绑定 %q 的 kind 无效: %q", modelKey, kind)
				}
				continue
			}
			providerName = strings.TrimSpace(providerName)
			if providerName == "" {
				continue
			}
			if _, exists := toolHostingSetting.Providers[providerName]; !exists {
				if !ignoreInvalidEntries {
					return nil, fmt.Errorf("工具托管绑定 %q 引用不存在的提供商 %q", modelKey, providerName)
				}
				continue
			}
			filtered[kind] = providerName
		}
		if len(filtered) == 0 {
			delete(bindings, modelKey)
			continue
		}
		bindings[modelKey] = filtered
	}
	return bindings, nil
}

// LoadToolHostingProvidersFromJSONString replaces the providers and rebuilds
// the index. Invalid legacy entries are dropped individually, and bindings
// that referenced a removed provider are pruned to stay consistent.
func LoadToolHostingProvidersFromJSONString(value string) {
	providers, err := decodeToolHostingProvidersJSON(value, true)
	if err != nil {
		common.SysError("加载工具托管提供商失败: " + err.Error())
		providers = make(map[string]ToolHostingProvider)
	}
	toolHostingSetting.Providers = providers
	pruneBindingsToProviders()
	RebuildToolHostingIndex()
}

// LoadToolHostingBindingsFromJSONString replaces the bindings and rebuilds
// the index. Invalid entries are dropped individually.
func LoadToolHostingBindingsFromJSONString(value string) {
	bindings, err := decodeToolHostingBindingsJSON(value, true)
	if err != nil {
		common.SysError("加载工具托管绑定失败: " + err.Error())
		bindings = make(map[string]map[string]string)
	}
	toolHostingSetting.Bindings = bindings
	RebuildToolHostingIndex()
}

// pruneBindingsToProviders removes binding kinds whose provider no longer
// exists, keeping the config consistent after a provider removal.
func pruneBindingsToProviders() {
	for modelKey, kinds := range toolHostingSetting.Bindings {
		if len(kinds) == 0 {
			continue
		}
		filtered := make(map[string]string, len(kinds))
		for kind, providerName := range kinds {
			if _, exists := toolHostingSetting.Providers[providerName]; exists {
				filtered[kind] = providerName
			}
		}
		if len(filtered) == 0 {
			delete(toolHostingSetting.Bindings, modelKey)
		} else {
			toolHostingSetting.Bindings[modelKey] = filtered
		}
	}
}

// SetToolHostingForTest seeds providers and bindings for tests.
func SetToolHostingForTest(providers map[string]ToolHostingProvider, bindings map[string]map[string]string) {
	if providers == nil {
		providers = make(map[string]ToolHostingProvider)
	}
	if bindings == nil {
		bindings = make(map[string]map[string]string)
	}
	toolHostingSetting.Providers = providers
	toolHostingSetting.Bindings = bindings
	RebuildToolHostingIndex()
}
