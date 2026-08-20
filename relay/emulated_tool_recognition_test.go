package relay

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/tool_hosting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	tool_hosting.SetToolHostingForTest(
		map[string]tool_hosting.ToolHostingProvider{
			"g-search": {Kind: tool_hosting.KindWebSearch, Type: "tavily", APIKey: "gk"},
			"g-vision": {Kind: tool_hosting.KindRecognition, Type: "gemini", APIKey: "gk", Model: "gemini-2.5-flash"},
		},
		map[string]map[string]string{
			"muse-spark-1.2": {"web_search": "g-search", "image_recognition": "g-vision"},
		},
	)
}

const testImageURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func TestExecuteEmulatedImageRecognitionGemini(t *testing.T) {
	dataURLBody := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		dataURLBody = strings.Contains(string(buf[:n]), "inlineData")
		assert.Contains(t, string(buf[:n]), `"text":"what animal"`)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"A cat."}]}}]}`))
	}))
	defer server.Close()

	result := executeEmulatedImageRecognition(t.Context(), `{"question":"what animal","image_url":"`+testImageURL+`"}`, &dto.EmulatedToolBackend{
		Provider: dto.EmulatedRecognitionProviderGemini, APIKey: "k", APIBase: server.URL,
	}, nil)
	assert.True(t, dataURLBody, "inline image sent")
	assert.Equal(t, "A cat.", result.Output)
	assert.Equal(t, "A cat.", result.Summary)
}

func TestExecuteEmulatedImageRecognitionOpenAIVision(t *testing.T) {
	var gotURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer vk", r.Header.Get("Authorization"))
		buf := make([]byte, 8192)
		n, _ := r.Body.Read(buf)
		body := string(buf[:n])
		assert.Contains(t, body, `"type":"image_url"`)
		assert.Contains(t, body, `"model":"gpt-4o-mini"`)
		gotURL = extractImageURLFromBody(body)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"A dog running."}}]}`))
	}))
	defer server.Close()

	result := executeEmulatedImageRecognition(t.Context(), `{"question":"what is this","image_url":"https://example.com/pic.png"}`, &dto.EmulatedToolBackend{
		Provider: dto.EmulatedRecognitionProviderOpenAI, APIKey: "vk", APIBase: server.URL,
	}, nil)
	assert.Equal(t, "https://example.com/pic.png", gotURL)
	assert.Equal(t, "A dog running.", result.Output)
}

func TestExecuteEmulatedImageRecognitionFallsBackToInputImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"It is a mountain."}}]}`))
	}))
	defer server.Close()
	body := []byte(`{"model":"m","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"},{"type":"input_image","image_url":"https://cdn.example/photo1.jpg","detail":"high"}]},{"type":"message","role":"user","content":[{"type":"input_image","image_url":"https://cdn.example/photo2.jpg"}]}]}`)
	result := executeEmulatedImageRecognition(t.Context(), `{"question":"what is shown"}`, &dto.EmulatedToolBackend{
		Provider: dto.EmulatedRecognitionProviderOpenAI, APIKey: "k", APIBase: server.URL,
	}, body)
	assert.Equal(t, "It is a mountain.", result.Output)
	assert.Equal(t, "https://cdn.example/photo2.jpg", lastInputImageFromBody(body), "most recent input_image selected")
}

func TestExecuteEmulatedImageRecognitionErrors(t *testing.T) {
	require.Contains(t, executeEmulatedImageRecognition(t.Context(), `{"question":"q"}`, nil, nil).Output, "not configured")
	require.Contains(t, executeEmulatedImageRecognition(t.Context(), `{"question":"q"}`, &dto.EmulatedToolBackend{Provider: dto.EmulatedRecognitionProviderOpenAI, APIKey: "k"}, nil).Output, "no image", "no image in call or request")
	require.Contains(t, executeEmulatedImageRecognition(t.Context(), `{"image_url":"`+testImageURL+`"}`, &dto.EmulatedToolBackend{Provider: "nope", APIKey: "k"}, nil).Output, "unsupported provider")
	// a channel reference without a channel id must degrade to text, never a
	// default-endpoint network call
	require.Contains(t, executeEmulatedImageRecognition(t.Context(), `{"image_url":"`+testImageURL+`"}`, &dto.EmulatedToolBackend{Provider: dto.EmulatedChannelProvider}, nil).Output, "failed")
	require.Contains(t, executeEmulatedImageRecognition(t.Context(), `{"image_url":"`+testImageURL+`"}`, &dto.EmulatedToolBackend{Provider: dto.EmulatedChannelProvider, ChannelID: 0, Executor: "gemini"}, nil).Output, "failed")
}

func TestImageForVisionRemoteDownloadAndMime(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(png)
	}))
	defer server.Close()
	data, mime, err := imageForVision(server.URL + "/img")
	require.NoError(t, err)
	assert.Equal(t, "image/png", mime)
	decoded, err := base64.StdEncoding.DecodeString(data)
	require.NoError(t, err)
	assert.Equal(t, png, decoded)
}

func extractImageURLFromBody(body string) string {
	start := strings.Index(body, `"url":`)
	if start < 0 {
		return ""
	}
	rest := body[start+len(`"url":`):]
	start = strings.Index(rest, `"`)
	if start < 0 {
		return ""
	}
	rest = rest[start+1:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func TestMergeGlobalToolHosting(t *testing.T) {
	local := map[string]*dto.EmulatedToolBackend{
		dto.EmulatedToolTypeWebSearch: {Provider: dto.EmulatedSearchProviderTavily, APIKey: "local"},
	}
	strip := map[string]struct{}{}
	exclude := map[string]struct{}{}

	// local wins when a global binding also exists for the model
	merged := mergeGlobalToolHosting(local, strip, exclude, true, "ignored-model")
	require.Len(t, merged, 1)
	assert.Equal(t, "local", merged[dto.EmulatedToolTypeWebSearch].APIKey)

	// no local config + global binding for the model -> global backend added
	merged = mergeGlobalToolHosting(nil, strip, exclude, true, "muse-spark-1.2")
	require.NotNil(t, merged)
	assert.Equal(t, "tavily", merged[dto.EmulatedToolTypeWebSearch].Provider)
	assert.Contains(t, merged, dto.EmulatedToolTypeRecognition, "recognition binding materialized")
	require.NotNil(t, merged[dto.EmulatedToolTypeRecognition])

	// inheritance disabled -> nothing from global
	require.Nil(t, mergeGlobalToolHosting(nil, strip, exclude, false, "muse-spark-1.2"))

	// no binding for the model -> nil
	require.Nil(t, mergeGlobalToolHosting(nil, strip, exclude, true, "unknown-model"))

	// stripped kind is not emulated via global
	merged = mergeGlobalToolHosting(nil, map[string]struct{}{dto.EmulatedToolTypeWebSearch: {}}, exclude, true, "muse-spark-1.2")
	_, hasSearch := merged[dto.EmulatedToolTypeWebSearch]
	require.False(t, hasSearch, "stripped kind excluded from global emulation")

	// excluded kind is not emulated via global
	merged = mergeGlobalToolHosting(nil, strip, map[string]struct{}{dto.EmulatedToolTypeRecognition: {}}, true, "muse-spark-1.2")
	_, hasRecognition := merged[dto.EmulatedToolTypeRecognition]
	require.False(t, hasRecognition, "excluded kind excluded from global emulation")
}
