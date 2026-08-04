package convmeta

// Options is the per-request snapshot of host configuration that converters
// consult. The host fills it from its settings system when constructing the
// Meta (see relaycommon.RelayInfo.ConvOptions); relaykit users fill it
// directly. Zero value = every adaptation disabled, no defaults applied.
type Options struct {
	Claude ClaudeOptions
	Gemini GeminiOptions

	// OpenRouterDialect marks the upstream as OpenRouter's OpenAI-compatible
	// surface, which accepts extra fields (reasoning config, cache_control on
	// system parts) that converters emit only for that dialect. The host sets
	// it from the channel type.
	OpenRouterDialect bool

	// PreserveThinkingSuffix reports models whose -thinking/-nothinking/effort
	// suffix must be kept on the outgoing model name (host blacklist lookup).
	// Nil means "never preserve".
	PreserveThinkingSuffix func(modelName string) bool

	// ResponsesChatBridge is request-scoped state used when a Responses request
	// is bridged through a Chat Completions upstream. Chat only exposes function
	// tools, so the request converter records how custom, tool_search, and
	// namespaced tools were represented. The response converter uses the same
	// state to restore native Responses call items.
	ResponsesChatBridge *ResponsesChatBridgeContext
}

type ResponsesChatToolKind string

const (
	ResponsesChatToolFunction  ResponsesChatToolKind = "function"
	ResponsesChatToolCustom    ResponsesChatToolKind = "custom"
	ResponsesChatToolSearch    ResponsesChatToolKind = "tool_search"
	ResponsesChatToolNamespace ResponsesChatToolKind = "namespace"
)

type ResponsesChatToolSpec struct {
	Kind      ResponsesChatToolKind
	Name      string
	Namespace string
}

type ResponsesChatBridgeContext struct {
	ToolsByChatName map[string]ResponsesChatToolSpec
}

func (o *Options) ResetResponsesChatBridge() *ResponsesChatBridgeContext {
	if o == nil {
		return &ResponsesChatBridgeContext{ToolsByChatName: make(map[string]ResponsesChatToolSpec)}
	}
	o.ResponsesChatBridge = &ResponsesChatBridgeContext{
		ToolsByChatName: make(map[string]ResponsesChatToolSpec),
	}
	return o.ResponsesChatBridge
}

func (c *ResponsesChatBridgeContext) Register(chatName string, spec ResponsesChatToolSpec) bool {
	if c == nil || chatName == "" {
		return false
	}
	if c.ToolsByChatName == nil {
		c.ToolsByChatName = make(map[string]ResponsesChatToolSpec)
	}
	if _, exists := c.ToolsByChatName[chatName]; exists {
		return false
	}
	c.ToolsByChatName[chatName] = spec
	return true
}

func (c *ResponsesChatBridgeContext) Lookup(chatName string) (ResponsesChatToolSpec, bool) {
	if c == nil {
		return ResponsesChatToolSpec{}, false
	}
	spec, ok := c.ToolsByChatName[chatName]
	return spec, ok
}

type ClaudeOptions struct {
	// ThinkingAdapterEnabled turns "-thinking"-suffixed OpenAI model names
	// into Claude extended-thinking requests.
	ThinkingAdapterEnabled bool
	// ThinkingAdapterBudgetTokensPercentage sizes thinking budget_tokens as a
	// fraction of max_tokens when the adapter fires.
	ThinkingAdapterBudgetTokensPercentage float64
	// DefaultMaxTokens returns the max_tokens to inject when the source
	// request carries none. The Claude Messages API requires max_tokens
	// (omitting it is a 400), so when this hook is nil and no other path
	// supplies a value, OpenAI→Claude request conversion fails with an
	// explicit error instead of emitting a request the upstream is
	// guaranteed to reject. The new-api host always provides this hook;
	// standalone relaykit users must supply one or guarantee max_tokens on
	// every request.
	DefaultMaxTokens func(modelName string) int
}

type GeminiOptions struct {
	// ThinkingAdapterEnabled maps -thinking/-nothinking/effort suffixes to
	// Gemini thinkingConfig.
	ThinkingAdapterEnabled bool
	// ThinkingAdapterBudgetTokensPercentage sizes thinkingBudget as a fraction
	// of maxOutputTokens when the adapter fires.
	ThinkingAdapterBudgetTokensPercentage float64
	// FunctionCallThoughtSignatureEnabled attaches thoughtSignature bypass
	// values to function-call parts.
	FunctionCallThoughtSignatureEnabled bool
	// SupportsImagine reports whether the model supports image generation
	// (switches response modalities). Nil means "never".
	SupportsImagine func(modelName string) bool
	// SafetySetting returns the harm threshold for a category. Nil or empty
	// return means no safetySettings are attached.
	SafetySetting func(category string) string
}

func (o *ClaudeOptions) DefaultMaxTokensFor(modelName string) (int, bool) {
	if o == nil || o.DefaultMaxTokens == nil {
		return 0, false
	}
	return o.DefaultMaxTokens(modelName), true
}

func (o *GeminiOptions) SupportsImagineModel(modelName string) bool {
	return o != nil && o.SupportsImagine != nil && o.SupportsImagine(modelName)
}

func (o *GeminiOptions) SafetySettingFor(category string) string {
	if o == nil || o.SafetySetting == nil {
		return ""
	}
	return o.SafetySetting(category)
}

func (o *Options) ShouldPreserveThinkingSuffix(modelName string) bool {
	return o != nil && o.PreserveThinkingSuffix != nil && o.PreserveThinkingSuffix(modelName)
}
