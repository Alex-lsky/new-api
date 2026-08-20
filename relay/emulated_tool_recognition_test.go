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
	tool_hosting.SetToolHostingForTest(map[string]tool_hosting.ToolHostingProvider{
		"g-search": {Kind: tool_hosting.KindWebSearch, Type: "tavily", APIKey: "gk"},
		"g-vision": {Kind: tool_hosting.KindRecognition, Type: "gemini", APIKey: "gk", Model: "gemini-2.5-flash"},
		"g-image":  {Kind: tool_hosting.KindImageGeneration, Type: "gemini_images", APIKey: "gk"},
	})
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

func TestResolveEmulatedToolRefs(t *testing.T) {
	// inline backend kept as-is (no ref)
	inline := map[string]*dto.EmulatedToolBackend{
		dto.EmulatedToolTypeWebSearch: {Provider: dto.EmulatedSearchProviderTavily, APIKey: "local"},
	}
	resolved := resolveEmulatedToolRefs(inline)
	require.Len(t, resolved, 1)
	assert.Equal(t, "local", resolved[dto.EmulatedToolTypeWebSearch].APIKey)

	// refs resolve to the global provider config, including channel/model
	referenced := map[string]*dto.EmulatedToolBackend{
		dto.EmulatedToolTypeWebSearch:   {Ref: "g-search"},
		dto.EmulatedToolTypeRecognition: {Ref: "g-vision"},
		dto.EmulatedToolTypeImage:       {Ref: "g-image"},
	}
	resolved = resolveEmulatedToolRefs(referenced)
	require.Len(t, resolved, 3)
	assert.Equal(t, "tavily", resolved[dto.EmulatedToolTypeWebSearch].Provider)
	assert.Equal(t, "gk", resolved[dto.EmulatedToolTypeWebSearch].APIKey)
	assert.Equal(t, "gemini", resolved[dto.EmulatedToolTypeRecognition].Provider)
	assert.Equal(t, "gemini-2.5-flash", resolved[dto.EmulatedToolTypeRecognition].Model)
	assert.Equal(t, "gemini_images", resolved[dto.EmulatedToolTypeImage].Provider)

	// unknown ref or kind mismatch -> dropped, never a request failure
	dropped := resolveEmulatedToolRefs(map[string]*dto.EmulatedToolBackend{
		dto.EmulatedToolTypeWebSearch: {Ref: "missing"},
		dto.EmulatedToolTypeImage:     {Ref: "g-search"},
	})
	require.Nil(t, dropped)
}
