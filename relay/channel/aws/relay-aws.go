package aws

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"one-api/common"
	relaymodel "one-api/dto"
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
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
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

func awsHandler(c *gin.Context, info *relaycommon.RelayInfo, requestMode int) (*relaymodel.OpenAIErrorWithStatusCode, *relaymodel.Usage) {
	awsCli, err := newAwsClient(c, info)
	if err != nil {
		return wrapErr(errors.Wrap(err, "newAwsClient")), nil
	}

	awsModelId, err := awsModelID(c.GetString("request_model"))
	if err != nil {
		return wrapErr(errors.Wrap(err, "awsModelID")), nil
	}

	claudeReq_, ok := c.Get("converted_request")
	if !ok {
		return wrapErr(errors.New("request not found")), nil
	}
	claudeReq := claudeReq_.(*claude.ClaudeRequest)

	awsReq := &bedrockruntime.ConverseInput{
		ModelId: aws.String(awsModelId),
		Messages: []types.Message{
			{
				Role: types.ConversationRoleUser,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberText{
						Value: claudeReq.Prompt,
					},
				},
			},
		},
	}

	awsResp, err := awsCli.Converse(c.Request.Context(), awsReq)
>>>>>>> 7fcb696146e84d7583114d1df210f76f5d2c69f0
	if err != nil {
		return wrapErr(errors.Wrap(err, "Converse")), nil
	}

<<<<<<< HEAD
	// 解析响应
	var bedrockResp BedrockResponse
	if err := json.Unmarshal(output.Body, &bedrockResp); err != nil {
		return wrapErr(errors.Wrap(err, "unmarshal response")), nil
	}

	response := &relaymodel.OpenAITextResponse{
		Id:      fmt.Sprintf("aws-%s", bedrockResp.RequestId),
		Object:  "chat.completion",
		Created: common.GetTimestamp(),
		Model:   req.ModelId,
		Choices: []relaymodel.OpenAITextResponseChoice{
			{
				Index: 0,
				Message: relaymodel.Message{
					Role: bedrockResp.Message.Role,
				},
				FinishReason: "stop",
			},
		},
	}
	response.Choices[0].Message.SetStringContent(bedrockResp.Message.Content)

	usage := relaymodel.Usage{
		PromptTokens:     bedrockResp.Usage.InputTokens,
		CompletionTokens: bedrockResp.Usage.OutputTokens,
		TotalTokens:      bedrockResp.Usage.TotalTokens,
	}
	response.Usage = usage

	c.JSON(http.StatusOK, response)
	return nil, &usage
=======
	type Usage struct {
		InputTokens  int
		OutputTokens int
	}

	type ClaudeResponse struct {
		Completion string
		Usage      Usage
	}

	type BedrockResponse struct {
		Completion string
		Usage      struct {
			InputTokens  int
			OutputTokens int
		}
	}

	outputMessage, ok := awsResp.Output.(*types.ConverseOutputMemberMessage)
	if !ok {
		return wrapErr(errors.New("invalid output type")), nil
	}

	bedrockResponse := &BedrockResponse{
		Completion: outputMessage.Value.Content[0].(*types.ContentBlockMemberText).Value,
		Usage: struct {
			InputTokens  int
			OutputTokens int
		}{
			InputTokens:  int(*awsResp.Usage.InputTokens),
			OutputTokens: int(*awsResp.Usage.OutputTokens),
		},
	}

	openaiResp := &relaymodel.OpenAITextResponse{
		Choices: []relaymodel.OpenAITextResponseChoice{
			{
				Message: relaymodel.Message{
					Content: json.RawMessage(bedrockResponse.Completion),
				},
			},
		},
		Usage: relaymodel.Usage{
			PromptTokens:     bedrockResponse.Usage.InputTokens,
			CompletionTokens: bedrockResponse.Usage.OutputTokens,
			TotalTokens:      bedrockResponse.Usage.InputTokens + bedrockResponse.Usage.OutputTokens,
		},
	}

	c.JSON(http.StatusOK, openaiResp)
	return nil, &openaiResp.Usage

	c.JSON(http.StatusOK, openaiResp)
	return nil, &openaiResp.Usage
