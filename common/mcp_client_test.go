package common

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallRemoteMCPToolBearerAndArguments(t *testing.T) {
	var authorization string
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "lookup"}, func(ctx context.Context, request *mcp.CallToolRequest, input map[string]any) (*mcp.CallToolResult, any, error) {
		assert.Equal(t, "hello", input["query"])
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "one"}, &mcp.TextContent{Text: "two"}}}, nil, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(request *http.Request) *mcp.Server {
		authorization = request.Header.Get("Authorization")
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	result, err := CallRemoteMCPTool(t.Context(), httpServer.URL, "secret", nil, MCPToolCall{
		ToolName:  "lookup",
		Arguments: map[string]any{"query": "hello"},
	})
	require.NoError(t, err)
	assert.Equal(t, "Bearer secret", authorization)
	assert.Equal(t, "one\ntwo", result)
}

func TestMCPBearerTransportDoesNotLeakAcrossOrigins(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	transport := &mcpBearerTransport{apiKey: "secret", scheme: "https", host: "mcp.example"}
	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	response.Body.Close()
	assert.Empty(t, authorization)
}

func TestMCPToolResultTextStructuredAndError(t *testing.T) {
	result, err := mcpToolResultText(&mcp.CallToolResult{StructuredContent: map[string]any{"ok": true}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, result)

	_, err = mcpToolResultText(&mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "bad request"}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad request")
}
