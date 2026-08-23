package relay

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// maxEmulationRounds bounds how many times the gateway may go back to the
// upstream with tool results. It is sized with generous headroom so a model
// that refines its query across several searches still gets a final round to
// produce an answer; the loop terminates early the moment upstream returns no
// emulated calls.
const maxEmulationRounds = 8

const (
	emulatedWebSearchFunctionName   = "web_search"
	emulatedImageFunctionName       = "image_generation"
	emulatedRecognitionFunctionName = "image_recognition"
)

// emulatedToolCall is one call the model made against an emulated hosted
// tool. Result/ImageB64/Summary are filled in when the gateway executes it.
type emulatedToolCall struct {
	CallID    string
	Name      string
	Arguments string
	Kind      relaycommon.ResponsesClientToolKind
	// Result is the text fed back to the model as the function_call_output.
	Result string
	// ImageB64 is the generated image payload (bare base64) restored to the
	// client in a native image_generation_call item. Only set for
	// image_generation calls.
	ImageB64 string
	// Summary is the short text restored in an image_recognition_call item.
	// Only set for image_recognition calls.
	Summary string
}

func emulatedRecognitionToolDeclaration() map[string]any {
	return map[string]any{
		"type":        "function",
		"name":        emulatedRecognitionFunctionName,
		"description": "Analyze an image (the image the user already attached, or the one in image_url) and answer a question about it.",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"image_url": map[string]any{"type": "string", "description": "Optional image to analyze; omit to analyze the last image in the conversation."},
				"question":  map[string]any{"type": "string", "description": "What to determine about the image."},
			},
		},
	}
}

// exemptEmulatedFromStrip removes tool types the gateway emulates from the
// strip blacklist: their declarations must survive so the emulation rewrite
// can turn them into gateway-executed functions.
func exemptEmulatedFromStrip(stripSet map[string]struct{}, backends map[string]*dto.EmulatedToolBackend) map[string]struct{} {
	if len(stripSet) == 0 || len(backends) == 0 {
		return stripSet
	}
	stripped := make(map[string]struct{}, len(stripSet))
	for toolType := range stripSet {
		if _, emulated := backends[toolType]; emulated {
			continue
		}
		if toolType == "web_search_preview" {
			if _, emulated := backends[dto.EmulatedToolTypeWebSearch]; emulated {
				continue
			}
		}
		stripped[toolType] = struct{}{}
	}
	if len(stripped) == 0 {
		return nil
	}
	return stripped
}