>>>>>>> 7fcb696146e84d7583114d1df210f76f5d2c69f0
}
=======
	bedrockReq, ok := c.Get("bedrock_request")
	var output any
	var bedrockResp BedrockResponse
	if ok {
		req := bedrockReq.(*BedrockRequest)

		// 构建请求体
		requestBody := map[string]interface{}{
			"messages":    req.Messages,
			"max_tokens":  req.MaxTokens,
			"temperature": req.Temperature,
			"top_p":       req.TopP,
			"top_k":       req.TopK,
		}

		bodyBytes, err := json.Marshal(requestBody)
		if err != nil {
			return wrapErr(errors.Wrap(err, "marshal request")), nil
		}

		// 调用Bedrock API
		output, err = awsCli.InvokeModel(c.Request.Context(), &bedrockruntime.InvokeModelInput{
			ModelId:     aws.String(req.ModelId),
			Body:        bodyBytes,
			ContentType: aws.String("application/json"),
			Accept:      aws.String("application/json"),
		})
		if err != nil {
			return wrapErr(errors.Wrap(err, "InvokeModel")), nil
		}

		// 解析响应
		if err := json.Unmarshal(output.(*bedrockruntime.InvokeModelOutput).Body, &bedrockResp); err != nil {
			return wrapErr(errors.Wrap(err, "unmarshal response")), nil
		}
	} else {
		awsModelId, err := awsModelID(c.GetString("request_model"))
		if err != nil {
			return wrapErr(errors.Wrap(err, "awsModelID")), nil
		}

		claudeReq_, ok := c.Get("converted_request")
		if !ok {
			return wrapErr(errors.New("request not found")), nil
		}
		claudeReq := claudeReq_.(*claude.ClaudeRequest)

		awsReq := &bedrockruntime.ConverseInput{
			ModelId: aws.String(awsModelId),
			Messages: []types.Message{
				{
					Role: types.ConversationRoleUser,
					Content: []types.ContentBlock{
						&types.ContentBlockMemberText{
							Value: claudeReq.Prompt,
						},
					},
				},
			},
		}

		awsResp, err := awsCli.Converse(c.Request.Context(), awsReq)
		if err != nil {
			return wrapErr(errors.Wrap(err, "Converse")), nil
		}
		output = awsResp
		type Usage struct {
			InputTokens  int
			OutputTokens int
		}

		type ClaudeResponse struct {
			Completion string
			Usage      Usage
		}

		type BedrockResponse struct {
			Completion string
			Usage      struct {
				InputTokens  int
				OutputTokens int
			}
		}

		outputMessage, ok := awsResp.Output.(*types.ConverseOutputMemberMessage)
		if !ok {
			return wrapErr(errors.New("invalid output type")), nil
		}

		bedrockResp = BedrockResponse{
			Completion: outputMessage.Value.Content[0].(*types.ContentBlockMemberText).Value,
			Usage: struct {
				InputTokens  int
				OutputTokens int
			}{
				InputTokens:  int(*awsResp.Usage.InputTokens),
				OutputTokens: int(*awsResp.Usage.OutputTokens),
			},
		}
	}

	response := &relaymodel.OpenAITextResponse{
		Choices: []relaymodel.OpenAITextResponseChoice{
			{
				Message: relaymodel.Message{
					Content: json.RawMessage(bedrockResp.Completion),
				},
			},
		},
		Usage: relaymodel.Usage{
			PromptTokens:     bedrockResp.Usage.InputTokens,
			CompletionTokens: bedrockResp.Usage.OutputTokens,
			TotalTokens:      bedrockResp.Usage.InputTokens + bedrockResp.Usage.OutputTokens,
		},
	}

	c.JSON(http.StatusOK, response)
	return nil, &response.Usage
}
=======
	awsModelId, err := awsModelID(c.GetString("request_model"))
	if err != nil {
		return wrapErr(errors.Wrap(err, "awsModelID")), nil
	}

	claudeReq_, ok := c.Get("converted_request")
	if !ok {
		return wrapErr(errors.New("request not found")), nil
	}
	claudeReq := claudeReq_.(*claude.ClaudeRequest)

	awsReq := &bedrockruntime.ConverseInput{
		ModelId: aws.String(awsModelId),
		Messages: []types.Message{
			{
				Role: types.ConversationRoleUser,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberText{
						Value: claudeReq.Prompt,
					},
				},
			},
		},
	}

	awsResp, err := awsCli.Converse(c.Request.Context(), awsReq)
>>>>>>> 7fcb696146e84d7583114d1df210f76f5d2c69f0
	if err != nil {
		return wrapErr(errors.Wrap(err, "Converse")), nil
	}

<<<<<<< HEAD
	// 解析响应
	var bedrockResp BedrockResponse
	if err := json.Unmarshal(output.Body, &bedrockResp); err != nil {
		return wrapErr(errors.Wrap(err, "unmarshal response")), nil
	}

	response := &relaymodel.OpenAITextResponse{
		Id:      fmt.Sprintf("aws-%s", bedrockResp.RequestId),
		Object:  "chat.completion",
		Created: common.GetTimestamp(),
		Model:   req.ModelId,
		Choices: []relaymodel.OpenAITextResponseChoice{
			{
				Index: 0,
				Message: relaymodel.Message{
					Role: bedrockResp.Message.Role,
				},
				FinishReason: "stop",
			},
		},
	}
	response.Choices[0].Message.SetStringContent(bedrockResp.Message.Content)

	usage := relaymodel.Usage{
		PromptTokens:     bedrockResp.Usage.InputTokens,
		CompletionTokens: bedrockResp.Usage.OutputTokens,
		TotalTokens:      bedrockResp.Usage.TotalTokens,
	}
	response.Usage = usage

	c.JSON(http.StatusOK, response)
	return nil, &usage
