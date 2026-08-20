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
// upstream with search results. It is sized with generous headroom so a model
// that refines its query across several searches still gets a final round to
// produce an answer; the loop terminates early the moment upstream returns no
// search calls.
const maxEmulationRounds = 8

const emulatedWebSearchFunctionName = "web_search"

// emulatedToolCall is one search call the model made against an emulated tool.
type emulatedToolCall struct {
	CallID    string
	Name      string
	Arguments string
}

// emulateResponsesHostedTools rewrites emulated hosted tool declarations
// (web_search / web_search_preview per the channel's emulate_tool_types) into
// an ordinary web_search function tool, and rewrites matching web_search_call
// history items into function form. Bodies with nothing to emulate are
// returned byte-identical.
func emulateResponsesHostedTools(body []byte, emulateSet map[string]struct{}, bridge *relaycommon.ResponsesClientToolBridge) []byte {
	if len(emulateSet) == 0 || len(body) == 0 {
		return body
	}
	tools := gjson.GetBytes(body, "tools")
	rewrittenInput, inputChanged := rewriteWebSearchHistory(gjson.GetBytes(body, "input"))
	if !tools.IsArray() && !inputChanged {
		return body
	}

	changed := false
	emittedSearch := false
	newTools := make([][]byte, 0, len(tools.Array()))
	for _, tool := range tools.Array() {
		toolType := strings.ToLower(strings.TrimSpace(tool.Get("type").String()))
		if toolType != "web_search" && toolType != "web_search_preview" {
			newTools = append(newTools, []byte(tool.Raw))
			continue
		}
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
	}

	if !changed && !inputChanged {
		return body
	}
	result := body
	if changed {
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
	for index, raw := range rewrittenInput {
		var err error
		result, err = sjson.SetRawBytes(result, fmt.Sprintf("input.%d", index), raw)
		if err != nil {
			return body
		}
	}
	return result
}

// rewriteWebSearchHistory turns native web_search_call history items into the
// function form the upstream expects. Returns per-index replacements.
func rewriteWebSearchHistory(input gjson.Result) (map[int][]byte, bool) {
	if !input.IsArray() {
		return nil, false
	}
	rewritten := make(map[int][]byte)
	for i, item := range input.Array() {
		if strings.ToLower(strings.TrimSpace(item.Get("type").String())) != "web_search_call" {
			continue
		}
		callID := item.Get("call_id").String()
		if callID == "" {
			callID = item.Get("id").String()
		}
		arguments, _ := common.Marshal(map[string]any{"query": item.Get("action.query").String()})
		rewritten[i] = marshaledTool(map[string]any{
			"type":      "function_call",
			"call_id":   callID,
			"name":      emulatedWebSearchFunctionName,
			"arguments": string(arguments),
		})
	}
	return rewritten, len(rewritten) > 0
}

// captureWriter buffers everything the relay writes during one emulation
// round, so intermediate rounds never reach the client and the final round can
// be flushed (with search items prepended) once it is known to be final.
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
// client's writer buffered; if the model called the emulated web_search
// function, the gateway executes the search, appends the calls and results to
// the input and iterates. The first round without emulated calls is flushed to
// the client as the final answer, with native web_search_call items spliced in
// front. Usage is summed across rounds so billing matches the real upstream
// consumption.
func runResponsesEmulationLoop(c *gin.Context, info *relaycommon.RelayInfo, doRequest func(io.Reader) (any, error), doResponse func(*http.Response) (*dto.Usage, *types.NewAPIError), baseBody []byte, backend *dto.EmulatedToolBackend) (*dto.Usage, *types.NewAPIError) {
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
				logger.LogWarn(c.Request.Context(), fmt.Sprintf("emulation round cap reached, forwarding response with %d unexecuted search calls", len(calls)))
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

		next, err := appendEmulatedToolResults(body, calls, captured.buf.Bytes(), backend, c)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		body = next
	}
	return totalUsage, nil
}