// emulateResponsesHostedTools rewrites emulated hosted tool declarations
// (web_search / web_search_preview / image_generation per the channel's
// emulate_tool_types) into ordinary function tools, and rewrites matching
// history call items into function form. Bodies with nothing to emulate are
// returned byte-identical.
func emulateResponsesHostedTools(body []byte, emulateSet map[string]struct{}, bridge *relaycommon.ResponsesClientToolBridge) []byte {
	if len(emulateSet) == 0 || len(body) == 0 {
		return body
	}
	tools := gjson.GetBytes(body, "tools")
	rewrittenInput, inputChanged := rewriteEmulatedToolHistory(gjson.GetBytes(body, "input"), emulateSet)
	_, recognitionEmulated := emulateSet["image_recognition"]
	if recognitionEmulated {
		// the upstream must never see input_image parts (text-only or
		// vision-flaky upstreams reject the whole request), so remember the
		// last image for the executor and replace the parts with a marker
		if image := lastInputImageFromBody(body); image != "" && bridge != nil {
			bridge.AttachedImage = image
		}
		var stripped bool
		rewrittenInput, stripped = stripRecognitionImages(rewrittenInput)
		inputChanged = inputChanged || stripped
	}
	if !tools.IsArray() && !inputChanged && !recognitionEmulated {
		return body
	}

	changed := inputChanged
	emittedSearch := false
	emittedImage := false
	emittedRecognition := false
	newTools := make([][]byte, 0, len(tools.Array()))
	for _, tool := range tools.Array() {
		toolType := strings.ToLower(strings.TrimSpace(tool.Get("type").String()))
		switch toolType {
		case "web_search", "web_search_preview":
			if _, emulated := emulateSet[toolType]; !emulated {
				// tolerate unfolded sets: web_search_preview shares the executor
				if toolType == "web_search_preview" {
					_, emulated = emulateSet["web_search"]
				}
				if !emulated {
					newTools = append(newTools, []byte(tool.Raw))
					continue
				}
			}
			if emittedSearch {
				// a second web_search declaration is the same tool; forwarding a
				// duplicate function name would be rejected upstream
				changed = true
				continue
			}
			replacement := marshaledTool(map[string]any{
				"type":        "function",
				"name":        emulatedWebSearchFunctionName,
				"description": "Search the web for current information and return a grounded answer with source links.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string", "description": "The search query."},
					},
					"required": []string{"query"},
				},
			})
			if replacement == nil || !bridge.Register(emulatedWebSearchFunctionName, relaycommon.ResponsesClientToolSpec{
				Kind: relaycommon.ResponsesClientToolWebSearch,
				Name: emulatedWebSearchFunctionName,
			}) {
				newTools = append(newTools, []byte(tool.Raw))
				continue
			}
			newTools = append(newTools, replacement)
			emittedSearch = true
			changed = true
			continue
		case "image_generation":
			if _, emulated := emulateSet[toolType]; !emulated {
				newTools = append(newTools, []byte(tool.Raw))
				continue
			}
			if emittedImage {
				changed = true
				continue
			}
			replacement := marshaledTool(map[string]any{
				"type":        "function",
				"name":        emulatedImageFunctionName,
				"description": "Generate an image from a text prompt. The image is delivered to the user as the result of this call; describe it in your reply instead of embedding data.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"prompt": map[string]any{"type": "string", "description": "Text description of the image to generate."},
						"size":   map[string]any{"type": "string", "enum": []string{"auto", "1024x1024", "1536x1024", "1024x1536"}, "description": "Image size."},
						"quality": map[string]any{
							"type": "string", "enum": []string{"auto", "high", "medium", "low"},
							"description": "Image quality.",
						},
					},
					"required": []string{"prompt"},
				},
			})
			if replacement == nil || !bridge.Register(emulatedImageFunctionName, relaycommon.ResponsesClientToolSpec{
				Kind: relaycommon.ResponsesClientToolImageGeneration,
				Name: emulatedImageFunctionName,
			}) {
				newTools = append(newTools, []byte(tool.Raw))
				continue
			}
			newTools = append(newTools, replacement)
			emittedImage = true
			changed = true
			continue
		case "image_recognition":
			if _, emulated := emulateSet[toolType]; !emulated {
				newTools = append(newTools, []byte(tool.Raw))
				continue
			}
			if emittedRecognition {
				changed = true
				continue
			}
			replacement := marshaledTool(emulatedRecognitionToolDeclaration())
			if replacement == nil || !bridge.Register(emulatedRecognitionFunctionName, relaycommon.ResponsesClientToolSpec{
				Kind: relaycommon.ResponsesClientToolImageRecognition,
				Name: emulatedRecognitionFunctionName,
			}) {
				newTools = append(newTools, []byte(tool.Raw))
				continue
			}
			newTools = append(newTools, replacement)
			emittedRecognition = true
			changed = true
			continue
		}
		newTools = append(newTools, []byte(tool.Raw))
	}

	// the gateway-injected image_recognition tool: add the declaration even
	// when the client never listed it, so text-only upstreams can still call
	// a vision backend
	if recognitionEmulated && !emittedRecognition {
		replacement := marshaledTool(emulatedRecognitionToolDeclaration())
		if replacement != nil && bridge.Register(emulatedRecognitionFunctionName, relaycommon.ResponsesClientToolSpec{
			Kind: relaycommon.ResponsesClientToolImageRecognition,
			Name: emulatedRecognitionFunctionName,
		}) {
			newTools = append(newTools, replacement)
			emittedRecognition = true
			changed = true
		}
	}

	if !changed {
		return body
	}
	result := body
	{
		var b strings.Builder
		b.WriteByte('[')
		for i, raw := range newTools {
			if i > 0 {
				b.WriteByte(',')
			}
			b.Write(raw)
		}
		b.WriteByte(']')
		var err error
		result, err = sjson.SetRawBytes(body, "tools", []byte(b.String()))
		if err != nil {
			return body
		}
	}
	if inputChanged {
		var b strings.Builder
		b.WriteByte('[')
		for i, raw := range rewrittenInput {
			if i > 0 {
				b.WriteByte(',')
			}
			b.Write(raw)
		}
		b.WriteByte(']')
		var err error
		result, err = sjson.SetRawBytes(result, "input", []byte(b.String()))
		if err != nil {
			return body
		}
	}
	return rewriteEmulatedToolChoice(result, emittedSearch, emittedImage)
}

func rewriteEmulatedToolChoice(body []byte, searchEmulated bool, imageEmulated bool) []byte {
	toolChoice := gjson.GetBytes(body, "tool_choice")
	if !toolChoice.IsObject() {
		return body
	}

	toolType := strings.ToLower(strings.TrimSpace(toolChoice.Get("type").String()))
	functionName := ""
	switch toolType {
	case "web_search", "web_search_preview":
		if !searchEmulated {
			return body
		}
		functionName = emulatedWebSearchFunctionName
	case "image_generation":
		if !imageEmulated {
			return body
		}
		functionName = emulatedImageFunctionName
	default:
		return body
	}

	choice, err := common.Marshal(map[string]any{
		"type": "function",
		"name": functionName,
	})
	if err != nil {
		return body
	}
	result, err := sjson.SetRawBytes(body, "tool_choice", choice)
	if err != nil {
		return body
	}
	return result
}

