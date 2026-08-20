package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecuteEmulatedWebSearchProviders(t *testing.T) {
	tests := []struct {
		name       string
		backend    dto.EmulatedToolBackend
		wantPath   string
		response   string
		wantSubstr []string
	}{
		{
			name:       "zhipu",
			backend:    dto.EmulatedToolBackend{Provider: dto.EmulatedSearchProviderZhipu, APIKey: "zk"},
			wantPath:   "/web_search",
			response:   `{"search_result":[{"title":"Zhipu Result","link":"https://zhipu.example/doc","content":"zhipu snippet"}]}`,
			wantSubstr: []string{"Zhipu Result", "https://zhipu.example/doc", "zhipu snippet"},
		},
		{
			name:       "tavily",
			backend:    dto.EmulatedToolBackend{Provider: dto.EmulatedSearchProviderTavily, APIKey: "tk"},
			wantPath:   "/search",
			response:   `{"answer":"tavily answer","results":[{"title":"Tavily Result","url":"https://tavily.example","content":"tavily snippet"}]}`,
			wantSubstr: []string{"tavily answer", "Tavily Result", "https://tavily.example"},
		},
		{
			name:       "brave",
			backend:    dto.EmulatedToolBackend{Provider: dto.EmulatedSearchProviderBrave, APIKey: "bk"},
			wantPath:   "/web/search",
			response:   `{"web":{"results":[{"title":"Brave Result","url":"https://brave.example","description":"brave snippet"}]}}`,
			wantSubstr: []string{"Brave Result", "https://brave.example", "brave snippet"},
		},
		{
			name:       "bocha",
			backend:    dto.EmulatedToolBackend{Provider: dto.EmulatedSearchProviderBocha, APIKey: "bok"},
			wantPath:   "/web-search",
			response:   `{"data":{"webPages":{"value":[{"name":"Bocha Result","url":"https://bocha.example","summary":"bocha summary"}]}}}`,
			wantSubstr: []string{"Bocha Result", "https://bocha.example", "bocha summary"},
		},
		{
			name:       "searxng",
			backend:    dto.EmulatedToolBackend{Provider: dto.EmulatedSearchProviderSearXNG},
			wantPath:   "/search",
			response:   `{"results":[{"title":"SearXNG Result","url":"https://searxng.example","content":"searxng snippet"}]}`,
			wantSubstr: []string{"SearXNG Result", "https://searxng.example", "searxng snippet"},
		},
		{
			name: "http_json get with array path",
			backend: dto.EmulatedToolBackend{
				Provider: dto.EmulatedSearchProviderHTTPJSON,
				APIBase:  "/search?q={query}",
				Extra:    map[string]string{"result_path": "items"},
			},
			wantPath:   "/search",
			response:   `{"items":[{"title":"Generic Result","url":"https://generic.example","snippet":"generic snippet"}]}`,
			wantSubstr: []string{"Generic Result", "https://generic.example", "generic snippet"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			var gotHeader http.Header
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotHeader = r.Header
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.response))
			}))
			defer server.Close()
			if strings.HasPrefix(tt.backend.APIBase, "/") {
				tt.backend.APIBase = server.URL + tt.backend.APIBase
			} else {
				tt.backend.APIBase = server.URL
			}
			result := executeEmulatedWebSearch(t.Context(), "go release notes", &tt.backend)
			assert.Equal(t, tt.wantPath, gotPath)
			for _, want := range tt.wantSubstr {
				assert.Contains(t, result, want)
			}
			if tt.backend.Provider == dto.EmulatedSearchProviderBrave {
				assert.Equal(t, "bk", gotHeader.Get("X-Subscription-Token"))
			} else if tt.backend.APIKey != "" {
				assert.Equal(t, "Bearer "+tt.backend.APIKey, gotHeader.Get("Authorization"))
			}
		})
	}
}

func TestExecuteEmulatedWebSearchCodePlanMCP(t *testing.T) {
	var gotAuthorization string
	server := mcp.NewServer(&mcp.Implementation{Name: "search-test", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: emulatedSearchCodePlanMCPToolName}, func(ctx context.Context, request *mcp.CallToolRequest, input map[string]any) (*mcp.CallToolResult, any, error) {
		assert.Equal(t, "latest Go release", input["search_query"])
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "MCP search result"}}}, nil, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(request *http.Request) *mcp.Server {
		gotAuthorization = request.Header.Get("Authorization")
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	result := executeEmulatedWebSearch(t.Context(), "latest Go release", &dto.EmulatedToolBackend{
		Provider: dto.EmulatedSearchProviderZhipuCodePlanSearchMCP,
		APIKey:   "code-plan-key",
		APIBase:  httpServer.URL,
	})
	assert.Equal(t, "Bearer code-plan-key", gotAuthorization)
	assert.Equal(t, "MCP search result", result)
}

