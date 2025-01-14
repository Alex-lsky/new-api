package aws

// Message represents a chat message
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// BedrockRequest 统一的Bedrock请求结构
type BedrockRequest struct {
	ModelId  string    `json:"modelId"`
	Messages []Message `json:"messages"`
	// 通用参数
	MaxTokens   uint    `json:"maxTokens,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"topP,omitempty"`
	TopK        int     `json:"topK,omitempty"`
}

// BedrockResponse 统一的Bedrock响应结构
type BedrockResponse struct {
	ModelId   string  `json:"modelId"`
	RequestId string  `json:"requestId"`
	Message   Message `json:"message,omitempty"`
	// 流式响应
	Delta struct {
		Text string `json:"text,omitempty"`
	} `json:"delta,omitempty"`
	// 结束原因
	StopReason *string `json:"stopReason,omitempty"`
	// 错误信息
	Error *BedrockError `json:"error,omitempty"`
	// Token使用统计
	Usage *BedrockUsage `json:"usage,omitempty"`
}

// BedrockError AWS Bedrock错误结构
type BedrockError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// BedrockUsage Token使用统计
type BedrockUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`
}

// 支持的模型列表
var SupportedModels = map[string]string{
	// Claude系列
	"claude-instant-1.2":         "anthropic.claude-instant-v1",
	"claude-2.0":                 "anthropic.claude-v2",
	"claude-2.1":                 "anthropic.claude-v2:1",
	"claude-3-sonnet-20240229":   "anthropic.claude-3-sonnet-20240229-v1:0",
	"claude-3-opus-20240229":     "anthropic.claude-3-opus-20240229-v1:0",
	"claude-3-haiku-20240307":    "anthropic.claude-3-haiku-20240307-v1:0",
	"claude-3-5-sonnet-20240620": "anthropic.claude-3-5-sonnet-20240620-v1:0",
	"claude-3-5-sonnet-20241022": "anthropic.claude-3-5-sonnet-20241022-v2:0",
	"claude-3-5-haiku-20241022":  "anthropic.claude-3-5-haiku-20241022-v1:0",

	// Titan系列
	"amazon.titan-text-lite-v1":    "amazon.titan-text-lite-v1",
	"amazon.titan-text-express-v1": "amazon.titan-text-express-v1",

	// Llama 2系列
	"meta.llama2-13b-chat-v1": "meta.llama2-13b-chat-v1",
	"meta.llama2-70b-chat-v1": "meta.llama2-70b-chat-v1",

	// Cohere系列
	"cohere.command-text-v14":       "cohere.command-text-v14",
	"cohere.command-light-text-v14": "cohere.command-light-text-v14",
}

// GetModelID 获取AWS Bedrock模型ID
func GetModelID(model string) string {
	if modelID, ok := SupportedModels[model]; ok {
		return modelID
	}
	return model
}
