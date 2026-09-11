package relay

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// responsesToolNameMaxLen mirrors the 64-character function-name ceiling the
// OpenAI tool-call schema imposes and strict upstreams (e.g. CommandCode)
// enforce with a hard 400.
const responsesToolNameMaxLen = 64

// truncateResponsesToolName shortens an over-long tool name deterministically:
// a UTF-8-safe prefix plus "__" followed by 12 hex chars of the SHA-256 of the
// full name, so the same original always maps to the same shortened name and
// input-history rewrites stay consistent across turns.
func truncateResponsesToolName(name string) string {
	if len(name) <= responsesToolNameMaxLen {
		return name
	}
	digest := sha256.Sum256([]byte(name))
	suffix := "__" + hex.EncodeToString(digest[:6])
	prefixLen := responsesToolNameMaxLen - len(suffix)
	for prefixLen > 0 && prefixLen < len(name) && (name[prefixLen]&0xc0) == 0x80 {
		prefixLen--
	}
	return name[:prefixLen] + suffix
}

// sanitizeResponsesToolNames rewrites every client-declared function or custom
// tool name longer than 64 characters — in the tools declaration, in
// input-history function_call / custom_tool_call items, and in a pinned
// tool_choice — into the deterministic short form, registering each rename in
// the bridge so the response side restores the original name. Bodies whose
// names all fit are returned byte-identical.
func sanitizeResponsesToolNames(body []byte, bridge *relaycommon.ResponsesClientToolBridge) []byte {
	if len(body) == 0 || bridge == nil {
		return body
	}
	result := body
	changed := false
	rename := func(path, name string) {
		name = strings.TrimSpace(name)
		short := truncateResponsesToolName(name)
		if short == name {
			return
		}
		updated, err := sjson.SetBytes(result, path, short)
		if err != nil {
			return
		}
		result = updated
		changed = true
		bridge.Register(short, relaycommon.ResponsesClientToolSpec{
			Kind: relaycommon.ResponsesClientToolRenamed,
			Name: name,
		})
	}

	tools := gjson.GetBytes(result, "tools")
	if tools.IsArray() {
		for i, tool := range tools.Array() {
			switch strings.ToLower(tool.Get("type").String()) {
			case "function", "custom":
			default:
				continue
			}
			if name := tool.Get("name"); name.Exists() {
				rename(fmt.Sprintf("tools.%d.name", i), name.String())
				continue
			}
			if nested := tool.Get("function.name"); nested.Exists() {
				rename(fmt.Sprintf("tools.%d.function.name", i), nested.String())
			}
		}
	}

	input := gjson.GetBytes(result, "input")
	if input.IsArray() {
		for i, item := range input.Array() {
			switch strings.ToLower(item.Get("type").String()) {
			case "function_call", "custom_tool_call":
			default:
				continue
			}
			rename(fmt.Sprintf("input.%d.name", i), item.Get("name").String())
		}
	}

	if choice := gjson.GetBytes(result, "tool_choice"); choice.IsObject() &&
		strings.EqualFold(choice.Get("type").String(), "function") {
		rename("tool_choice.name", choice.Get("name").String())
	}

	if !changed {
		return body
	}
	return result
}
