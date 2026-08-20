package openai

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// restoreBridgedResponsesOutput rewrites function_call output items that were
// bridged on the request side back into their native Responses kinds
// (custom_tool_call / tool_search_call / namespaced function_call), so the
// client sees exactly the tool kinds it declared. Unknown fields survive the
// rewrite because the body is only re-encoded through a generic map. Bodies
// without bridged items are returned unchanged.
func restoreBridgedResponsesOutput(body []byte, bridge *relaycommon.ResponsesClientToolBridge) []byte {
	var data map[string]any
	if err := common.Unmarshal(body, &data); err != nil {
		return body
	}
	output, ok := data["output"].([]any)
	if !ok {
		return body
	}
	changed := false
	for _, rawItem := range output {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		if restoreBridgedOutputItem(item, bridge) {
			changed = true
		}
	}
	if !changed {
		return body
	}
	restored, err := common.Marshal(data)
	if err != nil {
		return body
	}
	return restored
}

// restoreBridgedOutputItem rewrites one output item in place and reports
// whether anything changed.
func restoreBridgedOutputItem(item map[string]any, bridge *relaycommon.ResponsesClientToolBridge) bool {
	if itemType, _ := item["type"].(string); itemType != "function_call" {
		return false
	}
	name, _ := item["name"].(string)
	spec, ok := bridge.Lookup(name)
	if !ok {
		return false
	}
	switch spec.Kind {
	case relaycommon.ResponsesClientToolCustom:
		item["type"] = "custom_tool_call"
		item["input"] = customInputFromFunctionArguments(item["arguments"])
		delete(item, "arguments")
	case relaycommon.ResponsesClientToolSearch:
		item["type"] = "tool_search_call"
		item["execution"] = "client"
	case relaycommon.ResponsesClientToolWebSearch:
		item["type"] = "web_search_call"
		query := webSearchQueryFromArguments(item["arguments"])
		delete(item, "arguments")
		item["action"] = map[string]any{"type": "search", "query": query}
	case relaycommon.ResponsesClientToolNamespace:
		item["name"] = spec.Name
		item["namespace"] = spec.Namespace
	default:
		return false
	}
	return true
}

