package common

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpClientName    = "new-api"
	mcpClientVersion = "1"
)

type MCPCommand struct {
	Name string
	Args []string
	Env  []string
}

type MCPToolCall struct {
	ToolName  string
	Arguments map[string]any
}

func CallRemoteMCPTool(ctx context.Context, endpoint string, apiKey string, httpClient *http.Client, call MCPToolCall) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", fmt.Errorf("MCP endpoint is required")
	}
	endpointURL, err := url.Parse(endpoint)
	if err != nil || (endpointURL.Scheme != "http" && endpointURL.Scheme != "https") || endpointURL.Host == "" {
		return "", fmt.Errorf("MCP endpoint must use HTTP(S)")
	}
	client := httpClient
	if client == nil {
		client = http.DefaultClient
	}
	if apiKey = strings.TrimSpace(apiKey); apiKey != "" {
		redirectPolicy := client.CheckRedirect
		client = &http.Client{
			Transport: &mcpBearerTransport{
				base:   client.Transport,
				apiKey: apiKey,
				scheme: endpointURL.Scheme,
				host:   endpointURL.Host,
			},
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if !strings.EqualFold(request.URL.Scheme, endpointURL.Scheme) || !strings.EqualFold(request.URL.Host, endpointURL.Host) {
					return fmt.Errorf("MCP redirect to a different origin is not allowed")
				}
				if redirectPolicy != nil {
					return redirectPolicy(request, via)
				}
				if len(via) >= 10 {
					return fmt.Errorf("stopped after 10 redirects")
				}
				return nil
			},
			Jar:     client.Jar,
			Timeout: client.Timeout,
		}
	}
	transport := &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           client,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}
	return callMCPTool(ctx, transport, call)
}

func CallCommandMCPTool(ctx context.Context, command MCPCommand, call MCPToolCall) (string, error) {
	name := strings.TrimSpace(command.Name)
	if name == "" {
		return "", fmt.Errorf("MCP command is required")
	}
	cmd := exec.CommandContext(ctx, name, command.Args...)
	cmd.Env = append(cmd.Environ(), command.Env...)
	transport := &mcp.CommandTransport{Command: cmd}
	return callMCPTool(ctx, transport, call)
}

func callMCPTool(ctx context.Context, transport mcp.Transport, call MCPToolCall) (string, error) {
	toolName := strings.TrimSpace(call.ToolName)
	if toolName == "" {
		return "", fmt.Errorf("MCP tool name is required")
	}
	client := mcp.NewClient(&mcp.Implementation{Name: mcpClientName, Version: mcpClientVersion}, &mcp.ClientOptions{
		Capabilities: &mcp.ClientCapabilities{},
	})
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return "", fmt.Errorf("connect MCP client: %w", err)
	}
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: call.Arguments,
	})
	if err != nil {
		return "", fmt.Errorf("call MCP tool %q: %w", toolName, err)
	}
	text, err := mcpToolResultText(result)
	if err != nil {
		return "", fmt.Errorf("MCP tool %q: %w", toolName, err)
	}
	return text, nil
}

func mcpToolResultText(result *mcp.CallToolResult) (string, error) {
	if result == nil {
		return "", fmt.Errorf("returned no result")
	}
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			if value := strings.TrimSpace(text.Text); value != "" {
				parts = append(parts, value)
			}
		}
	}
	if len(parts) == 0 && result.StructuredContent != nil {
		data, err := Marshal(result.StructuredContent)
		if err != nil {
			return "", fmt.Errorf("marshal structured result: %w", err)
		}
		parts = append(parts, string(data))
	}
	text := strings.Join(parts, "\n")
	if result.IsError {
		if text == "" {
			text = "server reported a tool error"
		}
		return "", fmt.Errorf("%s", text)
	}
	if text == "" {
		return "", fmt.Errorf("returned no text content")
	}
	return text, nil
}

type mcpBearerTransport struct {
	base   http.RoundTripper
	apiKey string
	scheme string
	host   string
}

func (t *mcpBearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	if strings.EqualFold(request.URL.Scheme, t.scheme) && strings.EqualFold(request.URL.Host, t.host) {
		clone.Header.Set("Authorization", "Bearer "+t.apiKey)
	} else {
		clone.Header.Del("Authorization")
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}
