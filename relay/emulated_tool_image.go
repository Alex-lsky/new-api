package relay

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

const (
	emulatedImageDefaultOpenAIModel = "gpt-image-1"
	emulatedImageDefaultGeminiModel = "gemini-2.5-flash-image"
	emulatedImageTimeout            = 120 * time.Second
	emulatedImagePromptMaxRunes     = 4000
	emulatedImageDownloadLimit      = 20 << 20
)

var emulatedImageSizePattern = regexp.MustCompile(`^\d{3,4}x\d{3,4}$`)

// emulatedImageArgs is the function-call shape the gateway exposes to the
// model for the hosted image_generation tool.
type emulatedImageArgs struct {
	Prompt  string
	Size    string
	Quality string
}

func parseEmulatedImageArgs(arguments string) emulatedImageArgs {
	var raw struct {
		Prompt  string `json:"prompt"`
		Size    string `json:"size"`
		Quality string `json:"quality"`
	}
	if err := common.Unmarshal([]byte(arguments), &raw); err != nil {
		return emulatedImageArgs{Prompt: arguments}
	}
	args := emulatedImageArgs{
		Prompt:  strings.TrimSpace(raw.Prompt),
		Size:    strings.ToLower(strings.TrimSpace(raw.Size)),
		Quality: strings.ToLower(strings.TrimSpace(raw.Quality)),
	}
	if len([]rune(args.Prompt)) > emulatedImagePromptMaxRunes {
		args.Prompt = string([]rune(args.Prompt)[:emulatedImagePromptMaxRunes])
	}
	if args.Size != "auto" && !emulatedImageSizePattern.MatchString(args.Size) {
		args.Size = ""
	}
	switch args.Quality {
	case "auto", "high", "medium", "low", "standard", "hd":
	default:
		args.Quality = ""
	}
	return args
}

// emulatedImageResult carries the image execution outcome: Output is the text
// fed back to the model (never the pixels — the model only needs to know the
// image reached the user), ImageB64 is the base64 payload restored to the
// client as a native image_generation_call result.
type emulatedImageResult struct {
	Output   string
	ImageB64 string
}

// executeEmulatedImageGeneration runs the channel's configured image backend.
// Execution errors are reported to the model as text so the conversation can
// continue instead of failing the whole request.
func executeEmulatedImageGeneration(ctx context.Context, arguments string, backend *dto.EmulatedToolBackend) emulatedImageResult {
	if backend == nil {
		return emulatedImageResult{Output: "image generation is not configured on this channel"}
	}
	args := parseEmulatedImageArgs(arguments)
	if args.Prompt == "" {
		return emulatedImageResult{Output: "image generation failed: prompt is required"}
	}
	var (
		imageB64 string
		info     string
		err      error
	)
	switch strings.ToLower(strings.TrimSpace(backend.Provider)) {
	case dto.EmulatedImageProviderOpenAI:
		imageB64, info, err = openAIImagesGenerate(ctx, args, backend)
	case dto.EmulatedImageProviderGemini:
		imageB64, info, err = geminiImagesGenerate(ctx, args, backend)
	default:
		return emulatedImageResult{Output: fmt.Sprintf("unsupported image generation provider %q", backend.Provider)}
	}
	if err != nil {
		return emulatedImageResult{Output: fmt.Sprintf("image generation failed: %v", err)}
	}
	if imageB64 == "" {
		return emulatedImageResult{Output: "image generation failed: backend returned no image"}
	}
	return emulatedImageResult{
		Output:   fmt.Sprintf("Image generated%s. The image was already delivered to the user as the result of this image_generation call; do not embed or repeat the image data in your reply.", info),
		ImageB64: imageB64,
	}
}

