package relay

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// strippedToolIncludePrefixes maps a stripped tool type to the include-array
// entry prefixes that belong to that tool's outputs. Types not listed fall
// back to "<type>_call." per the Responses naming convention.
var strippedToolIncludePrefixes = map[string][]string{
	"web_search":           {"web_search_call."},
	"web_search_preview":   {"web_search_call."},
	"computer_use":         {"computer_call_output."},
	"computer_use_preview": {"computer_call_output."},
}

// stripResponsesToolTypes removes tools whose "type" is blacklisted from an
// OpenAI Responses request body, together with the fields that would dangle
// after the removal: tool_choice pointing at a stripped tool type, and
// include entries that ask for a stripped tool's outputs. A body without
// blacklisted tools is returned byte-identical.
func stripResponsesToolTypes(body []byte, blacklist map[string]struct{}) []byte {
	if len(blacklist) == 0 || len(body) == 0 {
		return body
	}
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return body
	}

	kept := make([]gjson.Result, 0, len(tools.Array()))
	strippedTypes := make(map[string]struct{})
	for _, tool := range tools.Array() {
		toolType := normalizeResponsesToolType(tool.Get("type"))
		if _, stripped := blacklist[toolType]; stripped {
			strippedTypes[toolType] = struct{}{}
			continue
		}
		kept = append(kept, tool)
	}
	if len(strippedTypes) == 0 {
		return body
	}

	var result []byte
	if len(kept) == 0 {
		// An empty tools array is itself rejected by some upstreams.
		var err error
		result, err = sjson.DeleteBytes(body, "tools")
		if err != nil {
			return body
		}
	} else {
		var b strings.Builder
		b.WriteByte('[')
		for i, tool := range kept {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(tool.Raw)
		}
		b.WriteByte(']')
		var err error
		result, err = sjson.SetRawBytes(body, "tools", []byte(b.String()))
		if err != nil {
			return body
		}
	}

	result = stripResponsesToolChoice(result, blacklist)
	return stripResponsesInclude(result, strippedTypes)
}

func normalizeResponsesToolType(value gjson.Result) string {
	return strings.ToLower(strings.TrimSpace(value.String()))
}

// stripResponsesToolChoice drops tool_choice when it pins a stripped tool
// type. String modes ("auto", "none", "required") and function choices stay.
func stripResponsesToolChoice(body []byte, blacklist map[string]struct{}) []byte {
	toolChoice := gjson.GetBytes(body, "tool_choice")
	if !toolChoice.IsObject() {
		return body
	}
	if _, stripped := blacklist[normalizeResponsesToolType(toolChoice.Get("type"))]; !stripped {
		return body
	}
	result, err := sjson.DeleteBytes(body, "tool_choice")
	if err != nil {
		return body
	}
	return result
}

// stripResponsesInclude removes include entries that reference the outputs of
// a stripped tool (e.g. "web_search_call.results" once web_search is gone).
func stripResponsesInclude(body []byte, strippedTypes map[string]struct{}) []byte {
	include := gjson.GetBytes(body, "include")
	if !include.IsArray() {
		return body
	}
	entries := include.Array()
	kept := make([]string, 0, len(entries))
	for _, entry := range entries {
		if responsesIncludeBelongsToStrippedTool(entry.String(), strippedTypes) {
			continue
		}
		kept = append(kept, entry.String())
	}
	if len(kept) == len(entries) {
		return body
	}
	if len(kept) == 0 {
		result, err := sjson.DeleteBytes(body, "include")
		if err != nil {
			return body
		}
		return result
	}
	result, err := sjson.SetBytes(body, "include", kept)
	if err != nil {
		return body
	}
	return result
}

func responsesIncludeBelongsToStrippedTool(entry string, strippedTypes map[string]struct{}) bool {
	entry = strings.TrimSpace(entry)
	for toolType := range strippedTypes {
		prefixes := strippedToolIncludePrefixes[toolType]
		if prefixes == nil {
			prefixes = []string{toolType + "_call."}
		}
		for _, prefix := range prefixes {
			if strings.HasPrefix(entry, prefix) {
				return true
			}
		}
	}
	return false
}
