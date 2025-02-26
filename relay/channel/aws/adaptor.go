package aws

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"one-api/dto"
	"one-api/relay/channel/claude"
	relaycommon "one-api/relay/common"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	RequestModeCompletion = 1
	RequestModeMessage    = 2
)

type Adaptor struct {
	RequestMode int
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
	a.RequestMode = RequestModeMessage
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return "", nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	return nil
}

func (a *Adaptor) ConvertRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}

	// 根据模型类型选择不同的转换逻辑
	switch {
	case strings.Contains(request.Model, "claude"):
		claudeReq, err := claude.RequestOpenAI2ClaudeMessage(*request)
		if err != nil {
			return nil, err
		}
		c.Set("converted_request", claudeReq)
	case strings.Contains(request.Model, "llama"):
		llamaReq, err := convertToLlamaRequest(*request)
		if err != nil {
			return nil, err
		}
		c.Set("converted_request", llamaReq)
	case strings.Contains(request.Model, "mistral"):
		mistralReq, err := convertToMistralRequest(*request)
		if err != nil {
			return nil, err
		}
		c.Set("converted_request", mistralReq)
	case strings.Contains(request.Model, "jamba"):
		jambaReq, err := convertToJambaRequest(*request)
		if err != nil {
			return nil, err
		}
		c.Set("converted_request", jambaReq)
	default:
		return nil, fmt.Errorf("unsupported model type: %s", request.Model)
	}

	c.Set("request_model", request.Model)
	convertedReq, exists := c.Get("converted_request")
	if !exists {
		return nil, errors.New("converted request not found in context")
	}
	return convertedReq, nil
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return nil, nil
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *dto.OpenAIErrorWithStatusCode) {
	if info.IsStream {
		err, usage = awsStreamHandler(c, resp, info, a.RequestMode)
	} else {
		err, usage = awsHandler(c, info, a.RequestMode)
	}
	return
}

func (a *Adaptor) GetModelList() (models []string) {
	for n := range awsModelIDMap {
		models = append(models, n)
	}

	return
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}

// 新增模型转换函数
func convertToLlamaRequest(request dto.GeneralOpenAIRequest) (*dto.GeneralOpenAIRequest, error) {
	// Llama使用原生OpenAI格式
	return &request, nil
}

func convertToMistralRequest(request dto.GeneralOpenAIRequest) (*dto.GeneralOpenAIRequest, error) {
	// Mistral使用原生OpenAI格式
	return &request, nil
}

func convertToJambaRequest(request dto.GeneralOpenAIRequest) (*dto.GeneralOpenAIRequest, error) {
	// Jamba使用原生OpenAI格式
	return &request, nil
}
