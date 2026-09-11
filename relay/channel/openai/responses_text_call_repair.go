package openai

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"github.com/gin-gonic/gin"
)

// Textualized tool-call repair. Some models (observed: muse-spark via opencode
// zen/go) occasionally end a turn by writing a tool call as plain assistant
// text — "[tool call] exec_command({...})" — instead of a structured
// function_call item, so the client sees prose it cannot execute and the loop
// dies. When the channel lists a model in repair_text_tool_call_models, the
// gateway detects such messages and rewrites them into a real function_call
// item (non-stream) or a synthesized function_call event sequence (stream).

// textualToolCallPrefix introduces a textualized tool call.
const textualToolCallPrefix = "[tool call]"

// parseTextualToolCall parses a message that consists solely of a textualized
// tool call: optional whitespace, "[tool call]", a tool name, and a JSON
// object in parentheses. Prose around the call, a non-object payload or
// anything after the closing parenthesis reports ok=false.
func parseTextualToolCall(text string) (name string, arguments string, ok bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, textualToolCallPrefix) {
		return "", "", false
	}
	rest := strings.TrimSpace(trimmed[len(textualToolCallPrefix):])
	open := strings.IndexByte(rest, '(')
	if open <= 0 {
		return "", "", false
	}
	name = strings.TrimSpace(rest[:open])
	if !isToolName(name) {
		return "", "", false
	}
	object, tail, ok := scanJSONObject(rest[open+1:])
	if !ok {
		return "", "", false
	}
	if strings.TrimSpace(tail) != ")" {
		return "", "", false
	}
	var envelope map[string]any
	if err := common.Unmarshal([]byte(object), &envelope); err != nil {
		return "", "", false
	}
	return name, object, true
}

// isToolName reports whether name is a plausible tool identifier
// (letters, digits, underscore, dash, dot).
func isToolName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.':
		default:
			return false
		}
	}
	return true
}

// scanJSONObject returns the leading balanced JSON object plus the remainder
// of the input. Strings and escapes are tracked so braces inside string
// values do not confuse the depth counting.
func scanJSONObject(s string) (object string, tail string, ok bool) {
	start := -1
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			depth--
			if depth == 0 && start >= 0 {
				return s[start : i+1], s[i+1:], true
			}
			if depth < 0 {
				return "", "", false
			}
		}
	}
	return "", "", false
}

// repairTextualToolCallsBody rewrites assistant message items that consist
// solely of a textualized call to a declared tool into structured
// function_call items. Bodies without a repairable item return unchanged.
func repairTextualToolCallsBody(body []byte, toolNames map[string]struct{}, c *gin.Context) []byte {
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
		text, repairable := textualRepairCandidate(item)
		if !repairable {
			continue
		}
		name, arguments, parsed := parseTextualToolCall(text)
		if !parsed {
			continue
		}
		if _, declared := toolNames[name]; !declared {
			continue
		}
		rewriteTextualCallItem(item, name, arguments)
		logger.LogWarn(c, "repaired textualized tool call "+name+" into a structured function_call")
		changed = true
	}
	if !changed {
		return body
	}
	repaired, err := common.Marshal(data)
	if err != nil {
		return body
	}
	return repaired
}

// textualRepairCandidate reports the message text when the item is an
// assistant message whose content is exclusively output_text parts.
func textualRepairCandidate(item map[string]any) (string, bool) {
	if itemType, _ := item["type"].(string); itemType != "message" {
		return "", false
	}
	if role, _ := item["role"].(string); role != "" && role != "assistant" {
		return "", false
	}
	parts, _ := item["content"].([]any)
	if len(parts) == 0 {
		return "", false
	}
	var text strings.Builder
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			return "", false
		}
		if partType, _ := part["type"].(string); partType != "output_text" {
			return "", false
		}
		text.WriteString(stringValue(part["text"]))
	}
	return text.String(), true
}

// rewriteTextualCallItem rewrites a message item in place into a completed
// function_call carrying fresh ids the client echoes back on the next turn.
func rewriteTextualCallItem(item map[string]any, name string, arguments string) {
	rewriteTextualCallItemWithIDs(item, "fc_"+common.GetUUID(), "call_"+common.GetUUID(), name, arguments)
}

// rewriteTextualCallItemWithIDs is rewriteTextualCallItem with explicit ids so
// the completed response's output array matches the synthetic stream events.
func rewriteTextualCallItemWithIDs(item map[string]any, itemID string, callID string, name string, arguments string) {
	for key := range item {
		delete(item, key)
	}
	item["type"] = "function_call"
	item["id"] = itemID
	item["call_id"] = callID
	item["name"] = name
	item["arguments"] = arguments
	item["status"] = "completed"
}