// rewriteEmulatedToolHistory turns native web_search_call,
// image_generation_call and image_recognition_call history items into the
// function form the upstream expects. Every hosted call expands into a
// function_call plus a marker function_call_output so the upstream never sees
// an unpaired call. Returns the rebuilt items and whether anything changed.
func rewriteEmulatedToolHistory(input gjson.Result, emulateSet map[string]struct{}) ([][]byte, bool) {
	if !input.IsArray() {
		return nil, false
	}
	items := input.Array()
	rewritten := make([][]byte, 0, len(items))
	changed := false
	_, searchEmulated := emulateSet["web_search"]
	_, imageEmulated := emulateSet["image_generation"]
	_, recognitionEmulated := emulateSet["image_recognition"]
	for _, item := range items {
		switch strings.ToLower(strings.TrimSpace(item.Get("type").String())) {
		case "web_search_call":
			if !searchEmulated {
				break
			}
			callID := item.Get("call_id").String()
			if callID == "" {
				callID = item.Get("id").String()
			}
			arguments, _ := common.Marshal(map[string]any{"query": item.Get("action.query").String()})
			if replacement := marshaledTool(map[string]any{
				"type":      "function_call",
				"call_id":   callID,
				"name":      emulatedWebSearchFunctionName,
				"arguments": string(arguments),
			}); replacement != nil {
				rewritten = append(rewritten, replacement)
				if output := marshaledTool(map[string]any{
					"type":    "function_call_output",
					"call_id": callID,
					"output":  "web search already performed and its results already delivered to the user",
				}); output != nil {
					rewritten = append(rewritten, output)
				}
				changed = true
				continue
			}
		case "image_generation_call":
			if !imageEmulated {
				break
			}
			callID := item.Get("call_id").String()
			if callID == "" {
				callID = item.Get("id").String()
			}
			args := imageArgsFromHistoryItem(item)
			arguments, _ := common.Marshal(args)
			if replacement := marshaledTool(map[string]any{
				"type":      "function_call",
				"call_id":   callID,
				"name":      emulatedImageFunctionName,
				"arguments": string(arguments),
			}); replacement != nil {
				rewritten = append(rewritten, replacement)
				if output := marshaledTool(map[string]any{
					"type":    "function_call_output",
					"call_id": callID,
					"output":  "image generated and already delivered to the user",
				}); output != nil {
					rewritten = append(rewritten, output)
				}
				changed = true
				continue
			}
		case "image_recognition_call":
			if !recognitionEmulated {
				break
			}
			callID := item.Get("call_id").String()
			if callID == "" {
				callID = item.Get("id").String()
			}
			args := recognitionArgsFromHistoryItem(item)
			arguments, _ := common.Marshal(args)
			if replacement := marshaledTool(map[string]any{
				"type":      "function_call",
				"call_id":   callID,
				"name":      emulatedRecognitionFunctionName,
				"arguments": string(arguments),
			}); replacement != nil {
				rewritten = append(rewritten, replacement)
				if output := marshaledTool(map[string]any{
					"type":    "function_call_output",
					"call_id": callID,
					"output":  "image analyzed and the result already surfaced to the user",
				}); output != nil {
					rewritten = append(rewritten, output)
				}
				changed = true
				continue
			}
		}
		rewritten = append(rewritten, []byte(item.Raw))
	}
	return rewritten, changed
}

// recognitionStripMarker replaces every stripped input_image part so the model
// still knows an image was attached and how to inspect it. When several images
// are attached the no-argument fallback only reaches the most recent one.
const recognitionStripMarker = "[An image was attached here but is not visible to you. Call the image_recognition tool without an image_url argument to get its description.]"

// stripRecognitionImages replaces input_image items and message content parts
// with a text marker so a recognition-emulating channel never forwards image
// payloads upstream. Returns the rebuilt items and whether anything changed.
func stripRecognitionImages(items [][]byte) ([][]byte, bool) {
	if len(items) == 0 {
		return items, false
	}
	changed := false
	for i, raw := range items {
		item := gjson.ParseBytes(raw)
		switch strings.ToLower(strings.TrimSpace(item.Get("type").String())) {
		case "input_image":
			if marker := marshaledTool(map[string]any{
				"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": recognitionStripMarker}},
			}); marker != nil {
				items[i] = marker
				changed = true
			}
		case "message":
			content := item.Get("content")
			if !content.IsArray() {
				continue
			}
			parts := content.Array()
			rebuilt := make([][]byte, 0, len(parts))
			partChanged := false
			for _, part := range parts {
				if strings.ToLower(strings.TrimSpace(part.Get("type").String())) == "input_image" {
					if marker := marshaledTool(map[string]any{"type": "input_text", "text": recognitionStripMarker}); marker != nil {
						rebuilt = append(rebuilt, marker)
						partChanged = true
						continue
					}
				}
				rebuilt = append(rebuilt, []byte(part.Raw))
			}
			if !partChanged {
				continue
			}
			var joined strings.Builder
			joined.WriteByte('[')
			for j, part := range rebuilt {
				if j > 0 {
					joined.WriteByte(',')
				}
				joined.Write(part)
			}
			joined.WriteByte(']')
			if updated, err := sjson.SetRawBytes(raw, "content", []byte(joined.String())); err == nil {
				items[i] = updated
				changed = true
			}
		}
	}
	return items, changed
}

