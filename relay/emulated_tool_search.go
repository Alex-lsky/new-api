package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/tidwall/gjson"
)

const (
	emulatedSearchDefaultGeminiModel = "gemini-2.5-flash"
	emulatedSearchTimeout            = 30 * time.Second
	emulatedSearchMaxResults         = 8
	emulatedSearchResponseLimit      = 1 << 20
)

// searchResultItem is one normalized web result, shared by every provider so
// formatting (and the model's view) stays uniform.
type searchResultItem struct {
	Title   string
	URL     string
	Snippet string
}

// searchResults is the normalized provider output: an optional grounded
// answer plus result items.
type searchResults struct {
	Answer string
	Items  []searchResultItem
}

// executeEmulatedWebSearch runs the channel's configured search backend and
// returns a text result (with source links when the backend provides them) for
// the model to reason over. Execution errors are returned as text rather than
// failures so the model can still answer without search.
func executeEmulatedWebSearch(ctx context.Context, query string, backend *dto.EmulatedToolBackend) string {
	if backend == nil {
		return "web search is not configured on this channel"
	}
	resolved, err := withResolvedChannelCreds(ctx, backend)
	if err != nil {
		return "web search failed: " + err.Error()
	}
	var results searchResults
	switch strings.ToLower(strings.TrimSpace(resolved.Provider)) {
	case dto.EmulatedSearchProviderGemini:
		results, err = geminiGroundingSearch(ctx, query, resolved)
	case dto.EmulatedSearchProviderZhipu:
		results, err = zhipuWebSearch(ctx, query, resolved)
	case dto.EmulatedSearchProviderTavily:
		results, err = tavilySearch(ctx, query, resolved)
	case dto.EmulatedSearchProviderBrave:
		results, err = braveSearch(ctx, query, resolved)
	case dto.EmulatedSearchProviderBocha:
		results, err = bochaSearch(ctx, query, resolved)
	case dto.EmulatedSearchProviderSearXNG:
		results, err = searxngSearch(ctx, query, resolved)
	case dto.EmulatedSearchProviderHTTPJSON:
		results, err = httpJSONSearch(ctx, query, resolved)
	default:
		return fmt.Sprintf("unsupported web search provider %q", resolved.Provider)
	}
	if err != nil {
		return fmt.Sprintf("web search failed: %v", err)
	}
	return formatSearchResults(results)
}

func formatSearchResults(results searchResults) string {
	var text strings.Builder
	if results.Answer != "" {
		text.WriteString(results.Answer)
	}
	for _, item := range results.Items {
		if text.Len() > 0 {
			text.WriteString("\n\n")
		}
		title := item.Title
		if title == "" {
			title = item.URL
		}
		if item.URL != "" {
			text.WriteString(fmt.Sprintf("- [%s](%s)", title, item.URL))
		} else {
			text.WriteString(fmt.Sprintf("- %s", title))
		}
		if item.Snippet != "" {
			text.WriteString("\n  " + strings.ReplaceAll(strings.TrimSpace(item.Snippet), "\n", " "))
		}
	}
	if text.Len() == 0 {
		return "no results returned for this query"
	}
	return text.String()
}

// searchBackendBase trims and defaults the provider endpoint.
func searchBackendBase(backend *dto.EmulatedToolBackend, def string) string {
	apiBase := strings.TrimRight(strings.TrimSpace(backend.APIBase), "/")
	if apiBase == "" {
		apiBase = def
	}
	return apiBase
}

// searchHTTPRequest issues one backend request and returns the limited body.
// authHeader is "Authorization" (bearer) for most providers; Brave needs
// X-Subscription-Token instead. method/body are empty for GET.
func searchHTTPRequest(ctx context.Context, method string, endpoint string, apiKey string, authHeader string, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	requestCtx, cancel := context.WithTimeout(ctx, emulatedSearchTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		if authHeader == "" {
			authHeader = "Authorization"
			request.Header.Set(authHeader, "Bearer "+apiKey)
		} else {
			request.Header.Set(authHeader, apiKey)
		}
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, emulatedSearchResponseLimit))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("backend returned %d: %.300s", response.StatusCode, string(data))
	}
	return data, nil
}

