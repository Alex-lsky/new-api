package common

import (
	"strconv"
	"strings"
)

// ResponsesClientToolKind identifies which native Responses tool kind a
// bridged function tool stands in for.
type ResponsesClientToolKind string

const (
	// ResponsesClientToolFunction marks a plain function tool that only
	// occupies its name so a same-named bridged tool cannot shadow it. Items
	// registered with this kind are never rewritten on the response side.
	ResponsesClientToolFunction ResponsesClientToolKind = "function"
	// ResponsesClientToolCustom is a custom tool (free-form text input, e.g.
	// Codex apply_patch) bridged as a function with an "input" string envelope.
	ResponsesClientToolCustom ResponsesClientToolKind = "custom"
	// ResponsesClientToolNamespace is a client-executed namespaced tool (e.g.
	// an MCP tool) bridged as a function with a flattened name.
	ResponsesClientToolNamespace ResponsesClientToolKind = "namespace"
	// ResponsesClientToolRenamed marks a plain function or custom tool whose
	// name exceeded the upstream 64-character limit: the outbound request
	// carries a deterministic truncated name and the response side restores
	// the original so the client's tool registry keeps matching.
	ResponsesClientToolRenamed ResponsesClientToolKind = "renamed"
	// ResponsesClientToolSearch is Codex's tool_search declaration bridged as
	// a plain function.
	ResponsesClientToolSearch ResponsesClientToolKind = "tool_search"
	// ResponsesClientToolWebSearch marks a hosted web_search declaration that
	// the gateway itself executes (channel emulate_tool_types): the model
	// calls the bridged function, the gateway runs the configured search
	// backend and feeds results back, and the final response restores native
	// web_search_call items for the client.
	ResponsesClientToolWebSearch ResponsesClientToolKind = "web_search"
	// ResponsesClientToolImageGeneration marks a hosted image_generation
	// declaration the gateway executes itself: the model calls the bridged
	// function, the gateway runs the configured image backend, and the final
	// response restores native image_generation_call items carrying the image.
	ResponsesClientToolImageGeneration ResponsesClientToolKind = "image_generation"
	// ResponsesClientToolImageRecognition marks a gateway-injected
	// image_recognition function tool: the model calls it to analyze an image,
	// the gateway sends the image to the configured vision backend, and the
	// final response restores a synthetic image_recognition_call item.
	ResponsesClientToolImageRecognition ResponsesClientToolKind = "image_recognition"
)

// ResponsesClientToolSpec records how a function tool seen by the upstream maps
// back to the native Responses tool kind the client declared.
type ResponsesClientToolSpec struct {
	Kind      ResponsesClientToolKind
	Name      string
	Namespace string
}

// ResponsesAttachment pairs a per-request image reference ("attachment://3")
// with the original image URL or data URL it stands in for.
type ResponsesAttachment struct {
	Ref string
	URL string
}

// AttachmentURLPrefix marks a per-request stripped-image reference passed by
// the model inside an image_recognition tool call.
const AttachmentURLPrefix = "attachment://"

// ResponsesClientToolBridge is a request-scoped registry populated while the
// outbound Responses request is rewritten (client tool kinds → function tools)
// and consulted while the upstream response is restored (function calls →
// native tool call items). A request that bridged nothing leaves it nil.
type ResponsesClientToolBridge struct {
	byChatName map[string]ResponsesClientToolSpec
	// AttachedImages records, in input order, every user input_image stripped
	// for a recognition-emulating channel, so the executor can resolve the
	// attachment:// references the strip markers hand the model.
	AttachedImages []ResponsesAttachment
}

// AddAttachment records a stripped image and returns its reference.
func (b *ResponsesClientToolBridge) AddAttachment(url string) string {
	if b == nil {
		return ""
	}
	b.AttachedImages = append(b.AttachedImages, ResponsesAttachment{Ref: strconv.Itoa(len(b.AttachedImages) + 1), URL: url})
	return AttachmentURLPrefix + b.AttachedImages[len(b.AttachedImages)-1].Ref
}

// ResolveAttachmentURL maps an attachment:// reference back to the original
// image URL; anything else (or an unknown reference) yields "".
func (b *ResponsesClientToolBridge) ResolveAttachmentURL(imageURL string) string {
	if b == nil || !strings.HasPrefix(imageURL, AttachmentURLPrefix) {
		return ""
	}
	ref := strings.TrimPrefix(imageURL, AttachmentURLPrefix)
	for _, attachment := range b.AttachedImages {
		if attachment.Ref == ref {
			return attachment.URL
		}
	}
	return ""
}

// LastAttachedImageURL returns the most recent stripped image, used when the
// model calls image_recognition without an image_url of its own.
func (b *ResponsesClientToolBridge) LastAttachedImageURL() string {
	if b == nil || len(b.AttachedImages) == 0 {
		return ""
	}
	return b.AttachedImages[len(b.AttachedImages)-1].URL
}

func NewResponsesClientToolBridge() *ResponsesClientToolBridge {
	return &ResponsesClientToolBridge{byChatName: make(map[string]ResponsesClientToolSpec)}
}

// Register records the upstream-facing function name for a native tool spec.
// It reports false when the name is already taken, so a genuine function tool
// declared by the client always wins over a bridged name.
func (b *ResponsesClientToolBridge) Register(chatName string, spec ResponsesClientToolSpec) bool {
	if b == nil {
		return false
	}
	chatName = strings.TrimSpace(chatName)
	if chatName == "" {
		return false
	}
	if _, exists := b.byChatName[chatName]; exists {
		return false
	}
	b.byChatName[chatName] = spec
	return true
}

func (b *ResponsesClientToolBridge) Lookup(chatName string) (ResponsesClientToolSpec, bool) {
	if b == nil {
		return ResponsesClientToolSpec{}, false
	}
	spec, ok := b.byChatName[strings.TrimSpace(chatName)]
	return spec, ok
}

func (b *ResponsesClientToolBridge) Len() int {
	if b == nil {
		return 0
	}
	return len(b.byChatName)
}

// Names returns every upstream-facing function name the registry recorded.
func (b *ResponsesClientToolBridge) Names() []string {
	if b == nil {
		return nil
	}
	names := make([]string, 0, len(b.byChatName))
	for name := range b.byChatName {
		names = append(names, name)
	}
	return names
}