// recognitionArgsFromHistoryItem best-effort extracts the arguments of a
// client-echoed image_recognition_call item.
func recognitionArgsFromHistoryItem(item gjson.Result) map[string]any {
	args := map[string]any{}
	if imageURL := firstGjsonString(item, "image_url", "action.image_url"); imageURL != "" {
		args["image_url"] = imageURL
	}
	if question := firstGjsonString(item, "question", "action.question"); question != "" {
		args["question"] = question
	}
	return args
}

// imageArgsFromHistoryItem best-effort extracts the generation arguments of
// a client-echoed image_generation_call item.
func imageArgsFromHistoryItem(item gjson.Result) map[string]any {
	args := map[string]any{}
	if prompt := firstGjsonString(item, "prompt", "action.prompt", "revised_prompt"); prompt != "" {
		args["prompt"] = prompt
	}
	if size := firstGjsonString(item, "size", "action.size"); size != "" {
		args["size"] = size
	}
	if quality := firstGjsonString(item, "quality", "action.quality"); quality != "" {
		args["quality"] = quality
	}
	return args
}

// captureWriter buffers everything the relay writes during one emulation
// round, so intermediate rounds never reach the client and the final round can
// be flushed (with tool items prepended) once it is known to be final.
type captureWriter struct {
	gin.ResponseWriter
	buf bytes.Buffer
}

func (w *captureWriter) Write(b []byte) (int, error)       { return w.buf.Write(b) }
func (w *captureWriter) WriteString(s string) (int, error) { return w.buf.WriteString(s) }
func (w *captureWriter) Flush()                            {}
func (w *captureWriter) WriteHeaderNow()                   {}

// runResponsesEmulationLoop drives the gateway-side hosted-tool execution:
// each round sends the (progressively extended) request upstream with the
// client's writer buffered; if the model called an emulated tool function,
// the gateway executes the matching backend, appends the calls and results to
// the input and iterates. The first round without emulated calls is flushed to
// the client as the final answer, with native web_search_call /
// image_generation_call items spliced in front. Usage is summed across rounds
// so billing matches the real upstream consumption.
func runResponsesEmulationLoop(c *gin.Context, info *relaycommon.RelayInfo, doRequest func(io.Reader) (any, error), doResponse func(*http.Response) (*dto.Usage, *types.NewAPIError), baseBody []byte, backends map[string]*dto.EmulatedToolBackend) (*dto.Usage, *types.NewAPIError) {
	totalUsage := &dto.Usage{}
	body := baseBody
	executed := make([]emulatedToolCall, 0, 2)
	for round := 0; round < maxEmulationRounds; round++ {
		outbound, closer, err := relaycommon.NewOutboundJSONBody(body)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		respAny, err := doRequest(outbound)
		if err != nil {
			closer.Close()
			return nil, types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
		}
		httpResp := respAny.(*http.Response)

		if httpResp.StatusCode != http.StatusOK {
			newAPIError := service.RelayErrorHandler(c.Request.Context(), httpResp, false)
			service.ResetStatusCode(newAPIError, c.GetString("status_code_mapping"))
			closer.Close()
			return nil, newAPIError
		}

		captured := &captureWriter{ResponseWriter: c.Writer}
		original := c.Writer
		c.Writer = captured
		usage, apiErr := doResponse(httpResp)
		c.Writer = original
		closer.Close()
		if apiErr != nil {
			return nil, apiErr
		}
		accumulateUsage(totalUsage, usage)

		calls := emulatedCallsFromCaptured(captured.buf.Bytes(), info)
		if len(calls) == 0 || round == maxEmulationRounds-1 {
			if len(calls) > 0 {
				logger.LogWarn(c.Request.Context(), fmt.Sprintf("emulation round cap reached, forwarding response with %d unexecuted tool calls", len(calls)))
			}
			prepend := executed
			if round == maxEmulationRounds-1 {
				prepend = append(prepend, calls...)
			}
			if err := flushFinalResponse(c, captured.buf.Bytes(), prepend, totalUsage); err != nil {
				return nil, types.NewError(err, types.ErrorCodeBadResponseBody, types.ErrOptionWithSkipRetry())
			}
			return totalUsage, nil
		}
		executed = append(executed, calls...)

		attachedImage := ""
		if info.ClientToolBridge != nil {
			attachedImage = info.ClientToolBridge.AttachedImage
		}
		next, executedCalls, err := appendEmulatedToolResults(body, calls, captured.buf.Bytes(), backends, c, attachedImage)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		// the executed snapshots must observe the execution results (image
		// payloads in particular), so replace the pre-execution copies
		copy(executed[len(executed)-len(calls):], executedCalls)
		body = next
	}
	return totalUsage, nil
}

