package relay

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/tidwall/gjson"
)

const (
	emulatedRecognitionDefaultModel              = "gemini-2.5-flash"
	emulatedRecognitionDefaultOpenAIModel        = "gpt-4o-mini"
	emulatedRecognitionDefaultCodePlanMCPCommand = "node"
	emulatedRecognitionDefaultCodePlanMCPScript  = "/opt/zai-mcp/node_modules/@z_ai/mcp-server/build/index.js"
	emulatedRecognitionCodePlanMCPCommandEnv     = "ZHIPU_CODE_PLAN_VISION_MCP_COMMAND"
	emulatedRecognitionCodePlanMCPScriptEnv      = "ZHIPU_CODE_PLAN_VISION_MCP_SCRIPT"
	emulatedRecognitionCodePlanMCPToolName       = "analyze_image"
	emulatedRecognitionTimeout                   = 120 * time.Second
	emulatedRecognitionQuestionMaxRunes          = 2000
	emulatedRecognitionImageLimit                = 20 << 20
	emulatedRecognitionMCPConcurrency            = 4
)

var emulatedRecognitionMCPSlots = make(chan struct{}, emulatedRecognitionMCPConcurrency)

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
// opencode zen can read images the user attached); when those parts were
// stripped for this channel the pre-strip image is passed as fallbackImage.
func executeEmulatedImageRecognition(ctx context.Context, arguments string, backend *dto.EmulatedToolBackend, requestBody []byte, fallbackImage string) emulatedRecognitionResult {
	if backend == nil {
		return emulatedRecognitionResult{Output: "image recognition is not configured on this channel"}
	}
	args := parseEmulatedRecognitionArgs(arguments)
	imageURL := args.ImageURL
	if imageURL == "" {
		imageURL = lastInputImageFromBody(requestBody)
		if imageURL == "" {
			imageURL = fallbackImage
		}
		if imageURL == "" {
			return emulatedRecognitionResult{Output: "image recognition failed: no image provided in the call or the request"}
		}
	}
	question := args.Question
	if question == "" {
		question = "Describe this image in detail."
	}

	provider := strings.ToLower(strings.TrimSpace(backend.Provider))
	if provider == dto.EmulatedChannelProvider {
		// legacy provider=="channel" shape: executor decides the call format
		executor := strings.ToLower(strings.TrimSpace(backend.Executor))
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

	resolved, err := withResolvedChannelCreds(ctx, backend)
	if err != nil {
		return emulatedRecognitionResult{Output: "image recognition failed: " + err.Error()}
	}
	switch provider {
	case dto.EmulatedRecognitionProviderGemini:
		return geminiVisionRecognize(ctx, imageURL, question, resolved.APIKey, resolved.APIBase, recognitionBackendModel(resolved, emulatedRecognitionDefaultModel))
	case dto.EmulatedRecognitionProviderOpenAI:
		return openaiVisionRecognize(ctx, imageURL, question, resolved.APIKey, resolved.APIBase, recognitionBackendModel(resolved, emulatedRecognitionDefaultOpenAIModel))
	case dto.EmulatedRecognitionProviderZhipuCodePlanVisionMCP:
		return zhipuCodePlanMCPRecognize(ctx, imageURL, question, resolved.APIKey)
	default:
		return emulatedRecognitionResult{Output: fmt.Sprintf("image recognition failed: unsupported provider %q", backend.Provider)}
	}
}

func zhipuCodePlanMCPRecognize(ctx context.Context, imageURL string, question string, apiKey string) emulatedRecognitionResult {
	requestCtx, cancel := context.WithTimeout(ctx, emulatedRecognitionTimeout)
	defer cancel()
	select {
	case emulatedRecognitionMCPSlots <- struct{}{}:
		defer func() { <-emulatedRecognitionMCPSlots }()
	case <-requestCtx.Done():
		return emulatedRecognitionResult{Output: "image recognition failed: " + requestCtx.Err().Error()}
	}
	imagePath, cleanup, err := writeMCPVisionImage(requestCtx, imageURL)
	if err != nil {
		return emulatedRecognitionResult{Output: "image recognition failed: " + err.Error()}
	}
	defer cleanup()

	command := strings.TrimSpace(os.Getenv(emulatedRecognitionCodePlanMCPCommandEnv))
	if command == "" {
		command = emulatedRecognitionDefaultCodePlanMCPCommand
	}
	script := strings.TrimSpace(os.Getenv(emulatedRecognitionCodePlanMCPScriptEnv))
	if script == "" {
		script = emulatedRecognitionDefaultCodePlanMCPScript
	}
	text, err := common.CallCommandMCPTool(requestCtx, common.MCPCommand{
		Name: command,
		Args: []string{script},
		Env: []string{
			"Z_AI_API_KEY=" + apiKey,
			"Z_AI_MODE=ZHIPU",
		},
	}, common.MCPToolCall{
		ToolName: emulatedRecognitionCodePlanMCPToolName,
		Arguments: map[string]any{
			"image_source": imagePath,
			"prompt":       question,
		},
	})
	if err != nil {
		return emulatedRecognitionResult{Output: "image recognition failed: " + err.Error()}
	}
	text = strings.TrimSpace(text)
	return emulatedRecognitionResult{Output: text, Summary: truncateTextForSummary(text)}
}

func writeMCPVisionImage(ctx context.Context, imageURL string) (string, func(), error) {
	data, mime, err := imageBytesForVision(ctx, imageURL)
	if err != nil {
		return "", func() {}, err
	}
	extension := ".png"
	if mime == "image/jpeg" {
		extension = ".jpg"
	}
	file, err := os.CreateTemp("", "new-api-mcp-vision-*"+extension)
	if err != nil {
		return "", func() {}, err
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return filepath.Clean(path), cleanup, nil
}

func imageBytesForVision(ctx context.Context, imageURL string) ([]byte, string, error) {
	if data, mime, ok := parseDataURL(imageURL); ok {
		if base64.StdEncoding.DecodedLen(len(data)) > emulatedRecognitionImageLimit {
			return nil, "", fmt.Errorf("image exceeds %d byte limit", emulatedRecognitionImageLimit)
		}
		decoded, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil, "", fmt.Errorf("decode image data: %w", err)
		}
		if len(decoded) > emulatedRecognitionImageLimit {
			return nil, "", fmt.Errorf("image exceeds %d byte limit", emulatedRecognitionImageLimit)
		}
		if sniffed := sniffImageMime(decoded); sniffed != "" {
			mime = sniffed
		}
		if mime != "image/png" && mime != "image/jpeg" {
			return nil, "", fmt.Errorf("unsupported image format %q", mime)
		}
		return decoded, mime, nil
	}
	parsedURL, err := url.Parse(imageURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return nil, "", fmt.Errorf("image URL must use HTTP(S)")
	}
	requestCtx, cancel := context.WithTimeout(ctx, emulatedRecognitionTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return nil, "", err
	}
	// CDNs like upload.wikimedia.org reject default Go UA with 403
	request.Header.Set("User-Agent", "new-api/"+common.Version+" (+https://github.com/QuantumNous/new-api)")
	request.Header.Set("Accept", "image/png, image/jpeg, image/*")
	response, err := service.GetSSRFProtectedHTTPClient().Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("image download returned %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, emulatedRecognitionImageLimit+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > emulatedRecognitionImageLimit {
		return nil, "", fmt.Errorf("image exceeds %d byte limit", emulatedRecognitionImageLimit)
	}
	mime := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	if sniffed := sniffImageMime(data); sniffed != "" {
		mime = sniffed
	}
	if mime != "image/png" && mime != "image/jpeg" {
		return nil, "", fmt.Errorf("unsupported image format %q", mime)
	}
	return data, mime, nil
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
			if url := inputImageURL(item); url != "" {
				found = url
			}
		case "message":
			for _, part := range item.Get("content").Array() {
				if strings.ToLower(strings.TrimSpace(part.Get("type").String())) != "input_image" {
					continue
				}
				if url := inputImageURL(part); url != "" {
					found = url
				}
			}
		}
	}
	return found
}

// inputImageURL reads an input_image's URL from either the Responses string
// form ("image_url": "data:...") or the chat-completions object form
// ("image_url": {"url": ...}) that some clients and upstreams accept.
func inputImageURL(item gjson.Result) string {
	if url := firstGjsonString(item, "image_url", "file_url", "url", "file"); url != "" {
		return url
	}
	return firstGjsonString(item.Get("image_url"), "url")
}

// geminiVisionRecognize calls a Gemini native generateContent endpoint with an
// inline image and returns the model's text answer.
func geminiVisionRecognize(ctx context.Context, imageURL string, question string, apiKey string, apiBase string, model string) emulatedRecognitionResult {
	data, mime, err := imageForVision(imageURL)
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
	binary, mime, err := imageBytesForVision(context.Background(), imageURL)
	if err != nil {
		return "", "", err
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