// syntheticFunctionCallEvents mirrors what a native upstream emits for one
// streamed function_call item.
func syntheticFunctionCallEvents(itemID string, callID string, name string, arguments string, outputIndex int) [][]byte {
	added, _ := common.Marshal(map[string]any{
		"type":         "response.output_item.added",
		"output_index": outputIndex,
		"item": map[string]any{
			"id":        itemID,
			"call_id":   callID,
			"type":      "function_call",
			"status":    "in_progress",
			"name":      name,
			"arguments": "",
		},
	})
	delta, _ := common.Marshal(map[string]any{
		"type":         "response.function_call_arguments.delta",
		"item_id":      itemID,
		"output_index": outputIndex,
		"delta":        arguments,
	})
	done, _ := common.Marshal(map[string]any{
		"type":         "response.function_call_arguments.done",
		"item_id":      itemID,
		"output_index": outputIndex,
		"arguments":    arguments,
	})
	itemDone, _ := common.Marshal(map[string]any{
		"type":         "response.output_item.done",
		"output_index": outputIndex,
		"item": map[string]any{
			"id":        itemID,
			"call_id":   callID,
			"type":      "function_call",
			"status":    "completed",
			"name":      name,
			"arguments": arguments,
		},
	})
	return [][]byte{added, delta, done, itemDone}
}

const (
	// textRepairUndecided: the leading text has not yet disproved the textual
	// call signature; events are held back.
	textRepairUndecided = iota
	// textRepairCapturing: the "[tool call]" signature matched; hold events
	// until the item completes, then parse the full text.
	textRepairCapturing
	// textRepairPassthrough: ordinary message; forward everything live.
	textRepairPassthrough
	// textRepairRepaired: textual call confirmed and rewritten; the buffered
	// message events are dropped.
	textRepairRepaired
)

// streamTextRepairItem tracks one message output item while the gateway
// decides whether its text is a textualized tool call.
type streamTextRepairItem struct {
	id              string
	outputIndex     int
	state           int
	text            strings.Builder
	events          [][]byte // raw events held back while undecided/capturing
	sawAdded        bool
	repairName      string
	repairArguments string
	repairItemID    string
	repairCallID    string
}

// responsesStreamTextRepair detects textualized tool calls in a Responses SSE
// stream. Message-item events are held back until the leading text either
// disproves the signature (everything then flows on untouched) or the item
// completes as a repairable call (buffered message events are dropped and a
// synthetic function_call sequence replaces them). The returned pre-events
// are meant to re-enter the ordinary forwarding pipeline, so bridging restore
// and backfill bookkeeping apply to them like to native events.
type responsesStreamTextRepair struct {
	toolNames map[string]struct{}
	items     map[string]*streamTextRepairItem
}

func newResponsesStreamTextRepair(toolNames map[string]struct{}) *responsesStreamTextRepair {
	return &responsesStreamTextRepair{
		toolNames: toolNames,
		items:     make(map[string]*streamTextRepairItem),
	}
}

