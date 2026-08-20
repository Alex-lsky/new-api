package relay

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// bridgeResponsesClientTools rewrites client-executed Responses tool kinds
// (custom / namespace / tool_search, per the channel's bridge_tool_types) into
// ordinary function tools the upstream accepts, and rewrites the matching
// input-history items accordingly. The registry records each rewritten name so
// the response side can restore native tool call kinds. Bodies with nothing to
// bridge are returned byte-identical.
func bridgeResponsesClientTools(body []byte, kinds map[string]struct{}, bridge *relaycommon.ResponsesClientToolBridge) []byte {
	if len(kinds) == 0 || len(body) == 0 {
		return body
	}

	newTools, toolsChanged := bridgeResponsesToolDeclarations(gjson.GetBytes(body, "tools"), kinds, bridge)
	input := gjson.GetBytes(body, "input")
	if !toolsChanged && !input.IsArray() {
		return body
	}
	rewrittenInput, extractedTools, inputChanged := bridgeResponsesInputHistory(input, kinds, bridge)
	if !toolsChanged && !inputChanged {
		return body
	}
	newTools = append(newTools, extractedTools...)

	var result []byte
	if toolsChanged || len(extractedTools) > 0 {
		var b strings.Builder
		b.WriteByte('[')
		for i, raw := range newTools {
			if i > 0 {
				b.WriteByte(',')
			}
			b.Write(raw)
		}
		b.WriteByte(']')
		var err error
		result, err = sjson.SetRawBytes(body, "tools", []byte(b.String()))
		if err != nil {
			return body
		}
	} else {
		result = body
	}
	for index, raw := range rewrittenInput {
		var err error
		result, err = sjson.SetRawBytes(result, fmt.Sprintf("input.%d", index), raw)
		if err != nil {
			return body
		}
	}
	return result
}

func bridgeResponsesToolDeclarations(tools gjson.Result, kinds map[string]struct{}, bridge *relaycommon.ResponsesClientToolBridge) ([][]byte, bool) {
	if !tools.IsArray() {
		return nil, false
	}
	newTools := make([][]byte, 0, len(tools.Array()))
	changed := false
	for _, tool := range tools.Array() {
		toolType := strings.ToLower(strings.TrimSpace(tool.Get("type").String()))
		switch {
		case toolType == "custom" && hasToolKind(kinds, "custom"):
			if replacement := bridgedCustomTool(tool, bridge); replacement != nil {
				newTools = append(newTools, replacement)
				changed = true
				continue
			}
		case toolType == "tool_search" && hasToolKind(kinds, "tool_search"):
			replacement := marshaledTool(map[string]any{
				"type":        "function",
				"name":        "tool_search",
				"description": "Search and load Codex tools, plugins, connectors, and MCP namespaces for the current task.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string", "description": "Search query for tools or connectors to load."},
						"limit": map[string]any{"type": "integer", "description": "Maximum number of tool groups to return."},
					},
					"required": []string{"query"},
				},
			})
			if replacement != nil && bridge.Register("tool_search", relaycommon.ResponsesClientToolSpec{Kind: relaycommon.ResponsesClientToolSearch, Name: "tool_search"}) {
				newTools = append(newTools, replacement)
				changed = true
				continue
			}
		case toolType == "namespace" && hasToolKind(kinds, "namespace"):
			children, expanded := bridgeNamespacedToolChildren(tool, bridge)
			if expanded {
				newTools = append(newTools, children...)
				changed = true
				continue
			}
		}
		// Plain function tools only occupy their name so a same-named bridged
		// or emulated tool cannot shadow them; the response side never
		// rewrites this kind. Other pass-through kinds (hosted tools, unknown
		// types) must not squat on their names.
		if toolType == "function" {
			if name := responsesBridgedToolName(tool); name != "" {
				bridge.Register(name, relaycommon.ResponsesClientToolSpec{Kind: relaycommon.ResponsesClientToolFunction, Name: name})
			}
		}
		newTools = append(newTools, []byte(tool.Raw))
	}
	return newTools, changed
}