// appendEmulatedToolResults executes every emulated call of the round and
// extends the request input with the model's calls (plus its reasoning items,
// mirroring what a native client echoes back) and the tool results. Calls are
// executed in place; the mutated slice is returned for the final restore.
func appendEmulatedToolResults(body []byte, calls []emulatedToolCall, captured []byte, backends map[string]*dto.EmulatedToolBackend, c *gin.Context, attachedImage string) ([]byte, []emulatedToolCall, error) {
	appended := reasoningItemsFromCaptured(captured)
	for i := range calls {
		call := &calls[i]
		switch call.Kind {
		case relaycommon.ResponsesClientToolImageGeneration:
			result := executeEmulatedImageGeneration(c.Request.Context(), call.Arguments, backends[dto.EmulatedToolTypeImage])
			call.Result = result.Output
			call.ImageB64 = result.ImageB64
			logger.LogDebug(c, "emulated image_generation %q -> %.200s", emulatedImagePrompt(call.Arguments), result.Output)
		case relaycommon.ResponsesClientToolImageRecognition:
			result := executeEmulatedImageRecognition(c.Request.Context(), call.Arguments, backends[dto.EmulatedToolTypeRecognition], body, attachedImage)
			call.Result = result.Output
			call.Summary = result.Summary
			logger.LogDebug(c, "emulated image_recognition %s -> %.200s", call.Arguments, result.Output)
		default:
			call.Result = executeEmulatedWebSearch(c.Request.Context(), webSearchQuery(call.Arguments), backends[dto.EmulatedToolTypeWebSearch])
			logger.LogDebug(c, "emulated web_search %q -> %.200s", webSearchQuery(call.Arguments), call.Result)
		}
		callItem, err := common.Marshal(map[string]any{
			"type":      "function_call",
			"call_id":   call.CallID,
			"name":      call.Name,
			"arguments": call.Arguments,
		})
		if err != nil {
			return nil, nil, err
		}
		outputItem, err := common.Marshal(map[string]any{
			"type":    "function_call_output",
			"call_id": call.CallID,
			"output":  call.Result,
		})
		if err != nil {
			return nil, nil, err
		}
		appended = append(appended, callItem, outputItem)
	}
	body, err := appendInputItems(body, appended)
	if err != nil {
		return nil, nil, err
	}
	return body, calls, nil
}

// appendInputItems appends raw JSON items to the request input, wrapping a
// plain-string input into message form first.
func appendInputItems(body []byte, items [][]byte) ([]byte, error) {
	input := gjson.GetBytes(body, "input")
	if input.IsArray() {
		existing := input.Raw
		var b strings.Builder
		b.WriteString(existing[:len(existing)-1])
		for _, item := range items {
			b.WriteByte(',')
			b.Write(item)
		}
		b.WriteByte(']')
		return sjson.SetRawBytes(body, "input", []byte(b.String()))
	}
	if input.Type == gjson.String {
		message, err := common.Marshal(map[string]any{
			"type":    "message",
			"role":    "user",
			"content": input.String(),
		})
		if err != nil {
			return nil, err
		}
		wrapped := append([]byte("["), message...)
		wrapped = append(wrapped, ']')
		body, err = sjson.SetRawBytes(body, "input", wrapped)
		if err != nil {
			return nil, err
		}
		return appendInputItems(body, items)
	}
	return nil, fmt.Errorf("cannot append tool results to input of type %v", input.Type)
}