// processEvent handles one SSE payload. It mirrors the streamBridge contract:
// the (possibly rewritten) payload to forward, events to emit first, and
// whether the payload should be forwarded at all.
func (r *responsesStreamTextRepair) processEvent(data []byte, c *gin.Context) ([]byte, [][]byte, bool) {
	var event map[string]any
	if err := common.Unmarshal(data, &event); err != nil {
		return data, nil, true
	}
	switch eventType, _ := event["type"].(string); eventType {
	case "response.output_item.added":
		item, _ := event["item"].(map[string]any)
		if item == nil || stringValue(item["type"]) != "message" {
			return data, nil, true
		}
		entry := r.itemFor(stringValue(item["id"]), intNumber(event["output_index"]))
		// a passthrough entry means this event is a flush re-entry; an entry
		// that already saw its added event must not begin again
		if entry.state != textRepairUndecided || entry.sawAdded {
			return data, nil, true
		}
		if entry.id == "" {
			entry.id = stringValue(item["id"])
		}
		entry.sawAdded = true
		entry.events = append(entry.events, data)
		return data, nil, false
	case "response.content_part.added":
		entry := r.heldItem(stringValue(event["item_id"]), intNumber(event["output_index"]))
		if entry == nil {
			return data, nil, true
		}
		part, _ := event["part"].(map[string]any)
		if part == nil || stringValue(part["type"]) != "output_text" || intNumber(event["content_index"]) != 0 {
			return data, r.release(entry), true
		}
		entry.events = append(entry.events, data)
		return data, nil, false
	case "response.output_text.delta":
		entry := r.itemFor(stringValue(event["item_id"]), intNumber(event["output_index"]))
		switch entry.state {
		case textRepairPassthrough, textRepairRepaired:
			return data, nil, true
		case textRepairCapturing:
			entry.text.WriteString(stringValue(event["delta"]))
			entry.events = append(entry.events, data)
			return data, nil, false
		default: // undecided
			entry.text.WriteString(stringValue(event["delta"]))
			text := strings.TrimSpace(entry.text.String())
			if len(text) < len(textualToolCallPrefix) {
				if strings.HasPrefix(textualToolCallPrefix, text) {
					entry.events = append(entry.events, data)
					return data, nil, false
				}
			} else if strings.HasPrefix(text, textualToolCallPrefix) {
				entry.state = textRepairCapturing
				entry.events = append(entry.events, data)
				return data, nil, false
			}
			return data, r.release(entry), true
		}
	case "response.output_text.done", "response.content_part.done":
		entry := r.heldItem(stringValue(event["item_id"]), intNumber(event["output_index"]))
		if entry == nil {
			return data, nil, true
		}
		entry.events = append(entry.events, data)
		return data, nil, false
	case "response.output_item.done":
		item, _ := event["item"].(map[string]any)
		if item == nil || stringValue(item["type"]) != "message" {
			return data, nil, true
		}
		entry := r.heldItem(stringValue(item["id"]), intNumber(event["output_index"]))
		if entry == nil {
			return data, nil, true
		}
		if text := messageItemText(item); text != "" {
			entry.text.Reset()
			entry.text.WriteString(text)
		}
		pre, repaired := r.resolve(entry, c)
		if repaired {
			// the message done event itself is dropped
			return data, pre, false
		}
		entry.events = append(entry.events, data)
		return data, r.release(entry), true
	case "response.completed", "response.done":
		return r.finishEvent(event, data, true, c)
	case "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		return r.finishEvent(event, data, false, c)
	default:
		// any other event that belongs to a held message item (annotations,
		// logs, ...) is held with it so nothing leaks before the decision
		entry := r.heldItem(stringValue(event["item_id"]), -1)
		if entry == nil {
			return data, nil, true
		}
		entry.events = append(entry.events, data)
		return data, nil, false
	}
}

// finishEvent resolves every held item when the stream ends. On success
// endings the final response's output array is rewritten to match; failed or
// incomplete responses only flush the held events.
func (r *responsesStreamTextRepair) finishEvent(event map[string]any, data []byte, repair bool, c *gin.Context) ([]byte, [][]byte, bool) {
	var pre [][]byte
	repairedAny := false
	if repair {
		response, _ := event["response"].(map[string]any)
		output, _ := response["output"].([]any)
		for _, rawItem := range output {
			item, ok := rawItem.(map[string]any)
			if !ok {
				continue
			}
			// a repair that already fired at the item's done event: rewrite
			// the output entry with the same ids the synthetic events used
			if entry := r.repairedEntry(stringValue(item["id"])); entry != nil {
				rewriteTextualCallItemWithIDs(item, entry.repairItemID, entry.repairCallID, entry.repairName, entry.repairArguments)
				repairedAny = true
				continue
			}
			// still-held items resolve now from the final output array
			entry := r.heldItem(stringValue(item["id"]), -1)
			if entry == nil {
				continue
			}
			if text := messageItemText(item); text != "" {
				entry.text.Reset()
				entry.text.WriteString(text)
			}
			events, repaired := r.resolve(entry, c)
			if repaired {
				rewriteTextualCallItemWithIDs(item, entry.repairItemID, entry.repairCallID, entry.repairName, entry.repairArguments)
				pre = append(pre, events...)
				repairedAny = true
			}
		}
		if repairedAny {
			if rewritten, err := common.Marshal(event); err == nil {
				data = rewritten
			}
		}
	}
	// flush everything still held (undecided items that never matched an
	// output entry, or all items on failed/incomplete endings)
	for _, entry := range r.items {
		switch entry.state {
		case textRepairUndecided, textRepairCapturing:
			pre = append(pre, r.release(entry)...)
		}
	}
	return data, pre, true
}

// repairedEntry returns the repaired item matching an output-array entry so
// the final response's output is rewritten with the same ids the synthetic
// events already carried. An idless message matches only when exactly one
// repair fired.
func (r *responsesStreamTextRepair) repairedEntry(itemID string) *streamTextRepairItem {
	if itemID != "" {
		if entry, ok := r.items[itemID]; ok && entry.state == textRepairRepaired {
			return entry
		}
		return nil
	}
	var found *streamTextRepairItem
	for _, entry := range r.items {
		if entry.state != textRepairRepaired {
			continue
		}
		if found != nil {
			return nil
		}
		found = entry
	}
	return found
}

