package aws

import (
	"errors"
	"io"
	"net/http"
	"one-api/dto"
	relaycommon "one-api/relay/common"

	"github.com/gin-gonic/gin"
)

const (
	RequestModeCompletion = 1
	RequestModeMessage    = 2
)

type Adaptor struct {
	mode int // 请求模式: RequestModeCompletion 或 RequestModeMessage
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
	a.mode = RequestModeMessage
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return "", nil // 使用AWS SDK，不需要URL
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	return nil // 使用AWS SDK，不需要设置header
}

func (a *Adaptor) ConvertRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}

	awsReq := &dto.AWSConverseRequest{
		ModelId: request.Model,
		Messages: []dto.AWSConverseMessage{
			{
				Role: "user",
				Content: dto.AWSConverseContent{
					Text: request.Messages[0].Content,
				},
			},
		},
	}

	c.Set("request_model", request.Model)
	c.Set("converted_request", awsReq)
	return awsReq, nil
>>>>>>> 7fcb696146e84d7583114d1df210f76f5d2c69f0
=======
	// 获取模型ID
	modelID := GetModelID(request.Model)
	if modelID == "" {
		// 如果不是Bedrock模型，则尝试使用AWS Converse模型
		awsReq := &dto.AWSConverseRequest{
			ModelId: request.Model,
			Messages: []dto.AWSConverseMessage{
				{
					Role: "user",
					Content: dto.AWSConverseContent{
						Text: request.Messages[0].Content,
					},
				},
			},
		}

		c.Set("request_model", request.Model)
		c.Set("converted_request", awsReq)
		return awsReq, nil
	}

	// 转换消息格式
	var messages []Message
	for _, msg := range request.Messages {
		messages = append(messages, Message{
			Role:    msg.Role,
			Content: msg.StringContent(),
		})
	}

	// 构建Bedrock请求
	bedrockReq := &BedrockRequest{
		ModelId:     modelID,
		Messages:    messages,
		MaxTokens:   request.MaxTokens,
		Temperature: request.Temperature,
		TopP:        request.TopP,
		TopK:        request.TopK,
	}

	// 设置上下文信息
	c.Set("request_model", modelID)
	c.Set("bedrock_request", bedrockReq)

	return bedrockReq, nil
=======
	awsReq := &dto.AWSConverseRequest{
		ModelId: request.Model,
		Messages: []dto.AWSConverseMessage{
			{
				Role: "user",
				Content: dto.AWSConverseContent{
					Text: request.Messages[0].Content,
				},
			},
		},
	}

	c.Set("request_model", request.Model)
	c.Set("converted_request", awsReq)
	return awsReq, nil
>>>>>>> 7fcb696146e84d7583114d1df210f76f5d2c69f0
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, errors.New("rerank not supported")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	if info.IsStream {
		openaiErr, usage := awsStreamHandler(c, nil, info, a.mode)
		if openaiErr != nil {
			return nil, errors.New(openaiErr.Error.Message)
		}
		return usage, nil
	}
	openaiErr, usage := awsHandler(c, info, a.mode)
	if openaiErr != nil {
		return nil, errors.New(openaiErr.Error.Message)
	}
	return usage, nil
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *dto.OpenAIErrorWithStatusCode) {
	// 由于在DoRequest中已经处理了响应，这里不需要额外处理
	return nil, nil
}

func (a *Adaptor) GetModelList() (models []string) {
	for model := range SupportedModels {
		models = append(models, model)
	}
	return
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