// emulatedCallsFromCaptured scans one round's buffered output (stream frames
// or a plain JSON body) for calls the gateway must execute. The buffer holds
// the client-facing form, so both shapes count: the bridged function_call and
// the already-restored native call item.
func emulatedCallsFromCaptured(captured []byte, info *relaycommon.RelayInfo) []emulatedToolCall {
	seen := make(map[string]struct{})
	var calls []emulatedToolCall
	collect := func(item gjson.Result) {
		itemType := strings.TrimSpace(item.Get("type").String())
		var call emulatedToolCall
		switch itemType {
		case "function_call":
			name := strings.TrimSpace(item.Get("name").String())
			spec, ok := info.ClientToolBridge.Lookup(name)
			if !ok || (spec.Kind != relaycommon.ResponsesClientToolWebSearch && spec.Kind != relaycommon.ResponsesClientToolImageGeneration && spec.Kind != relaycommon.ResponsesClientToolImageRecognition) {
				return
			}
			call = emulatedToolCall{
				CallID:    strings.TrimSpace(item.Get("call_id").String()),
				Name:      name,
				Arguments: item.Get("arguments").String(),
				Kind:      spec.Kind,
			}
		case "web_search_call":
			arguments, _ := common.Marshal(map[string]any{"query": item.Get("action.query").String()})
			call = emulatedToolCall{
				CallID:    strings.TrimSpace(item.Get("call_id").String()),
				Name:      emulatedWebSearchFunctionName,
				Arguments: string(arguments),
				Kind:      relaycommon.ResponsesClientToolWebSearch,
			}
		case "image_generation_call":
			call = emulatedToolCall{
				CallID:    strings.TrimSpace(item.Get("call_id").String()),
				Name:      emulatedImageFunctionName,
				Arguments: string(imageHistoryArguments(item)),
				Kind:      relaycommon.ResponsesClientToolImageGeneration,
			}
		case "image_recognition_call":
			call = emulatedToolCall{
				CallID:    strings.TrimSpace(item.Get("call_id").String()),
				Name:      emulatedRecognitionFunctionName,
				Arguments: string(recognitionHistoryArguments(item)),
				Kind:      relaycommon.ResponsesClientToolImageRecognition,
			}
		default:
			return
		}
		if call.CallID == "" {
			call.CallID = strings.TrimSpace(item.Get("id").String())
		}
		if _, dup := seen[call.CallID]; dup {
			return
		}
		seen[call.CallID] = struct{}{}
		calls = append(calls, call)
	}

	forEachCapturedOutputItem(captured, collect)
	return calls
}

// imageHistoryArguments renders an image call item's generation arguments as
// JSON suitable for the function form.
func imageHistoryArguments(item gjson.Result) []byte {
	arguments, _ := common.Marshal(imageArgsFromHistoryItem(item))
	return arguments
}

// recognitionHistoryArguments renders an image_recognition call item's
// arguments as JSON suitable for the function form.
func recognitionHistoryArguments(item gjson.Result) []byte {
	arguments, _ := common.Marshal(recognitionArgsFromHistoryItem(item))
	return arguments
}

// reasoningItemsFromCaptured returns the round's reasoning output items (with
// their encrypted content) so the follow-up request echoes them back like a
// native client would.
func reasoningItemsFromCaptured(captured []byte) [][]byte {
	var items [][]byte
	forEachCapturedOutputItem(captured, func(item gjson.Result) {
		if strings.TrimSpace(item.Get("type").String()) == "reasoning" {
			items = append(items, []byte(item.Raw))
		}
	})
	return items
}

// forEachCapturedOutputItem walks output items of a captured round, from
// output_item.done events (preferred: complete payloads) falling back to the
// completed event's output array, or the non-stream body's output array.
func forEachCapturedOutputItem(captured []byte, fn func(item gjson.Result)) {
	if isLikelySSE(captured) {
		seenFromDone := false
		for _, frame := range splitSSEFrames(captured) {
			data := sseFrameData(frame)
			if data == nil {
				continue
			}
			switch gjson.GetBytes(data, "type").String() {
			case "response.output_item.done":
				if item := gjson.GetBytes(data, "item"); item.Exists() {
					seenFromDone = true
					fn(item)
				}
			}
		}
		if seenFromDone {
			return
		}
		for _, frame := range splitSSEFrames(captured) {
			data := sseFrameData(frame)
			if data == nil {
				continue
			}
			switch gjson.GetBytes(data, "type").String() {
			case "response.completed", "response.done":
				for _, item := range gjson.GetBytes(data, "response.output").Array() {
					fn(item)
				}
			}
		}
		return
	}
	for _, item := range gjson.GetBytes(captured, "output").Array() {
		fn(item)
	}
}

func isLikelySSE(data []byte) bool {
	// a JSON body always wins: tool-call arguments can embed "data:" URLs
	// (data:image/png;base64,...) which must not be mistaken for SSE frames
	if trimmed := bytes.TrimLeft(data, " \t\r\n"); len(trimmed) > 0 && trimmed[0] == '{' {
		return false
	}
	return bytes.Contains(data, []byte("data:"))
}

func splitSSEFrames(data []byte) [][]byte {
	return bytes.Split(data, []byte("\n\n"))
}

func sseFrameData(frame []byte) []byte {
	for _, line := range bytes.Split(frame, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("data:")) {
			return bytes.TrimSpace(line[len("data:"):])
		}
	}
	return nil
}

func webSearchQuery(arguments string) string {
	if strings.TrimSpace(arguments) == "" {
		return ""
	}
	var envelope map[string]any
	if err := common.Unmarshal([]byte(arguments), &envelope); err == nil {
		if query, ok := envelope["query"].(string); ok {
			return query
		}
	}
	return arguments
}