func TestExecuteEmulatedWebSearchHTTPJSONPost(t *testing.T) {
	var gotMethod, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		buf := make([]byte, 512)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		_, _ = w.Write([]byte(`{"answer":"posted"}`))
	}))
	defer server.Close()

	backend := &dto.EmulatedToolBackend{
		Provider: dto.EmulatedSearchProviderHTTPJSON,
		APIBase:  server.URL + "/q={query}",
		Extra: map[string]string{
			"request_body": `{"query":"{query}","count":3}`,
			"result_path":  "answer",
		},
	}
	result := executeEmulatedWebSearch(t.Context(), "hello world", backend)
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Contains(t, gotBody, `"query":"hello world"`)
	assert.Contains(t, gotBody, `"count":3`)
	assert.Equal(t, "posted", result)
}

func TestExecuteEmulatedWebSearchErrorsAsText(t *testing.T) {
	assert.Contains(t, executeEmulatedWebSearch(t.Context(), "q", nil), "not configured")
	assert.Contains(t, executeEmulatedWebSearch(t.Context(), "q", &dto.EmulatedToolBackend{Provider: "nope", APIKey: "k"}), "unsupported web search provider")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer server.Close()
	result := executeEmulatedWebSearch(t.Context(), "q", &dto.EmulatedToolBackend{Provider: dto.EmulatedSearchProviderTavily, APIKey: "k", APIBase: server.URL})
	assert.Contains(t, result, "web search failed")
	assert.Contains(t, result, "401")
}

func TestExecuteEmulatedImageGenerationOpenAIURLDownload(t *testing.T) {
	const pngBytes = "pngbytes"
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pngBytes))
	}))
	defer imageServer.Close()
	var gotPrompt string
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 512)
		n, _ := r.Body.Read(buf)
		gotPrompt = string(buf[:n])
		_, _ = w.Write([]byte(`{"data":[{"url":"` + imageServer.URL + `/img.png"}]}`))
	}))
	defer apiServer.Close()

	result := executeEmulatedImageGeneration(t.Context(), `{"prompt":"a cat","size":"1024x1024","quality":"high"}`, &dto.EmulatedToolBackend{
		Provider: dto.EmulatedImageProviderOpenAI, APIKey: "k", APIBase: apiServer.URL,
	})
	assert.Contains(t, gotPrompt, `"prompt":"a cat"`)
	assert.Contains(t, gotPrompt, `"size":"1024x1024"`)
	assert.Contains(t, gotPrompt, `"quality":"high"`)
	assert.Contains(t, result.Output, "Image generated")
	assert.NotContains(t, result.Output, pngBytes)
	assert.Equal(t, pngBytes, decodeBase64OrEmpty(result.ImageB64))
}

func TestExecuteEmulatedImageGenerationGemini(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, ":generateContent")
		assert.NotEmpty(t, r.URL.Query().Get("key"))
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"Z2VtaW5pLWJ5dGVz"}}]}}]}`))
	}))
	defer server.Close()

	result := executeEmulatedImageGeneration(t.Context(), `{"prompt":"a dog","size":"1792x1024"}`, &dto.EmulatedToolBackend{
		Provider: dto.EmulatedImageProviderGemini, APIKey: "gk", APIBase: server.URL,
	})
	assert.Contains(t, result.Output, "Image generated")
	assert.Equal(t, "gemini-bytes", decodeBase64OrEmpty(result.ImageB64))
}

func TestExecuteEmulatedImageGenerationFailuresAsText(t *testing.T) {
	require.NotEmpty(t, executeEmulatedImageGeneration(t.Context(), `{}`, nil).Output, "no backend")
	require.Contains(t, executeEmulatedImageGeneration(t.Context(), `{"size":"1024x1024"}`, &dto.EmulatedToolBackend{Provider: dto.EmulatedImageProviderOpenAI, APIKey: "k"}).Output, "prompt is required")
	require.Contains(t, executeEmulatedImageGeneration(t.Context(), `{"prompt":"x"}`, &dto.EmulatedToolBackend{Provider: "nope", APIKey: "k"}).Output, "unsupported image generation provider")
}

func TestParseEmulatedImageArgsBounds(t *testing.T) {
	args := parseEmulatedImageArgs(`{"prompt":"  p  ","size":"9999x10","quality":"ultra"}`)
	assert.Equal(t, "p", args.Prompt)
	assert.Empty(t, args.Size, "non standard size dropped")
	assert.Empty(t, args.Quality, "unknown quality dropped")

	args = parseEmulatedImageArgs(`{"prompt":"p","size":"1024x1024","quality":"hd"}`)
	assert.Equal(t, "1024x1024", args.Size)
	assert.Equal(t, "hd", args.Quality)
}

func decodeBase64OrEmpty(data string) string {
	decoded, err := decodeBase64Image(data)
	if err != nil {
		return ""
	}
	return string(decoded)
}
