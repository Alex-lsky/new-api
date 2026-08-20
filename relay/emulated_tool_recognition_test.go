package relay

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/setting/tool_hosting"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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

func allowPrivateImageFetchForTest(t *testing.T) {
	t.Helper()
	service.InitHttpClient()
	fetchSetting := system_setting.GetFetchSetting()
	original := *fetchSetting
	t.Cleanup(func() { *fetchSetting = original })
	fetchSetting.EnableSSRFProtection = true
	fetchSetting.AllowPrivateIp = true
	fetchSetting.DomainFilterMode = false
	fetchSetting.IpFilterMode = false
	fetchSetting.DomainList = nil
	fetchSetting.IpList = nil
	fetchSetting.AllowedPorts = []string{"1-65535"}
	fetchSetting.ApplyIPFilterForDomain = false
}

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
	allowPrivateImageFetchForTest(t)
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

func TestWriteMCPVisionImageSecureTemporaryFile(t *testing.T) {
	path, cleanup, err := writeMCPVisionImage(t.Context(), testImageURL)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	assert.Equal(t, ".png", filepath.Ext(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	cleanup()
	_, err = os.Stat(path)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestImageBytesForVisionRejectsLocalAndOversizeData(t *testing.T) {
	_, _, err := imageBytesForVision(t.Context(), "file:///etc/passwd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP(S)")

	oversize := "data:image/png;base64," + strings.Repeat("A", base64.StdEncoding.EncodedLen(emulatedRecognitionImageLimit+1))
	_, _, err = imageBytesForVision(t.Context(), oversize)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}

func TestWriteMCPVisionImageRejectsOversizeRemoteImage(t *testing.T) {
	allowPrivateImageFetchForTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})
		_, _ = io.CopyN(w, zeroReader{}, emulatedRecognitionImageLimit)
	}))
	defer server.Close()

	path, cleanup, err := writeMCPVisionImage(t.Context(), server.URL)
	cleanup()
	assert.Empty(t, path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	for i := range buffer {
		buffer[i] = 0
	}
	return len(buffer), nil
}

func TestExecuteEmulatedImageRecognitionCodePlanMCP(t *testing.T) {
	t.Setenv(emulatedRecognitionCodePlanMCPCommandEnv, os.Args[0])
	t.Setenv(emulatedRecognitionCodePlanMCPScriptEnv, "-test.run=TestCodePlanVisionMCPHelperProcess")
	t.Setenv("GO_WANT_CODE_PLAN_VISION_MCP_HELPER", "1")

	result := executeEmulatedImageRecognition(t.Context(), `{"question":"describe image","image_url":"`+testImageURL+`"}`, &dto.EmulatedToolBackend{
		Provider: dto.EmulatedRecognitionProviderZhipuCodePlanVisionMCP,
		APIKey:   "vision-key",
	}, nil)
	assert.Equal(t, "vision result", result.Output)
	assert.Equal(t, "vision result", result.Summary)
}

func TestCodePlanVisionMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CODE_PLAN_VISION_MCP_HELPER") != "1" {
		return
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "vision-test", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: emulatedRecognitionCodePlanMCPToolName}, func(ctx context.Context, request *mcp.CallToolRequest, input map[string]any) (*mcp.CallToolResult, any, error) {
		imagePath, _ := input["image_source"].(string)
		if strings.Contains(imagePath, "..") || !filepath.IsAbs(imagePath) {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "unsafe image path"}}}, nil, nil
		}
		if os.Getenv("Z_AI_API_KEY") != "vision-key" || os.Getenv("Z_AI_MODE") != "ZHIPU" {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "missing MCP environment"}}}, nil, nil
		}
		if input["prompt"] != "describe image" {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "wrong prompt"}}}, nil, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "vision result"}}}, nil, nil
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
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

func TestApplyChannelCredentialsMCPUsesKeyOnly(t *testing.T) {
	search := applyChannelCredentials(&dto.EmulatedToolBackend{
		Provider: dto.EmulatedSearchProviderZhipuCodePlanSearchMCP,
		APIBase:  "https://custom-mcp.example/mcp",
	}, "borrowed-key", "https://channel.example/v1")
	assert.Equal(t, "borrowed-key", search.APIKey)
	assert.Equal(t, "https://custom-mcp.example/mcp", search.APIBase)

	vision := applyChannelCredentials(&dto.EmulatedToolBackend{
		Provider: dto.EmulatedRecognitionProviderZhipuCodePlanVisionMCP,
	}, "borrowed-key", "https://channel.example/v1")
	assert.Equal(t, "borrowed-key", vision.APIKey)
	assert.Empty(t, vision.APIBase)

	gemini := applyChannelCredentials(&dto.EmulatedToolBackend{
		Provider: dto.EmulatedSearchProviderGemini,
	}, "borrowed-key", "https://channel.example/v1")
	assert.Equal(t, "borrowed-key", gemini.APIKey)
	assert.Equal(t, "https://channel.example/v1", gemini.APIBase)
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