func geminiGroundingSearch(ctx context.Context, query string, backend *dto.EmulatedToolBackend) (searchResults, error) {
	model := strings.TrimSpace(backend.Model)
	if model == "" {
		model = emulatedSearchDefaultGeminiModel
	}
	apiBase := searchBackendBase(backend, "https://generativelanguage.googleapis.com/v1beta")
	body, err := common.Marshal(map[string]any{
		"contents": []any{map[string]any{"parts": []any{map[string]any{"text": query}}}},
		"tools":    []any{map[string]any{"google_search": map[string]any{}}},
	})
	if err != nil {
		return searchResults{}, err
	}
	endpoint := fmt.Sprintf("%s/models/%s:generateContent?key=%s", apiBase, model, url.QueryEscape(backend.APIKey))
	data, err := searchHTTPRequest(ctx, http.MethodPost, endpoint, "", "", body)
	if err != nil {
		return searchResults{}, err
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
		return searchResults{}, err
	}
	if len(payload.Candidates) == 0 {
		return searchResults{}, nil
	}
	var results searchResults
	for _, part := range payload.Candidates[0].Content.Parts {
		if part.Text != "" {
			if results.Answer != "" {
				results.Answer += "\n"
			}
			results.Answer += part.Text
		}
	}
	for _, chunk := range payload.Candidates[0].GroundingMetadata.GroundingChunks {
		if chunk.Web.URI != "" {
			results.Items = append(results.Items, searchResultItem{Title: chunk.Web.Title, URL: chunk.Web.URI})
		}
	}
	return results, nil
}

// zhipuWebSearch calls the Zhipu (GLM) web search API; Model selects the
// search engine (search_std by default, search_pro / sogou / baidu ...).
func zhipuWebSearch(ctx context.Context, query string, backend *dto.EmulatedToolBackend) (searchResults, error) {
	engine := strings.TrimSpace(backend.Model)
	if engine == "" {
		engine = "search_std"
	}
	body, err := common.Marshal(map[string]any{
		"search_engine": engine,
		"search_query":  query,
	})
	if err != nil {
		return searchResults{}, err
	}
	apiBase := searchBackendBase(backend, "https://open.bigmodel.cn/api/paas/v4")
	data, err := searchHTTPRequest(ctx, http.MethodPost, apiBase+"/web_search", backend.APIKey, "", body)
	if err != nil {
		return searchResults{}, err
	}
	var payload struct {
		SearchResult []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Content string `json:"content"`
			Media   string `json:"media"`
		} `json:"search_result"`
	}
	if err := common.Unmarshal(data, &payload); err != nil {
		return searchResults{}, err
	}
	var results searchResults
	for _, item := range payload.SearchResult {
		results.Items = append(results.Items, searchResultItem{Title: item.Title, URL: item.Link, Snippet: item.Content})
	}
	return results, nil
}

