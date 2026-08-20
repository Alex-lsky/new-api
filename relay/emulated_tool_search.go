package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

const (
	emulatedSearchProviderGemini = "gemini"
	emulatedSearchDefaultModel   = "gemini-2.5-flash"
	emulatedSearchTimeout        = 30 * time.Second
)

// executeEmulatedWebSearch runs the channel's configured search backend and
// returns a text result (with source links when the backend provides them) for
// the model to reason over. Execution errors are returned as text rather than
// failures so the model can still answer without search.
func executeEmulatedWebSearch(ctx context.Context, query string, backend *dto.EmulatedToolBackend) string {
	if backend == nil {
		return "web search is not configured on this channel"
	}
	switch strings.ToLower(strings.TrimSpace(backend.Provider)) {
	case emulatedSearchProviderGemini:
		return geminiGroundingSearch(ctx, query, backend)
	default:
		return fmt.Sprintf("unsupported web search provider %q", backend.Provider)
	}
}

func geminiGroundingSearch(ctx context.Context, query string, backend *dto.EmulatedToolBackend) string {
	model := strings.TrimSpace(backend.Model)
	if model == "" {
		model = emulatedSearchDefaultModel
	}
	apiBase := strings.TrimRight(strings.TrimSpace(backend.APIBase), "/")
	if apiBase == "" {
		apiBase = "https://generativelanguage.googleapis.com/v1beta"
	}
	body, err := common.Marshal(map[string]any{
		"contents": []any{map[string]any{"parts": []any{map[string]any{"text": query}}}},
		"tools":    []any{map[string]any{"google_search": map[string]any{}}},
	})
	if err != nil {
		return fmt.Sprintf("search request build failed: %v", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", apiBase, model, backend.APIKey)
	requestCtx, cancel := context.WithTimeout(ctx, emulatedSearchTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Sprintf("search request build failed: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Sprintf("web search failed: %v", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Sprintf("web search response read failed: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Sprintf("web search backend returned %d: %.300s", response.StatusCode, string(data))
	}

	var payload struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			GroundingMetadata struct {
				GroundingChunks []struct {
					Web struct {
						URI   string `json:"uri"`
						Title string `json:"title"`
					} `json:"web"`
				} `json:"groundingChunks"`
			} `json:"groundingMetadata"`
		} `json:"candidates"`
	}
	if err := common.Unmarshal(data, &payload); err != nil {
		return fmt.Sprintf("web search response parse failed: %v", err)
	}

	var text strings.Builder
	var sources []string
	if len(payload.Candidates) > 0 {
		for _, part := range payload.Candidates[0].Content.Parts {
			if part.Text != "" {
				if text.Len() > 0 {
					text.WriteString("\n")
				}
				text.WriteString(part.Text)
			}
		}
		for _, chunk := range payload.Candidates[0].GroundingMetadata.GroundingChunks {
			if chunk.Web.URI != "" {
				title := chunk.Web.Title
				if title == "" {
					title = chunk.Web.URI
				}
				sources = append(sources, fmt.Sprintf("- [%s](%s)", title, chunk.Web.URI))
			}
		}
	}
	if text.Len() == 0 && len(sources) == 0 {
		return "no results returned for this query"
	}
	if len(sources) > 0 {
		if text.Len() > 0 {
			text.WriteString("\n\nSources:\n")
		} else {
			text.WriteString("Sources:\n")
		}
		text.WriteString(strings.Join(sources, "\n"))
	}
	return text.String()
}
