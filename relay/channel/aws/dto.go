package aws

import (
	"one-api/relay/channel/claude"
)

type AwsClaudeRequest struct {
	// AnthropicVersion should be "bedrock-2023-05-31"
	AnthropicVersion string                 `json:"anthropic_version"`
	System           string                 `json:"system,omitempty"`
	Messages         []claude.ClaudeMessage `json:"messages"`
	MaxTokens        uint                   `json:"max_tokens,omitempty"`
	Temperature      float64                `json:"temperature,omitempty"`
	TopP             float64                `json:"top_p,omitempty"`
	TopK             int                    `json:"top_k,omitempty"`
	StopSequences    []string               `json:"stop_sequences,omitempty"`
	Tools            []claude.Tool          `json:"tools,omitempty"`
	ToolChoice       any                    `json:"tool_choice,omitempty"`
}

type AWSConverseRequest struct {
	Messages    []AWSConverseMessage `json:"messages"`
	ModelID     string               `json:"modelId"`
	Temperature float64              `json:"temperature,omitempty"`
	TopP        float64              `json:"topP,omitempty"`
	MaxTokens   int                  `json:"maxTokens,omitempty"`
}

type AWSConverseResponse struct {
	Message AWSConverseMessage `json:"message"`
	Usage   struct {
		InputTokens  int `json:"inputTokens"`
		OutputTokens int `json:"outputTokens"`
	} `json:"usage"`
}

type AWSConverseMessage struct {
	Role    string                    `json:"role"`
	Content []AWSConverseContentBlock `json:"content"`
}

type AWSConverseContentBlock struct {
	Text string `json:"text"`
}

func copyRequest(req *claude.ClaudeRequest) *AwsClaudeRequest {
	return &AwsClaudeRequest{
		AnthropicVersion: "bedrock-2023-05-31",
		System:           req.System,
		Messages:         req.Messages,
		MaxTokens:        req.MaxTokens,
		Temperature:      req.Temperature,
		TopP:             req.TopP,
		TopK:             req.TopK,
		StopSequences:    req.StopSequences,
		Tools:            req.Tools,
		ToolChoice:       req.ToolChoice,
	}
}