func tavilySearch(ctx context.Context, query string, backend *dto.EmulatedToolBackend) (searchResults, error) {
	body, err := common.Marshal(map[string]any{
		"query":          query,
		"max_results":    emulatedSearchMaxResults,
		"include_answer": true,
	})
	if err != nil {
		return searchResults{}, err
	}
	apiBase := searchBackendBase(backend, "https://api.tavily.com")
	data, err := searchHTTPRequest(ctx, http.MethodPost, apiBase+"/search", backend.APIKey, "", body)
	if err != nil {
		return searchResults{}, err
	}
	var payload struct {
		Answer  string `json:"answer"`
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := common.Unmarshal(data, &payload); err != nil {
		return searchResults{}, err
	}
	var results searchResults
	results.Answer = payload.Answer
	for _, item := range payload.Results {
		results.Items = append(results.Items, searchResultItem{Title: item.Title, URL: item.URL, Snippet: item.Content})
	}
	return results, nil
}

func braveSearch(ctx context.Context, query string, backend *dto.EmulatedToolBackend) (searchResults, error) {
	endpoint := searchBackendBase(backend, "https://api.search.brave.com/res/v1") +
		"/web/search?q=" + url.QueryEscape(query) + "&count=" + strconv.Itoa(emulatedSearchMaxResults)
	data, err := searchHTTPRequest(ctx, http.MethodGet, endpoint, backend.APIKey, "X-Subscription-Token", nil)
	if err != nil {
		return searchResults{}, err
	}
	var payload struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := common.Unmarshal(data, &payload); err != nil {
		return searchResults{}, err
	}
	var results searchResults
	for _, item := range payload.Web.Results {
		results.Items = append(results.Items, searchResultItem{Title: item.Title, URL: item.URL, Snippet: item.Description})
	}
	return results, nil
}

func bochaSearch(ctx context.Context, query string, backend *dto.EmulatedToolBackend) (searchResults, error) {
	body, err := common.Marshal(map[string]any{
		"query":   query,
		"count":   emulatedSearchMaxResults,
		"summary": true,
	})
	if err != nil {
		return searchResults{}, err
	}
	apiBase := searchBackendBase(backend, "https://api.bochaai.com/v1")
	data, err := searchHTTPRequest(ctx, http.MethodPost, apiBase+"/web-search", backend.APIKey, "", body)
	if err != nil {
		return searchResults{}, err
	}
	var payload struct {
		Data struct {
			WebPages struct {
				Value []struct {
					Name    string `json:"name"`
					URL     string `json:"url"`
					Snippet string `json:"snippet"`
					Summary string `json:"summary"`
				} `json:"value"`
			} `json:"webPages"`
		} `json:"data"`
	}
	if err := common.Unmarshal(data, &payload); err != nil {
		return searchResults{}, err
	}
	var results searchResults
	for _, item := range payload.Data.WebPages.Value {
		snippet := item.Summary
		if snippet == "" {
			snippet = item.Snippet
		}
		results.Items = append(results.Items, searchResultItem{Title: item.Name, URL: item.URL, Snippet: snippet})
	}
	return results, nil
}

// searxngSearch queries a self-hosted SearXNG instance's JSON API.
func searxngSearch(ctx context.Context, query string, backend *dto.EmulatedToolBackend) (searchResults, error) {
	apiBase := strings.TrimRight(strings.TrimSpace(backend.APIBase), "/")
	if apiBase == "" {
		return searchResults{}, fmt.Errorf("searxng requires api_base (your instance URL)")
	}
	endpoint := apiBase + "/search?q=" + url.QueryEscape(query) + "&format=json"
	data, err := searchHTTPRequest(ctx, http.MethodGet, endpoint, "", "", nil)
	if err != nil {
		return searchResults{}, err
	}
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := common.Unmarshal(data, &payload); err != nil {
		return searchResults{}, err
	}
	var results searchResults
	for _, item := range payload.Results {
		if len(results.Items) >= emulatedSearchMaxResults {
			break
		}
		results.Items = append(results.Items, searchResultItem{Title: item.Title, URL: item.URL, Snippet: item.Content})
	}
	return results, nil
}

// httpJSONSearch runs the generic template backend: api_base is a URL
// template containing {query}; Extra["request_body"] optionally carries a
// JSON body template (also with {query}); Extra["result_path"] is the gjson
// path to a result array (objects with title/name + url/link + snippet/
// content/description fields, or plain strings) or a plain string.
func httpJSONSearch(ctx context.Context, query string, backend *dto.EmulatedToolBackend) (searchResults, error) {
	template := strings.TrimSpace(backend.APIBase)
	if template == "" || !strings.Contains(template, "{query}") {
		return searchResults{}, fmt.Errorf("http_json api_base must contain {query}")
	}
	endpoint := strings.ReplaceAll(template, "{query}", url.QueryEscape(query))
	var body []byte
	if requestBody := strings.TrimSpace(backend.Extra["request_body"]); requestBody != "" {
		if !strings.Contains(requestBody, "{query}") {
			return searchResults{}, fmt.Errorf("http_json request_body must contain {query}")
		}
		replaced, err := sjsonSetString(requestBody, "{query}", query)
		if err != nil {
			return searchResults{}, err
		}
		if !gjson.Valid(replaced) {
			return searchResults{}, fmt.Errorf("http_json request_body is not valid JSON after substitution")
		}
		body = []byte(replaced)
	}
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
	}
	data, err := searchHTTPRequest(ctx, method, endpoint, backend.APIKey, "", body)
	if err != nil {
		return searchResults{}, err
	}
	resultPath := strings.TrimSpace(backend.Extra["result_path"])
	if resultPath == "" {
		return searchResults{}, fmt.Errorf("http_json result_path is required")
	}
	result := gjson.GetBytes(data, resultPath)
	if result.Type == gjson.String {
		return searchResults{Answer: result.String()}, nil
	}
	if !result.IsArray() {
		return searchResults{}, fmt.Errorf("http_json result_path %q did not match an array or string", resultPath)
	}
	var results searchResults
	for _, item := range result.Array() {
		if len(results.Items) >= emulatedSearchMaxResults {
			break
		}
		if item.Type == gjson.String {
			results.Items = append(results.Items, searchResultItem{Snippet: item.String()})
			continue
		}
		results.Items = append(results.Items, searchResultItem{
			Title:   firstGjsonString(item, "title", "name"),
			URL:     firstGjsonString(item, "url", "link"),
			Snippet: firstGjsonString(item, "snippet", "content", "description", "summary"),
		})
	}
	return results, nil
}

func firstGjsonString(item gjson.Result, keys ...string) string {
	for _, key := range keys {
		if value := item.Get(key); value.Type == gjson.String && value.String() != "" {
			return value.String()
		}
	}
	return ""
}

// sjsonSetString substitutes a {placeholder} inside a JSON document string
// with a properly escaped JSON string value.
func sjsonSetString(document string, placeholder string, value string) (string, error) {
	escaped, err := common.Marshal(value)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(document, placeholder, strings.Trim(string(escaped), `"`)), nil
}
