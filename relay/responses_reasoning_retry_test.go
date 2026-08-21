package relay

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStripResponsesInputReasoning(t *testing.T) {
	body := []byte(`{"model":"m","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"x"}]},{"type":"function_call_output","call_id":"c1","output":"ok"}],"tools":[]}`)
	out := stripResponsesInputReasoning(body)
	var types []string
	for _, it := range gjsonGetArray(out, "input") {
		types = append(types, it)
	}
	assert.Equal(t, []string{"message", "function_call_output"}, types, "reasoning item dropped, others preserved")
	assert.Contains(t, string(out), `"tools":[]`, "non-input fields untouched")

	// no reasoning -> byte-identical
	clean := []byte(`{"input":[{"type":"message","content":[]}]}`)
	assert.Equal(t, clean, stripResponsesInputReasoning(clean))

	// non-array input (e.g. plain string) -> byte-identical
	str := []byte(`{"input":"hello","model":"m"}`)
	assert.Equal(t, str, stripResponsesInputReasoning(str))

	// case-insensitive reasoning type dropped
	mixed := []byte(`{"input":[{"type":"Reasoning","id":"r"},{"type":"message","content":[]}]}`)
	assert.Equal(t, []string{"message"}, gjsonGetArray(stripResponsesInputReasoning(mixed), "input"))
}

func testGinCtx() *gin.Context {
	gin.SetMode(gin.TestMode)
	rc := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rc)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return ctx
}

func TestResponsesRetryDoRequestStaleReasoning(t *testing.T) {
	marked := `{"error":{"message":"Referenced reasoning item 'rs_x' was not found or has expired.","type":"invalid_request_error"}}`
	okBody := `{"id":"r1","output":[],"status":"completed"}`

	t.Run("retries once with reasoning stripped when upstream 400s on referenced reasoning", func(t *testing.T) {
		var bodies []string
		attempt := 0
		doRequest := func(r io.Reader) (any, error) {
			b, _ := io.ReadAll(r)
			bodies = append(bodies, string(b))
			attempt++
			if attempt == 1 {
				return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(marked))}, nil
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(okBody))}, nil
		}
		wrapped := responsesRetryDoRequest(testGinCtx(), doRequest)
		respAny, err := wrapped(bytes.NewReader([]byte(`{"input":[{"type":"message","content":[]},{"type":"reasoning","id":"rs_x"}],"model":"m"}`)))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, respAny.(*http.Response).StatusCode)
		require.Len(t, bodies, 2)
		assert.False(t, strings.Contains(bodies[1], `"type":"reasoning"`), "retry body has reasoning stripped")
		assert.True(t, strings.Contains(bodies[1], `"type":"message"`), "retry body keeps messages")
	})

	t.Run("does not retry on unrelated 400 and preserves the response body", func(t *testing.T) {
		unrelated := `{"error":{"message":"some other error","type":"invalid_request_error"}}`
		attempt := 0
		doRequest := func(r io.Reader) (any, error) {
			attempt++
			return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(unrelated))}, nil
		}
		wrapped := responsesRetryDoRequest(testGinCtx(), doRequest)
		respAny, err := wrapped(bytes.NewReader([]byte(`{"input":[{"type":"reasoning","id":"rs_x"}],"model":"m"}`)))
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, respAny.(*http.Response).StatusCode)
		require.Equal(t, 1, attempt, "no retry on unrelated 400")
		readBack, _ := io.ReadAll(respAny.(*http.Response).Body)
		assert.Equal(t, unrelated, string(readBack), "400 body intact for the error handler")
	})

	t.Run("does not retry when no reasoning item could be stripped", func(t *testing.T) {
		attempt := 0
		doRequest := func(r io.Reader) (any, error) {
			attempt++
			return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(marked))}, nil
		}
		wrapped := responsesRetryDoRequest(testGinCtx(), doRequest)
		respAny, err := wrapped(bytes.NewReader([]byte(`{"input":"plain string","model":"m"}`)))
		require.NoError(t, err)
		require.Equal(t, 1, attempt, "no pointless retry")
		assert.Equal(t, http.StatusBadRequest, respAny.(*http.Response).StatusCode)
	})

	t.Run("passes through success responses unchanged", func(t *testing.T) {
		attempt := 0
		doRequest := func(r io.Reader) (any, error) {
			attempt++
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(okBody))}, nil
		}
		wrapped := responsesRetryDoRequest(testGinCtx(), doRequest)
		respAny, err := wrapped(bytes.NewReader([]byte(`{"input":[],"model":"m"}`)))
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, respAny.(*http.Response).StatusCode)
		require.Equal(t, 1, attempt, "success never retried")
	})
}

func gjsonGetArray(body []byte, path string) []string {
	var out []string
	for _, v := range gjson.GetBytes(body, path).Array() {
		out = append(out, v.Get("type").String())
	}
	return out
}