// webSearchQueryFromArguments extracts the query from the bridged function's
// {"query": ...} arguments; malformed input falls back to the raw string.
func webSearchQueryFromArguments(arguments any) string {
	switch value := arguments.(type) {
	case string:
		if strings.TrimSpace(value) == "" {
			return ""
		}
		var envelope map[string]any
		if err := common.Unmarshal([]byte(value), &envelope); err == nil {
			if query, ok := envelope["query"].(string); ok {
				return query
			}
		}
		return value
	default:
		data, err := common.Marshal(value)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

// customInputFromFunctionArguments unwraps the {"input": ...} envelope the
// request-side bridge wraps custom tool calls in. Malformed or non-object
// arguments fall back to the raw string so tool input is never dropped.
func customInputFromFunctionArguments(arguments any) string {
	switch value := arguments.(type) {
	case string:
		if strings.TrimSpace(value) == "" {
			return ""
		}
		var envelope map[string]any
		if err := common.Unmarshal([]byte(value), &envelope); err == nil {
			if input, ok := envelope["input"].(string); ok {
				return input
			}
		}
		return value
	default:
		data, err := common.Marshal(value)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

// responsesStreamToolBridge restores bridged tool kinds while streaming. Custom
// tools are special: their input streams via custom_tool_call_input.delta/.done
// events, and the unwrappable envelope only exists once the full arguments are
// known, so argument deltas are buffered and emitted as one input chunk right
// before the item's done event.
type responsesStreamToolBridge struct {
	bridge     *relaycommon.ResponsesClientToolBridge
	specs      map[string]relaycommon.ResponsesClientToolSpec // item_id → spec
	byIndex    map[int]relaycommon.ResponsesClientToolSpec    // output_index → spec
	argsBuffer map[string]*strings.Builder
}

func newResponsesStreamToolBridge(bridge *relaycommon.ResponsesClientToolBridge) *responsesStreamToolBridge {
	return &responsesStreamToolBridge{
		bridge:     bridge,
		specs:      make(map[string]relaycommon.ResponsesClientToolSpec),
		byIndex:    make(map[int]relaycommon.ResponsesClientToolSpec),
		argsBuffer: make(map[string]*strings.Builder),
	}
}

// transformEvent rewrites one SSE payload. It returns the (possibly unchanged)
// payload to forward, optional events to emit first, and whether the event
// should be forwarded at all.
func (s *responsesStreamToolBridge) transformEvent(data []byte) ([]byte, [][]byte, bool) {
	var event map[string]any
	if err := common.Unmarshal(data, &event); err != nil {
		return data, nil, true
	}
	switch eventType, _ := event["type"].(string); eventType {
	case "response.output_item.added":
		item, _ := event["item"].(map[string]any)
		if item == nil {
			return data, nil, true
		}
		s.rememberFromEvent(event)
		if s.rewriteStreamItem(item) {
			out, err := common.Marshal(event)
			if err != nil {
				return data, nil, true
			}
			return out, nil, true
		}
		return data, nil, true
	case "response.function_call_arguments.delta":
		if s.isSilentArgumentsEvent(event) {
			itemID, _ := event["item_id"].(string)
			if itemID != "" {
				buffer, _ := s.argsBuffer[itemID]
				if buffer == nil {
					buffer = &strings.Builder{}
					s.argsBuffer[itemID] = buffer
				}
				buffer.WriteString(stringValue(event["delta"]))
			}
			return data, nil, false
		}
		return data, nil, true
	case "response.function_call_arguments.done":
		if s.isSilentArgumentsEvent(event) {
			return data, nil, false
		}
		return data, nil, true
	case "response.output_item.done":
		item, _ := event["item"].(map[string]any)
		if item == nil {
			return data, nil, true
		}
		spec, bridged := s.lookupItemSpec(item)
		if !bridged {
			return data, nil, true
		}
		var pre [][]byte
		if spec.Kind == relaycommon.ResponsesClientToolCustom {
			input := customInputFromFunctionArguments(item["arguments"])
			if input == "" {
				if buffer := s.argsBuffer[stringValue(item["id"])]; buffer != nil {
					input = customInputFromFunctionArguments(buffer.String())
				}
			}
			itemID := stringValue(item["id"])
			outputIndex := event["output_index"]
			delta, _ := common.Marshal(map[string]any{
				"type":         "response.custom_tool_call_input.delta",
				"item_id":      itemID,
				"output_index": outputIndex,
				"delta":        input,
			})
			done, _ := common.Marshal(map[string]any{
				"type":         "response.custom_tool_call_input.done",
				"item_id":      itemID,
				"output_index": outputIndex,
				"input":        input,
			})
			pre = append(pre, delta, done)
		}
		s.rewriteStreamItem(item)
		out, err := common.Marshal(event)
		if err != nil {
			return data, pre, true
		}
		return out, pre, true
	case "response.completed", "response.done", "response.failed", "response.incomplete":
		response, _ := event["response"].(map[string]any)
		if response == nil {
			return data, nil, true
		}
		output, _ := response["output"].([]any)
		changed := false
		for _, rawItem := range output {
			if item, ok := rawItem.(map[string]any); ok && restoreBridgedOutputItem(item, s.bridge) {
				changed = true
			}
		}
		if !changed {
			return data, nil, true
		}
		out, err := common.Marshal(event)
		if err != nil {
			return data, nil, true
		}
		return out, nil, true
	default:
		return data, nil, true
	}
}

// rewriteStreamItem rewrites an output_item payload in place for bridged names.
func (s *responsesStreamToolBridge) rewriteStreamItem(item map[string]any) bool {
	name := stringValue(item["name"])
	if _, ok := s.bridge.Lookup(name); !ok {
		return false
	}
	return restoreBridgedOutputItem(item, s.bridge)
}

func (s *responsesStreamToolBridge) rememberFromEvent(event map[string]any) {
	item, _ := event["item"].(map[string]any)
	if item == nil {
		return
	}
	name := stringValue(item["name"])
	spec, ok := s.bridge.Lookup(name)
	if !ok {
		return
	}
	// The item id is what argument delta/done events reference; record it
	// before any rewrite could touch it.
	if id := stringValue(item["id"]); id != "" {
		s.specs[id] = spec
	}
	if index, ok := event["output_index"].(float64); ok {
		s.byIndex[int(index)] = spec
	}
}

// lookupItemSpec resolves the spec for a done item, tolerating upstreams that
// already renamed fields or omitted ids.
func (s *responsesStreamToolBridge) lookupItemSpec(item map[string]any) (relaycommon.ResponsesClientToolSpec, bool) {
	if spec, ok := s.specs[stringValue(item["id"])]; ok {
		return spec, true
	}
	if spec, ok := s.bridge.Lookup(stringValue(item["name"])); ok {
		return spec, true
	}
	return relaycommon.ResponsesClientToolSpec{}, false
}

// isSilentArgumentsEvent reports whether an arguments delta/done event belongs
// to a bridged item whose native kind does not stream arguments (custom tools
// carry input via custom_tool_call_input events; web_search_call carries the
// query inside the item), by item_id first and output_index as fallback.
func (s *responsesStreamToolBridge) isSilentArgumentsEvent(event map[string]any) bool {
	spec, ok := s.specs[stringValue(event["item_id"])]
	if !ok {
		if index, ok := event["output_index"]; ok {
			switch v := index.(type) {
			case float64:
				spec, ok = s.byIndex[int(v)]
			case int:
				spec, ok = s.byIndex[v]
			}
		}
	}
	return ok && (spec.Kind == relaycommon.ResponsesClientToolCustom || spec.Kind == relaycommon.ResponsesClientToolWebSearch)
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