func imageHTTPRequest(ctx context.Context, method string, endpoint string, apiKey string, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	requestCtx, cancel := context.WithTimeout(ctx, emulatedImageTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
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

// openAIImagesGenerate targets any OpenAI-compatible /images/generations
// endpoint (OpenAI gpt-image / dall-e, or compatible providers). URL results
// are downloaded and converted to base64 so the restored
// image_generation_call always carries data.
func openAIImagesGenerate(ctx context.Context, args emulatedImageArgs, backend *dto.EmulatedToolBackend) (string, string, error) {
	model := strings.TrimSpace(backend.Model)
	if model == "" {
		model = emulatedImageDefaultOpenAIModel
	}
	apiBase := searchBackendBase(backend, "https://api.openai.com/v1")
	payload := map[string]any{
		"model":  model,
		"prompt": args.Prompt,
		"n":      1,
	}
	if args.Size != "" && args.Size != "auto" {
		payload["size"] = args.Size
	}
	if args.Quality != "" && args.Quality != "auto" {
		payload["quality"] = args.Quality
	}
	body, err := common.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	data, err := imageHTTPRequest(ctx, http.MethodPost, apiBase+"/images/generations", backend.APIKey, body)
	if err != nil {
		return "", "", err
	}
	var response struct {
		Data []struct {
			B64JSON       string `json:"b64_json"`
			URL           string `json:"url"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := common.Unmarshal(data, &response); err != nil {
		return "", "", err
	}
	if len(response.Data) == 0 {
		return "", "", fmt.Errorf("backend returned no image data")
	}
	item := response.Data[0]
	imageB64 := item.B64JSON
	if imageB64 == "" && item.URL != "" {
		imageB64, err = downloadImageAsBase64(ctx, item.URL)
		if err != nil {
			return "", "", err
		}
	}
	info := fmt.Sprintf(" (%s", model)
	if item.RevisedPrompt != "" {
		info += ", revised prompt: " + item.RevisedPrompt
	}
	info += ")"
	return imageB64, info, nil
}

func downloadImageAsBase64(ctx context.Context, imageURL string) (string, error) {
	requestCtx, cancel := context.WithTimeout(ctx, emulatedImageTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, imageURL, nil)
	if err != nil {
		return "", err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("image download returned %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, emulatedImageDownloadLimit))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// geminiImagesGenerate uses Gemini's native image output
// (generateContent with IMAGE response modality, e.g. gemini-2.5-flash-image).
func geminiImagesGenerate(ctx context.Context, args emulatedImageArgs, backend *dto.EmulatedToolBackend) (string, string, error) {
	model := strings.TrimSpace(backend.Model)
	if model == "" {
		model = emulatedImageDefaultGeminiModel
	}
	apiBase := searchBackendBase(backend, "https://generativelanguage.googleapis.com/v1beta")
	generationConfig := map[string]any{
		"responseModalities": []string{"TEXT", "IMAGE"},
	}
	if aspect := geminiImageAspect(args.Size); aspect != "" {
		generationConfig["imageConfig"] = map[string]any{"aspectRatio": aspect}
	}
	body, err := common.Marshal(map[string]any{
		"contents":         []any{map[string]any{"parts": []any{map[string]any{"text": args.Prompt}}}},
		"generationConfig": generationConfig,
	})
	if err != nil {
		return "", "", err
	}
	endpoint := fmt.Sprintf("%s/models/%s:generateContent", apiBase, model)
	if backend.APIKey != "" {
		endpoint += "?key=" + strings.TrimSpace(backend.APIKey)
	}
	data, err := imageHTTPRequest(ctx, http.MethodPost, endpoint, "", body)
	if err != nil {
		return "", "", err
	}
	var response struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text       string `json:"text"`
					InlineData struct {
						MimeType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := common.Unmarshal(data, &response); err != nil {
		return "", "", err
	}
	if len(response.Candidates) == 0 {
		return "", "", fmt.Errorf("backend returned no candidates")
	}
	var imageB64, note string
	for _, part := range response.Candidates[0].Content.Parts {
		if part.InlineData.Data != "" && imageB64 == "" {
			imageB64 = part.InlineData.Data
			if part.InlineData.MimeType != "" {
				note = fmt.Sprintf(" (%s, %s)", model, part.InlineData.MimeType)
			}
		}
		if part.Text != "" && note == "" {
			note = fmt.Sprintf(" (%s)", model)
		}
	}
	if note == "" {
		note = fmt.Sprintf(" (%s)", model)
	}
	return imageB64, note, nil
}

// decodeBase64Image decodes a data-URL or bare base64 payload.
func decodeBase64Image(data string) ([]byte, error) {
	if idx := strings.Index(data, ","); strings.HasPrefix(data, "data:") && idx > 0 {
		data = data[idx+1:]
	}
	return base64.StdEncoding.DecodeString(data)
}

// geminiImageAspect maps an OpenAI-style size to the closest Gemini
// aspect ratio; empty keeps the model default.
func geminiImageAspect(size string) string {
	dimensions := strings.SplitN(size, "x", 2)
	if len(dimensions) != 2 {
		return ""
	}
	width, errW := strconv.Atoi(dimensions[0])
	height, errH := strconv.Atoi(dimensions[1])
	if errW != nil || errH != nil || width <= 0 || height <= 0 {
		return ""
	}
	switch {
	case width == height:
		return "1:1"
	case width > height:
		return "16:9"
	default:
		return "9:16"
	}
}