=======
	type Usage struct {
		InputTokens  int
		OutputTokens int
	}

	type ClaudeResponse struct {
		Completion string
		Usage      Usage
	}

	type BedrockResponse struct {
		Completion string
		Usage      struct {
			InputTokens  int
			OutputTokens int
		}
	}

	outputMessage, ok := awsResp.Output.(*types.ConverseOutputMemberMessage)
	if !ok {
		return wrapErr(errors.New("invalid output type")), nil
	}

	bedrockResponse := &BedrockResponse{
		Completion: outputMessage.Value.Content[0].(*types.ContentBlockMemberText).Value,
		Usage: struct {
			InputTokens  int
			OutputTokens int
		}{
			InputTokens:  int(*awsResp.Usage.InputTokens),
			OutputTokens: int(*awsResp.Usage.OutputTokens),
		},
	}

	openaiResp := &relaymodel.OpenAITextResponse{
		Choices: []relaymodel.OpenAITextResponseChoice{
			{
				Message: relaymodel.Message{
					Content: json.RawMessage(bedrockResponse.Completion),
				},
			},
		},
		Usage: relaymodel.Usage{
			PromptTokens:     bedrockResponse.Usage.InputTokens,
			CompletionTokens: bedrockResponse.Usage.OutputTokens,
			TotalTokens:      bedrockResponse.Usage.InputTokens + bedrockResponse.Usage.OutputTokens,
		},
	}

	c.JSON(http.StatusOK, openaiResp)
	return nil, &openaiResp.Usage

	c.JSON(http.StatusOK, openaiResp)
	return nil, &openaiResp.Usage
>>>>>>> 7fcb696146e84d7583114d1df210f76f5d2c69f0
}

func awsStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, requestMode int) (*relaymodel.OpenAIErrorWithStatusCode, *relaymodel.Usage) {
	awsCli, err := newAwsClient(c, info)
	if err != nil {
		return wrapErr(errors.Wrap(err, "newAwsClient")), nil
	}

	bedrockReq, ok := c.Get("bedrock_request")
	if !ok {
		return wrapErr(errors.New("request not found")), nil
	}
	req := bedrockReq.(*BedrockRequest)

	// 构建请求体
	requestBody := map[string]interface{}{
		"messages":    req.Messages,
		"max_tokens":  req.MaxTokens,
		"temperature": req.Temperature,
		"top_p":       req.TopP,
		"top_k":       req.TopK,
		"stream":      true,
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return wrapErr(errors.Wrap(err, "marshal request")), nil
	}

	// 调用Bedrock流式API
	output, err := awsCli.InvokeModelWithResponseStream(c.Request.Context(), &bedrockruntime.InvokeModelWithResponseStreamInput{
		ModelId:     aws.String(req.ModelId),
		Body:        bodyBytes,
		ContentType: aws.String("application/json"),
		Accept:      aws.String("application/json"),
	})
	if err != nil {
		return wrapErr(errors.Wrap(err, "InvokeModelWithResponseStream")), nil
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	var usage relaymodel.Usage
	createdTime := common.GetTimestamp()
	isFirst := true

	stream := output.GetStream()
	defer stream.Close()

	c.Stream(func(w io.Writer) bool {
		event, ok := <-stream.Events()
		if !ok {
			return false
		}

		if isFirst {
			isFirst = false
			info.FirstResponseTime = time.Now()
		}

		chunk, ok := event.(*types.ResponseStreamMemberChunk)
		if !ok {
			return true
		}

		var bedrockResp BedrockResponse
		if err := json.Unmarshal(chunk.Value.Bytes, &bedrockResp); err != nil {
			common.SysError("error unmarshalling stream response: " + err.Error())
			return true
		}

		response := &relaymodel.ChatCompletionsStreamResponse{
			Id:      fmt.Sprintf("aws-%s", bedrockResp.RequestId),
			Object:  "chat.completion.chunk",
			Created: createdTime,
			Model:   req.ModelId,
			Choices: []relaymodel.ChatCompletionsStreamResponseChoice{
				{
					Index: 0,
					Delta: relaymodel.ChatCompletionsStreamResponseChoiceDelta{
						Role: bedrockResp.Message.Role,
					},
				},
			},
		}
		response.Choices[0].Delta.SetContentString(bedrockResp.Message.Content)

		jsonStr, err := json.Marshal(response)
		if err != nil {
			common.SysError("error marshalling stream response: " + err.Error())
			return true
		}
		c.Render(-1, common.CustomEvent{Data: "data: " + string(jsonStr)})

		if bedrockResp.Usage != nil {
			usage.PromptTokens = bedrockResp.Usage.InputTokens
			usage.CompletionTokens = bedrockResp.Usage.OutputTokens
			usage.TotalTokens = bedrockResp.Usage.TotalTokens
		}

		return true
	})

	if info.ShouldIncludeUsage {
		response := service.GenerateFinalUsageResponse(fmt.Sprintf("aws-%d", createdTime), createdTime, info.UpstreamModelName, usage)
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
