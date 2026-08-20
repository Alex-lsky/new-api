package relay

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/tidwall/gjson"
)

const (
	emulatedRecognitionDefaultModel       = "gemini-2.5-flash"
	emulatedRecognitionDefaultOpenAIModel = "gpt-4o-mini"
	emulatedRecognitionTimeout            = 120 * time.Second
	emulatedRecognitionQuestionMaxRunes   = 2000
	emulatedRecognitionImageLimit         = 20 << 20
)

// emulatedRecognitionArgs is the function-call shape the gateway exposes to
// the model for the injected image_recognition tool.
type emulatedRecognitionArgs struct {
	ImageURL string
	Question string
}

func parseEmulatedRecognitionArgs(arguments string) emulatedRecognitionArgs {
	var raw struct {
		ImageURL string `json:"image_url"`
		Question string `json:"question"`
	}
	if err := common.Unmarshal([]byte(arguments), &raw); err != nil {
		return emulatedRecognitionArgs{}
	}
	args := emulatedRecognitionArgs{
		ImageURL: strings.TrimSpace(raw.ImageURL),
		Question: strings.TrimSpace(raw.Question),
	}
	if len([]rune(args.Question)) > emulatedRecognitionQuestionMaxRunes {
		args.Question = string([]rune(args.Question)[:emulatedRecognitionQuestionMaxRunes])
	}
	return args
}

// emulatedRecognitionResult carries the vision outcome: Output is the text fed
// back to the model, Summary the short text surfaced in the restored
// image_recognition_call item.
type emulatedRecognitionResult struct {
	Output  string
	Summary string
}

// executeEmulatedImageRecognition analyzes an image with the configured vision
// backend. When the model call does not include an image_url, the gateway uses
// the most recent user input_image in the request (so text-only upstreams like
// opencode zen can read images the user attached).
func executeEmulatedImageRecognition(ctx context.Context, arguments string, backend *dto.EmulatedToolBackend, requestBody []byte) emulatedRecognitionResult {
	if backend == nil {
		return emulatedRecognitionResult{Output: "image recognition is not configured on this channel"}
	}
	args := parseEmulatedRecognitionArgs(arguments)
	imageURL := args.ImageURL
	if imageURL == "" {
		imageURL = lastInputImageFromBody(requestBody)
		if imageURL == "" {
			return emulatedRecognitionResult{Output: "image recognition failed: no image provided in the call or the request"}
		}
	}
	question := args.Question
	if question == "" {
		question = "Describe this image in detail."
	}

	provider := strings.ToLower(strings.TrimSpace(backend.Provider))
	executor := strings.ToLower(strings.TrimSpace(backend.Executor))
	if provider == dto.EmulatedChannelProvider {
		apiKey, apiBase, err := resolveEmulatedBackend(ctx, backend)
		if err != nil {
			return emulatedRecognitionResult{Output: "image recognition failed: " + err.Error()}
		}
		if executor == "" || executor == dto.EmulatedRecognitionProviderOpenAI {
			return openaiVisionRecognize(ctx, imageURL, question, apiKey, apiBase, recognitionBackendModel(backend, emulatedRecognitionDefaultOpenAIModel))
		}
		if executor == dto.EmulatedRecognitionProviderGemini {
			return geminiVisionRecognize(ctx, imageURL, question, apiKey, apiBase, recognitionBackendModel(backend, emulatedRecognitionDefaultModel))
		}
		return emulatedRecognitionResult{Output: fmt.Sprintf("image recognition failed: unsupported executor %q", backend.Executor)}
	}

	apiKey, apiBase, err := resolveEmulatedBackend(ctx, backend)
	if err != nil {
		return emulatedRecognitionResult{Output: "image recognition failed: " + err.Error()}
	}
	switch provider {
	case dto.EmulatedRecognitionProviderGemini:
		return geminiVisionRecognize(ctx, imageURL, question, apiKey, apiBase, recognitionBackendModel(backend, emulatedRecognitionDefaultModel))
	case dto.EmulatedRecognitionProviderOpenAI:
		return openaiVisionRecognize(ctx, imageURL, question, apiKey, apiBase, recognitionBackendModel(backend, emulatedRecognitionDefaultOpenAIModel))
	default:
		return emulatedRecognitionResult{Output: fmt.Sprintf("image recognition failed: unsupported provider %q", backend.Provider)}
	}
}

func recognitionBackendModel(backend *dto.EmulatedToolBackend, def string) string {
	if strings.TrimSpace(backend.Model) != "" {
		return strings.TrimSpace(backend.Model)
	}
	return def
}

// lastInputImageFromBody returns the image_url of the most recent user
// input_image content part in the Responses request input.
func lastInputImageFromBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return ""
	}
	var found string
	for _, item := range input.Array() {
		switch strings.ToLower(strings.TrimSpace(item.Get("type").String())) {
		case "input_image":
			if url := firstGjsonString(item, "image_url", "file_url", "url", "file"); url != "" {
				found = url
			}
		case "message":
			for _, part := range item.Get("content").Array() {
				if strings.ToLower(strings.TrimSpace(part.Get("type").String())) != "input_image" {
					continue
				}
				if url := firstGjsonString(part, "image_url", "file_url", "url", "file"); url != "" {
					found = url
				}
			}
		}
	}
	return found
}