// resolve decides one held item from its accumulated text. On success the
// item is marked repaired and the synthetic function_call events are
// returned; otherwise the caller must release the item.
func (r *responsesStreamTextRepair) resolve(entry *streamTextRepairItem, c *gin.Context) ([][]byte, bool) {
	name, arguments, parsed := parseTextualToolCall(entry.text.String())
	if !parsed {
		return nil, false
	}
	if _, declared := r.toolNames[name]; !declared {
		return nil, false
	}
	entry.state = textRepairRepaired
	entry.repairName = name
	entry.repairArguments = arguments
	entry.repairItemID = "fc_" + common.GetUUID()
	entry.repairCallID = "call_" + common.GetUUID()
	logger.LogWarn(c, "repaired textualized tool call "+name+" into a structured function_call")
	return syntheticFunctionCallEvents(entry.repairItemID, entry.repairCallID, name, arguments, entry.outputIndex), true
}

// release marks the item passthrough and returns its held events (plus a
// synthesized lifecycle header when the upstream never sent one).
func (r *responsesStreamTextRepair) release(entry *streamTextRepairItem) [][]byte {
	entry.state = textRepairPassthrough
	var events [][]byte
	if !entry.sawAdded {
		id := entry.id
		if id == "" {
			id = "msg_" + common.GetUUID()
			entry.id = id
		}
		added, _ := common.Marshal(map[string]any{
			"type":         "response.output_item.added",
			"output_index": entry.outputIndex,
			"item": map[string]any{
				"id":      id,
				"type":    "message",
				"status":  "in_progress",
				"role":    "assistant",
				"content": []any{},
			},
		})
		partAdded, _ := common.Marshal(map[string]any{
			"type":          "response.content_part.added",
			"item_id":       id,
			"output_index":  entry.outputIndex,
			"content_index": 0,
			"part": map[string]any{
				"type":        "output_text",
				"text":        "",
				"annotations": []any{},
			},
		})
		events = append(events, added, partAdded)
	}
	events = append(events, entry.events...)
	entry.events = nil
	return events
}

// heldItem returns the item for an id (or output index) while it is still
// being held back; passthrough/repaired items and unknown ids yield nil.
func (r *responsesStreamTextRepair) heldItem(itemID string, outputIndex int) *streamTextRepairItem {
	entry := r.lookup(itemID, outputIndex)
	if entry == nil {
		return nil
	}
	switch entry.state {
	case textRepairUndecided, textRepairCapturing:
		return entry
	default:
		return nil
	}
}

// itemFor returns the tracked item, creating an undecided one for a
// previously unseen message delta (upstreams that skip output_item.added).
func (r *responsesStreamTextRepair) itemFor(itemID string, outputIndex int) *streamTextRepairItem {
	if entry := r.lookup(itemID, outputIndex); entry != nil {
		return entry
	}
	return r.beginItem(itemID, outputIndex)
}

func (r *responsesStreamTextRepair) lookup(itemID string, outputIndex int) *streamTextRepairItem {
	if itemID != "" {
		if entry, ok := r.items[itemID]; ok {
			return entry
		}
	}
	// events may reference the same item with or without an id; the output
	// index disambiguates whenever it is present
	if outputIndex >= 0 {
		return r.items[indexKey(outputIndex)]
	}
	return nil
}

func (r *responsesStreamTextRepair) beginItem(itemID string, outputIndex int) *streamTextRepairItem {
	if itemID == "" && outputIndex >= 0 {
		for _, entry := range r.items {
			if entry.outputIndex == outputIndex {
				return entry
			}
		}
	}
	key := itemID
	if key == "" {
		key = indexKey(outputIndex)
	}
	entry := &streamTextRepairItem{
		id:          itemID,
		outputIndex: outputIndex,
		state:       textRepairUndecided,
	}
	r.items[key] = entry
	return entry
}

func indexKey(outputIndex int) string {
	return "#" + strconv.Itoa(outputIndex)
}

// messageItemText joins the output_text parts of a completed message item.
func messageItemText(item map[string]any) string {
	parts, _ := item["content"].([]any)
	var text strings.Builder
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			continue
		}
		if partType, _ := part["type"].(string); partType != "output_text" {
			continue
		}
		text.WriteString(stringValue(part["text"]))
	}
	return text.String()
}

func intNumber(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return -1
	}
}