func emulatedImagePrompt(arguments string) string {
	if strings.TrimSpace(arguments) == "" {
		return ""
	}
	var envelope map[string]any
	if err := common.Unmarshal([]byte(arguments), &envelope); err == nil {
		if prompt, ok := envelope["prompt"].(string); ok {
			return prompt
		}
	}
	return arguments
}

// restoredToolItem renders the client-facing native item for one executed
// emulated call: web_search_call items carry the query, image_generation_call
// items carry the generated image as bare base64.
func restoredToolItem(call emulatedToolCall) map[string]any {
	if call.Kind == relaycommon.ResponsesClientToolImageGeneration {
		args := parseEmulatedImageArgs(call.Arguments)
		action := map[string]any{"type": "generate", "prompt": args.Prompt}
		if args.Size != "" {
			action["size"] = args.Size
		}
		if args.Quality != "" {
			action["quality"] = args.Quality
		}
		item := map[string]any{
			"id":      callIDOrDefault(call),
			"call_id": call.CallID,
			"type":    "image_generation_call",
			"action":  action,
		}
		if call.ImageB64 != "" {
			item["status"] = "completed"
			item["result"] = call.ImageB64
		} else {
			item["status"] = "failed"
		}
		return item
	}
	if call.Kind == relaycommon.ResponsesClientToolImageRecognition {
		args := parseEmulatedRecognitionArgs(call.Arguments)
		item := map[string]any{
			"id":      callIDOrDefault(call),
			"call_id": call.CallID,
			"type":    "image_recognition_call",
			"status":  "completed",
			"action":  map[string]any{"type": "recognize", "question": args.Question},
		}
		if call.Summary != "" {
			item["summary"] = call.Summary
		} else if strings.Contains(call.Result, "failed") || strings.Contains(call.Result, "not configured") {
			item["status"] = "failed"
		}
		return item
	}
	return map[string]any{
		"id":      callIDOrDefault(call),
		"call_id": call.CallID,
		"type":    "web_search_call",
		"status":  "completed",
		"action":  map[string]any{"type": "search", "query": webSearchQuery(call.Arguments)},
	}
}

// flushFinalResponse replays the final buffered round to the client. For
// streams, native tool item events are spliced in after the response preamble
// and every remaining frame's output_index is shifted so the client observes
// one coherent sequence (synthetic events carry no sequence_number, matching
// the reasoning-injection precedent). Non-stream bodies get the restored
// items spliced to the front of the output array.
func flushFinalResponse(c *gin.Context, captured []byte, calls []emulatedToolCall, totalUsage *dto.Usage) error {
	// the buffered DoResponse set Content-Length for the un-prepend body on
	// the real writer; the spliced-in tool items make it stale
	c.Writer.Header().Del("Content-Length")
	if !isLikelySSE(captured) {
		if len(calls) == 0 && totalUsage == nil {
			_, err := c.Writer.Write(captured)
			return err
		}
		var body map[string]any
		if err := common.Unmarshal(captured, &body); err != nil {
			_, werr := c.Writer.Write(captured)
			return werr
		}
		output, _ := body["output"].([]any)
		extra := make([]any, 0, len(calls))
		for _, call := range calls {
			extra = append(extra, restoredToolItem(call))
		}
		body["output"] = append(extra, output...)
		if totalUsage != nil {
			body["usage"] = summedUsageJSON(totalUsage)
		}
		data, err := common.Marshal(body)
		if err != nil {
			_, werr := c.Writer.Write(captured)
			return werr
		}
		_, err = c.Writer.Write(data)
		return err
	}

	indexShift := len(calls)
	prepended := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		prepended = append(prepended, restoredToolItem(call))
	}
	inserted := false
	for _, frame := range splitSSEFrames(captured) {
		if len(bytes.TrimSpace(frame)) == 0 {
			continue
		}
		if !inserted {
			eventType := sseFrameEvent(frame)
			if eventType == "response.created" || eventType == "response.in_progress" {
				if err := writeFrame(c, frame); err != nil {
					return err
				}
				if len(calls) > 0 {
					if err := writeSyntheticToolItemEvents(c, calls); err != nil {
						return err
					}
				}
				inserted = true
				continue
			}
		}
		shifted, err := shiftFrameIndexes(frame, indexShift, totalUsage, prepended)
		if err != nil {
			return err
		}
		if err := writeFrame(c, shifted); err != nil {
			return err
		}
	}
	return nil
}

func summedUsageJSON(total *dto.Usage) map[string]any {
	usage := map[string]any{
		"input_tokens":  total.PromptTokens,
		"output_tokens": total.CompletionTokens,
		"total_tokens":  total.TotalTokens,
	}
	if total.PromptTokensDetails.CachedTokens != 0 || total.PromptTokensDetails.CacheWriteTokens != 0 {
		usage["input_tokens_details"] = map[string]any{
			"cached_tokens":      total.PromptTokensDetails.CachedTokens,
			"cache_write_tokens": total.PromptTokensDetails.CacheWriteTokens,
		}
	}
	return usage
}