// geminiVisionRecognize calls a Gemini native generateContent endpoint with an
// inline image and returns the model's text answer.
func geminiVisionRecognize(ctx context.Context, imageURL string, question string, apiKey string, apiBase string, model string) emulatedRecognitionResult {
	mime, data, err := imageForVision(imageURL)
	if err != nil {
		return emulatedRecognitionResult{Output: "image recognition failed: " + err.Error()}
	}
	parts := []any{map[string]any{"inlineData": map[string]any{"mimeType": mime, "data": data}}}
	if question != "" {
		parts = append(parts, map[string]any{"text": question})
	}
	body, err := common.Marshal(map[string]any{"contents": []any{map[string]any{"parts": parts}}})
	if err != nil {
		return emulatedRecognitionResult{Output: "image recognition failed: " + err.Error()}
	}
	goAPIBase := apiBase
	if goAPIBase == "" {
		goAPIBase = "https://generativelanguage.googleapis.com/v1beta"
	}
	endpoint := fmt.Sprintf("%s/models/%s:generateContent", strings.TrimRight(goAPIBase, "/"), model)
	if apiKey != "" {
		endpoint += "?key=" + apiKey
	}
	raw, err := imageHTTPRequest(ctx, http.MethodPost, endpoint, "", body)
	if err != nil {
		return emulatedRecognitionResult{Output: "image recognition failed: " + err.Error()}
	}
	var payload struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := common.Unmarshal(raw, &payload); err != nil {
		return emulatedRecognitionResult{Output: "image recognition failed: " + err.Error()}
	}
	var text strings.Builder
	for _, candidate := range payload.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				if text.Len() > 0 {
					text.WriteString("\n")
				}
				text.WriteString(part.Text)
			}
		}
	}
	if text.Len() == 0 {
		return emulatedRecognitionResult{Output: "image recognition failed: backend returned no text"}
	}
	return emulatedRecognitionResult{Output: text.String(), Summary: truncateTextForSummary(text.String())}
}

// openaiVisionRecognize calls an OpenAI-compatible chat-completions endpoint
// with an image_url content part.
func openaiVisionRecognize(ctx context.Context, imageURL string, question string, apiKey string, apiBase string, model string) emulatedRecognitionResult {
	goAPIBase := apiBase
	if goAPIBase == "" {
		goAPIBase = "https://api.openai.com/v1"
	}
	body, err := common.Marshal(map[string]any{
		"model": model,
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "text", "text": question},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL}},
			},
		}},
		"max_tokens": 1024,
	})
	if err != nil {
		return emulatedRecognitionResult{Output: "image recognition failed: " + err.Error()}
	}
	data, err := imageHTTPRequest(ctx, http.MethodPost, strings.TrimRight(goAPIBase, "/")+"/chat/completions", apiKey, body)
	if err != nil {
		return emulatedRecognitionResult{Output: "image recognition failed: " + err.Error()}
	}
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := common.Unmarshal(data, &payload); err != nil {
		return emulatedRecognitionResult{Output: "image recognition failed: " + err.Error()}
	}
	if len(payload.Choices) == 0 || strings.TrimSpace(payload.Choices[0].Message.Content) == "" {
		return emulatedRecognitionResult{Output: "image recognition failed: backend returned no text"}
	}
	text := strings.TrimSpace(payload.Choices[0].Message.Content)
	return emulatedRecognitionResult{Output: text, Summary: truncateTextForSummary(text)}
}

// imageForVision normalizes any image reference (data URL or remote URL) into
// a mime type + base64 payload for inline Gemini parts.
func imageForVision(imageURL string) (string, string, error) {
	if data, mime, ok := parseDataURL(imageURL); ok {
		if mime == "" {
			mime = "image/png"
		}
		return mime, data, nil
	}
	requestCtx, cancel := context.WithTimeout(context.Background(), emulatedRecognitionTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, imageURL, nil)
	if err != nil {
		return "", "", err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("image download returned %d", response.StatusCode)
	}
	binary, err := io.ReadAll(io.LimitReader(response.Body, emulatedRecognitionImageLimit))
	if err != nil {
		return "", "", err
	}
	mime := response.Header.Get("Content-Type")
	if !strings.HasPrefix(mime, "image/") {
		if sniffed := sniffImageMime(binary); sniffed != "" {
			mime = sniffed
		} else {
			mime = "image/png"
		}
	}
	return base64.StdEncoding.EncodeToString(binary), mime, nil
}

func parseDataURL(imageURL string) (string, string, bool) {
	if !strings.HasPrefix(imageURL, "data:") {
		return "", "", false
	}
	comma := strings.Index(imageURL, ",")
	if comma < 0 {
		return "", "", false
	}
	header := imageURL[:comma]
	b64 := imageURL[comma+1:]
	mime := "image/png"
	if semicolon := strings.Index(header, ";"); semicolon > 5 {
		mime = header[5:semicolon]
	}
	return b64, mime, true
}

func sniffImageMime(binary []byte) string {
	switch {
	case len(binary) >= 8 && binary[0] == 0x89 && binary[1] == 'P' && binary[2] == 'N' && binary[3] == 'G':
		return "image/png"
	case len(binary) >= 3 && binary[0] == 0xFF && binary[1] == 0xD8 && binary[2] == 0xFF:
		return "image/jpeg"
	case len(binary) >= 6 && binary[0] == 'G' && binary[1] == 'I' && binary[2] == 'F' && binary[3] == '8':
		return "image/gif"
	case len(binary) >= 12 && string(binary[0:4]) == "RIFF" && string(binary[8:12]) == "WEBP":
		return "image/webp"
	default:
		return ""
	}
}

func truncateTextForSummary(text string) string {
	const summaryMaxRunes = 200
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= summaryMaxRunes {
		return string(runes)
	}
	return string(runes[:summaryMaxRunes]) + "…"
}
