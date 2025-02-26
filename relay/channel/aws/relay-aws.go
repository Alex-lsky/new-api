package aws

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"one-api/common"
	"one-api/dto"
	relaymodel "one-api/dto"
	"one-api/relay/channel/claude"
	relaycommon "one-api/relay/common"
	"one-api/service"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func newAwsClient(c *gin.Context, info *relaycommon.RelayInfo) (*bedrockruntime.Client, error) {
	awsSecret := strings.Split(info.ApiKey, "|")
	if len(awsSecret) != 3 {
		return nil, errors.New("invalid aws secret key")
	}
	ak := awsSecret[0]
	sk := awsSecret[1]
	region := awsSecret[2]
	client := bedrockruntime.New(bedrockruntime.Options{
		Region:      region,
		Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(ak, sk, "")),
	})

	return client, nil
}

func wrapErr(err error) *relaymodel.OpenAIErrorWithStatusCode {
	return &relaymodel.OpenAIErrorWithStatusCode{
		StatusCode: http.StatusInternalServerError,
		Error: relaymodel.OpenAIError{
			Message: fmt.Sprintf("%s", err.Error()),
		},
	}
}

func awsModelID(requestModel string) (string, error) {
	if awsModelID, ok := awsModelIDMap[requestModel]; ok {
		return awsModelID, nil
	}

	return requestModel, nil
}

func awsHandler(c *gin.Context, info *relaycommon.RelayInfo, requestMode int) (*relaymodel.OpenAIErrorWithStatusCode, *relaymodel.Usage) {
	awsCli, err := newAwsClient(c, info)
	if err != nil {
		return wrapErr(errors.Wrap(err, "newAwsClient")), nil
	}

	awsModelId, err := awsModelID(c.GetString("request_model"))
	if err != nil {
		return wrapErr(errors.Wrap(err, "awsModelID")), nil
	}

	converseReq := &bedrockruntime.ConverseInput{
		ModelId: aws.String(awsModelId),
		Messages: []types.Message{
			{
				Role: types.ConversationRoleUser,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberText{
						Value: c.GetString("prompt"),
					},
				},
			},
		},
		InferenceConfig: &types.InferenceConfiguration{
			MaxTokens:   aws.Int32(2048),
			Temperature: aws.Float32(0.7),
		},
	}

	awsResp, err := awsCli.Converse(c.Request.Context(), converseReq)
	if err != nil {
		return wrapErr(errors.Wrap(err, "Converse")), nil
	}

	openaiResp := convertConverseResponse(converseReq, awsResp)
	usage := relaymodel.Usage{
		PromptTokens:     int(*awsResp.Usage.InputTokens),
		CompletionTokens: int(*awsResp.Usage.OutputTokens),
		TotalTokens:      int(*awsResp.Usage.InputTokens + *awsResp.Usage.OutputTokens),
	}
	openaiResp.Usage = usage

	c.JSON(http.StatusOK, openaiResp)
	return nil, &usage
}
func convertConverseResponse(input *bedrockruntime.ConverseInput, resp *bedrockruntime.ConverseOutput) *dto.OpenAITextResponse {
	output, ok := resp.Output.(*types.ConverseOutputMemberMessage)
	if !ok {
		errorContent, _ := json.Marshal("Invalid response format")
		return &dto.OpenAITextResponse{
			Choices: []dto.OpenAITextResponseChoice{
				{
					Message: dto.Message{
						Content: errorContent,
					},
				},
			},
		}
	}

	content := ""
	for _, block := range output.Value.Content {
		if textBlock, ok := block.(*types.ContentBlockMemberText); ok {
			content += textBlock.Value
		}
	}

	contentBytes, _ := json.Marshal(content)

	return &dto.OpenAITextResponse{
		Id:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   *input.ModelId,
		Choices: []dto.OpenAITextResponseChoice{
			{
				Index: 0,
				Message: dto.Message{
					Role:    string(output.Value.Role),
					Content: contentBytes,
				},
				FinishReason: string(resp.StopReason),
			},
		},
	}
}

func awsStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, requestMode int) (*relaymodel.OpenAIErrorWithStatusCode, *relaymodel.Usage) {
	awsCli, err := newAwsClient(c, info)
	if err != nil {
		return wrapErr(errors.Wrap(err, "newAwsClient")), nil
	}

	awsModelId, err := awsModelID(c.GetString("request_model"))
	if err != nil {
		return wrapErr(errors.Wrap(err, "awsModelID")), nil
	}

	awsReq := &bedrockruntime.InvokeModelWithResponseStreamInput{
		ModelId:     aws.String(awsModelId),
		Accept:      aws.String("application/json"),
		ContentType: aws.String("application/json"),
	}

	// 通用请求处理逻辑
	convertedReq, exists := c.Get("converted_request")
	if !exists {
		return wrapErr(errors.New("converted request not found")), nil
	}
	awsReq.Body, err = json.Marshal(convertedReq)
	if err != nil {
		return wrapErr(errors.Wrap(err, "marshal request")), nil
	}

	awsResp, err := awsCli.InvokeModelWithResponseStream(c.Request.Context(), awsReq)
	if err != nil {
		return wrapErr(errors.Wrap(err, "InvokeModelWithResponseStream")), nil
	}
	stream := awsResp.GetStream()
	defer stream.Close()

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	var usage relaymodel.Usage
	var id string
	var model string
	isFirst := true
	createdTime := common.GetTimestamp()
	c.Stream(func(w io.Writer) bool {
		event, ok := <-stream.Events()
		if !ok {
			return false
		}

		switch v := event.(type) {
		case *types.ResponseStreamMemberChunk:
			if isFirst {
				isFirst = false
				info.FirstResponseTime = time.Now()
			}
			claudeResp := new(claude.ClaudeResponse)
			err := json.NewDecoder(bytes.NewReader(v.Value.Bytes)).Decode(claudeResp)
			if err != nil {
				common.SysError("error unmarshalling stream response: " + err.Error())
				return false
			}

			response, claudeUsage := claude.StreamResponseClaude2OpenAI(requestMode, claudeResp)
			if claudeUsage != nil {
				usage.PromptTokens += claudeUsage.InputTokens
				usage.CompletionTokens += claudeUsage.OutputTokens
			}

			if response == nil {
				return true
			}

			if response.Id != "" {
				id = response.Id
			}
			if response.Model != "" {
				model = response.Model
			}
			response.Created = createdTime
			response.Id = id
			response.Model = model

			jsonStr, err := json.Marshal(response)
			if err != nil {
				common.SysError("error marshalling stream response: " + err.Error())
				return true
			}
			c.Render(-1, common.CustomEvent{Data: "data: " + string(jsonStr)})
			return true
		case *types.UnknownUnionMember:
			fmt.Println("unknown tag:", v.Tag)
			return false
		default:
			fmt.Println("union is nil or unknown type")
			return false
		}
	})
	if info.ShouldIncludeUsage {
		response := service.GenerateFinalUsageResponse(id, createdTime, info.UpstreamModelName, usage)
		err := service.ObjectData(c, response)
		if err != nil {
			common.SysError("send final response failed: " + err.Error())
		}
	}
	service.Done(c)
	if resp != nil {
		err = resp.Body.Close()
		if err != nil {
			return service.OpenAIErrorWrapperLocal(err, "close_response_body_failed", http.StatusInternalServerError), nil
		}
	}
	return nil, &usage
}