func callIDOrDefault(call emulatedToolCall) string {
	if call.CallID != "" {
		return call.CallID
	}
	switch call.Kind {
	case relaycommon.ResponsesClientToolImageGeneration:
		return "ig_" + common.GetUUID()
	case relaycommon.ResponsesClientToolImageRecognition:
		return "ir_" + common.GetUUID()
	default:
		return "ws_" + common.GetUUID()
	}
}

func sseFrameEvent(frame []byte) string {
	for _, line := range bytes.Split(frame, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("event:")) {
			return strings.TrimSpace(string(line[len("event:"):]))
		}
	}
	return ""
}

// shiftFrameIndexes bumps output_index of one SSE frame so spliced-in tool
// items keep the client's item ordering monotone, rewrites the terminal
// event's usage to the summed total, and prepends the restored native tool
// items to the terminal event's output array so clients that rebuild from
// response.completed / response.done see them (not just event subscribers).
func shiftFrameIndexes(frame []byte, indexShift int, totalUsage *dto.Usage, prepended []map[string]any) ([]byte, error) {
	if indexShift == 0 && totalUsage == nil && len(prepended) == 0 {
		return frame, nil
	}
	eventType := sseFrameEvent(frame)
	var lines [][]byte
	for _, line := range bytes.Split(frame, []byte("\n")) {
		payload := bytes.TrimSpace(line[len("data:"):])
		if bytes.HasPrefix(line, []byte("data:")) && len(payload) > 0 && !bytes.Equal(payload, []byte("[DONE]")) {
			var event map[string]any
			if err := common.Unmarshal(payload, &event); err == nil {
				if indexShift > 0 {
					if idx, ok := event["output_index"].(float64); ok {
						event["output_index"] = idx + float64(indexShift)
					}
				}
				if eventType == "response.completed" || eventType == "response.done" {
					if response, ok := event["response"].(map[string]any); ok {
						if totalUsage != nil {
							response["usage"] = summedUsageJSON(totalUsage)
						}
						if len(prepended) > 0 {
							output := make([]any, 0, len(prepended))
							for _, item := range prepended {
								output = append(output, item)
							}
							if existing, ok := response["output"].([]any); ok && len(existing) > 0 {
								output = append(output, existing...)
							}
							response["output"] = output
						}
					}
				}
				if data, err := common.Marshal(event); err == nil {
					line = append([]byte("data: "), data...)
				}
			}
		}
		lines = append(lines, line)
	}
	return bytes.Join(lines, []byte("\n")), nil
}

func writeFrame(c *gin.Context, frame []byte) error {
	_, err := c.Writer.Write(append(bytes.TrimRight(frame, "\n"), '\n', '\n'))
	return err
}

// writeSyntheticToolItemEvents emits added/done event pairs for the executed
// calls so the client renders them like native web_search_call /
// image_generation_call items.
func writeSyntheticToolItemEvents(c *gin.Context, calls []emulatedToolCall) error {
	for i, call := range calls {
		addedItem := restoredToolItem(call)
		addedItem["status"] = "in_progress"
		added, err := common.Marshal(map[string]any{
			"type":         "response.output_item.added",
			"output_index": i,
			"item":         addedItem,
		})
		if err != nil {
			return err
		}
		doneItem := restoredToolItem(call)
		if _, hasStatus := doneItem["status"]; !hasStatus {
			doneItem["status"] = "completed"
		}
		done, err := common.Marshal(map[string]any{
			"type":         "response.output_item.done",
			"output_index": i,
			"item":         doneItem,
		})
		if err != nil {
			return err
		}
		if _, err := c.Writer.Write([]byte("event: response.output_item.added\ndata: " + string(added) + "\n\n")); err != nil {
			return err
		}
		if _, err := c.Writer.Write([]byte("event: response.output_item.done\ndata: " + string(done) + "\n\n")); err != nil {
			return err
		}
	}
	return nil
}

// accumulateUsage sums one round's usage into the running total.
func accumulateUsage(total *dto.Usage, round *dto.Usage) {
	if round == nil {
		return
	}
	total.PromptTokens += round.PromptTokens
	total.CompletionTokens += round.CompletionTokens
	total.TotalTokens += round.TotalTokens
	if round.PromptTokensDetails.CachedTokens != 0 {
		total.PromptTokensDetails.CachedTokens += round.PromptTokensDetails.CachedTokens
	}
	if round.PromptTokensDetails.CacheWriteTokens != 0 {
		total.PromptTokensDetails.CacheWriteTokens += round.PromptTokensDetails.CacheWriteTokens
	}
}
