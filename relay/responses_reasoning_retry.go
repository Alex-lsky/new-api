package relay

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// responsesReasoningRefExpiredMark matches the upstream rejection when a
// request references a reasoning item the upstream can no longer resolve.
// opencode zen/Console Go does not retain reasoning items between requests, so
// a client echoing a previous turn's reasoning item (Codex does this on every
// turn) makes the upstream reject the whole body with this error even though
// the reasoning content is irrelevant to a new turn.
const responsesReasoningRefExpiredMark = "Referenced reasoning item"

// stripResponsesInputReasoning removes `reasoning` items from the top-level
// `input` array of a Responses request body. Reasoning is cross-request
// context only; dropping it never removes a message, tool call, or tool result
// the model needs to answer.
func stripResponsesInputReasoning(body []byte) []byte {
	if len(body) == 0 || !gjson.GetBytes(body, "input").IsArray() {
		return body
	}
	items := gjson.GetBytes(body, "input").Array()
	kept := make([][]byte, 0, len(items))
	changed := false
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Get("type").String()), "reasoning") {
			changed = true
			continue
		}
		kept = append(kept, []byte(item.Raw))
	}
	if !changed {
		return body
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, raw := range kept {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(raw)
	}
	b.WriteByte(']')
	out, err := sjson.SetRawBytes(body, "input", []byte(b.String()))
	if err != nil {
		return body
	}
	return out
}

// responsesRetryDoRequest wraps an upstream request so a single 400 caused by a
// stale reasoning reference is retried once with the client's reasoning items
// stripped from the input. Any other response is returned untouched (a rejected
// 400 body is re-buffered so the regular error path can still read it).
func responsesRetryDoRequest(c *gin.Context, doRequest func(io.Reader) (any, error)) func(io.Reader) (any, error) {
	return func(initial io.Reader) (any, error) {
		raw, err := io.ReadAll(initial)
		if err != nil {
			return nil, err
		}
		respAny, err := doRequest(bytes.NewReader(raw))
		if err != nil {
			return respAny, err
		}
		httpResp := respAny.(*http.Response)
		if httpResp.StatusCode != http.StatusBadRequest {
			return httpResp, nil
		}
		rawErr, readErr := io.ReadAll(httpResp.Body)
		_ = httpResp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		// restore the 400 body for the regular error handler unless we retry
		httpResp.Body = io.NopCloser(bytes.NewReader(rawErr))
		if !bytes.Contains(rawErr, []byte(responsesReasoningRefExpiredMark)) {
			return httpResp, nil
		}
		sanitized := stripResponsesInputReasoning(raw)
		if bytes.Equal(sanitized, raw) {
			return httpResp, nil
		}
		logger.LogWarn(c, "upstream rejected referenced reasoning item, retrying without client reasoning input")
		outbound, closer, err := relaycommon.NewOutboundJSONBody(sanitized)
		if err != nil {
			return nil, fmt.Errorf("rebuild request for reasoning retry: %w", err)
		}
		retried, retryErr := doRequest(outbound)
		_ = closer.Close()
		return retried, retryErr
	}
}