// appendEmulatedToolResults executes every emulated call of the round and
// extends the request input with the model's calls (plus its reasoning items,
// mirroring what a native client echoes back) and the search results.
func appendEmulatedToolResults(body []byte, calls []emulatedToolCall, captured []byte, backend *dto.EmulatedToolBackend, c *gin.Context) ([]byte, error) {
	appended := reasoningItemsFromCaptured(captured)
	for _, call := range calls {
		query := webSearchQuery(call.Arguments)
		result := executeEmulatedWebSearch(c.Request.Context(), query, backend)
		logger.LogDebug(c, "emulated web_search %q -> %.200s", query, result)
		callItem, err := common.Marshal(map[string]any{
			"type":      "function_call",
			"call_id":   call.CallID,
			"name":      call.Name,
			"arguments": call.Arguments,
		})
		if err != nil {
			return nil, err
		}
		outputItem, err := common.Marshal(map[string]any{
			"type":    "function_call_output",
			"call_id": call.CallID,
			"output":  result,
		})
		if err != nil {
			return nil, err
		}
		appended = append(appended, callItem, outputItem)
	}
	return appendInputItems(body, appended)
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
// or a plain JSON body) for search calls the gateway must execute. The buffer
// holds the client-facing form, so both shapes count: the bridged
// function_call and the already-restored native web_search_call.
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
			if !ok || spec.Kind != relaycommon.ResponsesClientToolWebSearch {
				return
			}
			call = emulatedToolCall{
				CallID:    strings.TrimSpace(item.Get("call_id").String()),
				Name:      name,
				Arguments: item.Get("arguments").String(),
			}
		case "web_search_call":
			arguments, _ := common.Marshal(map[string]any{"query": item.Get("action.query").String()})
			call = emulatedToolCall{
				CallID:    strings.TrimSpace(item.Get("call_id").String()),
				Name:      emulatedWebSearchFunctionName,
				Arguments: string(arguments),
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

// flushFinalResponse replays the final buffered round to the client. For
// streams, native web_search_call item events are spliced in after the
// response preamble and every remaining frame's output_index is shifted so the
// client observes one coherent sequence (synthetic events carry no
// sequence_number, matching the reasoning-injection precedent). Non-stream
// bodies get the restored items spliced to the front of the output array.
func flushFinalResponse(c *gin.Context, captured []byte, calls []emulatedToolCall, totalUsage *dto.Usage) error {
	// the buffered DoResponse set Content-Length for the un-prepend body on
	// the real writer; the spliced-in search items make it stale
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
			extra = append(extra, map[string]any{
				"id":      callIDOrDefault(call),
				"call_id": call.CallID,
				"type":    "web_search_call",
				"status":  "completed",
				"action":  map[string]any{"type": "search", "query": webSearchQuery(call.Arguments)},
			})
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
					if err := writeSyntheticSearchItemEvents(c, calls); err != nil {
						return err
					}
				}
				inserted = true
				continue
			}
		}
		shifted, err := shiftFrameIndexes(frame, indexShift, totalUsage)
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
			"cached_tokens":     total.PromptTokensDetails.CachedTokens,
			"cache_write_tokens": total.PromptTokensDetails.CacheWriteTokens,
		}
	}
	return usage
}

func callIDOrDefault(call emulatedToolCall) string {
	if call.CallID != "" {
		return call.CallID
	}
	return "ws_" + common.GetUUID()
}

func sseFrameEvent(frame []byte) string {
	for _, line := range bytes.Split(frame, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("event:")) {
			return strings.TrimSpace(string(line[len("event:"):]))
		}
	}
	return ""
}

// shiftFrameIndexes bumps output_index of one SSE frame so spliced-in search
// items keep the client's item ordering monotone, and rewrites the terminal
// event's usage to the summed total.
func shiftFrameIndexes(frame []byte, indexShift int, totalUsage *dto.Usage) ([]byte, error) {
	if indexShift == 0 && totalUsage == nil {
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
				if totalUsage != nil && (eventType == "response.completed" || eventType == "response.done") {
					if response, ok := event["response"].(map[string]any); ok {
						response["usage"] = summedUsageJSON(totalUsage)
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

// writeSyntheticSearchItemEvents emits added/done event pairs for the executed
// searches so the client renders them like native web_search calls.
func writeSyntheticSearchItemEvents(c *gin.Context, calls []emulatedToolCall) error {
	for i, call := range calls {
		added, err := common.Marshal(map[string]any{
			"type":         "response.output_item.added",
			"output_index": i,
			"item": map[string]any{
				"id":      callIDOrDefault(call),
				"call_id": call.CallID,
				"type":    "web_search_call",
				"status":  "in_progress",
			},
		})
		if err != nil {
			return err
		}
		done, err := common.Marshal(map[string]any{
			"type":         "response.output_item.done",
			"output_index": i,
			"item": map[string]any{
				"id":      callIDOrDefault(call),
				"call_id": call.CallID,
				"type":    "web_search_call",
				"status":  "completed",
				"action":  map[string]any{"type": "search", "query": webSearchQuery(call.Arguments)},
			},
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