func hasToolKind(kinds map[string]struct{}, kind string) bool {
	_, ok := kinds[kind]
	return ok
}

func bridgedCustomTool(tool gjson.Result, bridge *relaycommon.ResponsesClientToolBridge) []byte {
	name := strings.TrimSpace(tool.Get("name").String())
	if name == "" {
		return nil
	}
	if !bridge.Register(name, relaycommon.ResponsesClientToolSpec{Kind: relaycommon.ResponsesClientToolCustom, Name: name}) {
		return nil
	}
	description := strings.TrimSpace(tool.Get("description").String())
	replacement := map[string]any{
		"type":        "function",
		"name":        name,
		"description": "Custom tool bridged as a function. Call it with a single JSON object {\"input\": \"<raw input string>\"} honoring the original format below.\n```json\n" + tool.Raw + "\n```",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{
					"type":        "string",
					"description": "Raw input for the custom tool; follow the tool's documented format.",
				},
			},
			"required": []string{"input"},
		},
	}
	if description != "" {
		replacement["description"] = description + "\n\n" + replacement["description"].(string)
	}
	return marshaledTool(replacement)
}

// bridgeNamespacedToolChildren flattens a namespace container's function
// children into standalone function tools with chat-safe flattened names.
func bridgeNamespacedToolChildren(tool gjson.Result, bridge *relaycommon.ResponsesClientToolBridge) ([][]byte, bool) {
	namespace := strings.TrimSpace(tool.Get("name").String())
	if namespace == "" {
		return nil, false
	}
	children := tool.Get("tools")
	if !children.IsArray() {
		children = tool.Get("children")
	}
	if !children.IsArray() {
		return nil, false
	}
	out := make([][]byte, 0, len(children.Array()))
	for _, child := range children.Array() {
		if strings.ToLower(strings.TrimSpace(child.Get("type").String())) != "function" {
			continue
		}
		childName := responsesBridgedToolName(child)
		if childName == "" {
			continue
		}
		chatName := flattenResponsesNamespacedToolName(namespace, childName)
		if !bridge.Register(chatName, relaycommon.ResponsesClientToolSpec{Kind: relaycommon.ResponsesClientToolNamespace, Name: childName, Namespace: namespace}) {
			continue
		}
		replacement := map[string]any{
			"type":        "function",
			"name":        chatName,
			"description": child.Get("description").String(),
		}
		if params := child.Get("parameters"); params.Exists() {
			replacement["parameters"] = json.RawMessage(params.Raw)
		} else {
			replacement["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		if raw := marshaledTool(replacement); raw != nil {
			out = append(out, raw)
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// bridgeResponsesInputHistory rewrites client tool kinds in the input history
// into function_call / function_call_output items, and extracts tool
// declarations embedded in tool_search_output items so they remain callable.
func bridgeResponsesInputHistory(input gjson.Result, kinds map[string]struct{}, bridge *relaycommon.ResponsesClientToolBridge) (map[int][]byte, [][]byte, bool) {
	if !input.IsArray() {
		return nil, nil, false
	}
	rewritten := make(map[int][]byte)
	var extracted [][]byte
	items := input.Array()
	for i, item := range items {
		itemType := strings.ToLower(strings.TrimSpace(item.Get("type").String()))
		switch {
		case itemType == "custom_tool_call" && hasToolKind(kinds, "custom"):
			inputRaw := item.Get("input").Raw
			if inputRaw == "" {
				inputRaw = "null"
			}
			arguments, _ := common.Marshal(map[string]any{"input": json.RawMessage(inputRaw)})
			rewritten[i] = marshaledTool(map[string]any{
				"type":      "function_call",
				"call_id":   item.Get("call_id").String(),
				"name":      item.Get("name").String(),
				"arguments": string(arguments),
			})
		case itemType == "custom_tool_call_output" && hasToolKind(kinds, "custom"):
			rewritten[i] = marshaledTool(map[string]any{
				"type":    "function_call_output",
				"call_id": item.Get("call_id").String(),
				"output":  item.Raw,
			})
		case itemType == "tool_search_call" && hasToolKind(kinds, "tool_search"):
			arguments := "{}"
			if raw := item.Get("arguments").Raw; raw != "" {
				arguments = raw
			}
			rewritten[i] = marshaledTool(map[string]any{
				"type":      "function_call",
				"call_id":   item.Get("call_id").String(),
				"name":      "tool_search",
				"arguments": json.RawMessage(arguments),
			})
		case itemType == "tool_search_output" && hasToolKind(kinds, "tool_search"):
			extracted = append(extracted, toolsFromToolSearchOutput(item, bridge)...)
			rewritten[i] = marshaledTool(map[string]any{
				"type":    "function_call_output",
				"call_id": item.Get("call_id").String(),
				"output":  item.Raw,
			})
		case itemType == "function_call" && item.Get("namespace").Exists() && hasToolKind(kinds, "namespace"):
			namespace := strings.TrimSpace(item.Get("namespace").String())
			name := strings.TrimSpace(item.Get("name").String())
			replacement := map[string]any{
				"type":      "function_call",
				"call_id":   item.Get("call_id").String(),
				"name":      flattenResponsesNamespacedToolName(namespace, name),
				"arguments": json.RawMessage(argumentsRaw(item)),
			}
			rewritten[i] = marshaledTool(replacement)
		}
	}
	return rewritten, extracted, len(rewritten) > 0
}

// toolsFromToolSearchOutput surfaces tools loaded in a previous turn as
// function declarations the upstream can call.
func toolsFromToolSearchOutput(item gjson.Result, bridge *relaycommon.ResponsesClientToolBridge) [][]byte {
	tools := item.Get("tools")
	if !tools.IsArray() {
		return nil
	}
	var out [][]byte
	for _, tool := range tools.Array() {
		if strings.ToLower(strings.TrimSpace(tool.Get("type").String())) != "function" {
			continue
		}
		name := responsesBridgedToolName(tool)
		if name == "" {
			continue
		}
		replacement := map[string]any{
			"type":        "function",
			"name":        name,
			"description": tool.Get("description").String(),
		}
		if params := tool.Get("parameters"); params.Exists() {
			replacement["parameters"] = json.RawMessage(params.Raw)
		} else {
			replacement["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		if namespace := strings.TrimSpace(tool.Get("namespace").String()); namespace != "" {
			replacement["name"] = flattenResponsesNamespacedToolName(namespace, name)
			bridge.Register(replacement["name"].(string), relaycommon.ResponsesClientToolSpec{Kind: relaycommon.ResponsesClientToolNamespace, Name: name, Namespace: namespace})
		}
		if raw := marshaledTool(replacement); raw != nil {
			out = append(out, raw)
		}
	}
	return out
}

func responsesBridgedToolName(tool gjson.Result) string {
	if fn := tool.Get("function"); fn.IsObject() {
		if name := strings.TrimSpace(fn.Get("name").String()); name != "" {
			return name
		}
	}
	return strings.TrimSpace(tool.Get("name").String())
}

func argumentsRaw(item gjson.Result) string {
	if raw := item.Get("arguments").Raw; raw != "" {
		return raw
	}
	return "{}"
}

func marshaledTool(tool map[string]any) []byte {
	data, err := common.Marshal(tool)
	if err != nil {
		return nil
	}
	return data
}

// flattenResponsesNamespacedToolName produces an upstream-safe function name
// for a namespaced tool. Long names are truncated at a UTF-8 boundary with a
// short stable hash suffix so collisions stay rare.
func flattenResponsesNamespacedToolName(namespace string, name string) string {
	const maxLen = 64
	fullName := namespace + "__" + name
	if len(fullName) <= maxLen {
		return fullName
	}
	digest := sha256.Sum256([]byte(fullName))
	suffix := fmt.Sprintf("__%x", digest[:6])
	prefixLen := maxLen - len(suffix)
	for prefixLen > 0 && prefixLen < len(fullName) && (fullName[prefixLen]&0xc0) == 0x80 {
		prefixLen--
	}
	return fullName[:prefixLen] + suffix
}
