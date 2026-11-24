package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var codexSelectedPageName = regexp.MustCompile(`^codex_apps_selected_page_part_([1-9][0-9]*)_of_([1-9][0-9]*)$`)

// Codex injects external context as standalone outputs, without a model tool call.
// Use the legacy user-message representation for HTTP Responses interoperability.
func normalizeCodexUntrustedContext(body []byte) ([]byte, bool) {
	if !mayContainCodexUntrustedContext(body) || !hasUniqueJSONMembers(body) {
		return body, false
	}
	var request map[string]json.RawMessage
	if json.Unmarshal(body, &request) != nil {
		return body, false
	}
	var input []json.RawMessage
	if json.Unmarshal(request["input"], &input) != nil {
		return body, false
	}
	changed := false
	for i, raw := range input {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil {
			continue
		}
		readString := func(key string) string {
			var value string
			_ = json.Unmarshal(item[key], &value)
			return value
		}
		namespace := readString("namespace")
		if readString("type") != "function_call_output" {
			continue
		}
		if callID, exists := item["call_id"]; exists {
			var value string
			if bytes.Equal(bytes.TrimSpace(callID), []byte("null")) || json.Unmarshal(callID, &value) != nil || strings.TrimSpace(value) != "" {
				continue
			}
		}
		name := readString("name")
		var rawOutput *string
		if json.Unmarshal(item["output"], &rawOutput) != nil || rawOutput == nil {
			continue
		}
		output := *rawOutput
		var text string
		switch {
		case namespace == "agent" && validCodexUntrustedContext(name, output):
			text = "<external_" + name + ">" + output + "</external_" + name + ">"
			if validCodexPageContext(name, output) {
				text = "<external_" + name + ">\n" + output + "\n</external_" + name + ">"
			}
		case namespace == "guardian" && validCodexGuardianContext(name, output):
			text = "<external_" + name + ">" + output + "</external_" + name + ">"
		case namespace == "cloud_threads" && name == "send_message" && validCodexCloudDelegationEnvelope(output):
			// 与 codex_app/codex_tui 委派 bootstrap 一致，信封原文作为 user 文本。
			text = output
		default:
			continue
		}
		message := map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]string{{
				"type": "input_text",
				"text": text,
			}},
		}
		encoded, err := json.Marshal(message)
		if err != nil {
			return body, false
		}
		input[i] = encoded
		changed = true
	}
	if !changed {
		return body, false
	}
	encodedInput, err := json.Marshal(input)
	if err != nil {
		return body, false
	}
	request["input"] = encodedInput
	encoded, err := json.Marshal(request)
	if err != nil {
		return body, false
	}
	return encoded, true
}

// validCodexUntrustedContext 覆盖 agent 命名空间的独立上下文。#8769 观察到的 browser_info、
// goal_objective、untrusted_context 之外，openai/codex（e58bf8f5233b）的 session/approved_plan
// 与 context-fragments 还会产出 proposed_plan、untrusted_content。
func validCodexUntrustedContext(name, output string) bool {
	switch name {
	case "browser_tabs":
		// Desktop wraps the tab list as ambient UI state.
		payload, ok := strings.CutPrefix(strings.TrimSpace(output), `<in-app-browser-context source="ambient-ui-state">`)
		payload, closed := strings.CutSuffix(payload, "</in-app-browser-context>")
		return ok && closed && strings.TrimSpace(payload) != ""
	case "browser_info", "terminal_info", "goal_objective", "untrusted_context", "ide_context":
		return strings.TrimSpace(output) != ""
	case "untrusted_content":
		// Codex's fallback for an unnamed context source can carry any text, including an empty value.
		return true
	case "proposed_plan":
		// Codex splits an approved plan by byte length, so a chunk can be all whitespace.
		return output != ""
	default:
		return validCodexPageContext(name, output)
	}
}

// validCodexGuardianContext 覆盖 ext/guardian-v3/sampling 注入的 guardian 输入，输出是 JSON 对象。
func validCodexGuardianContext(name, output string) bool {
	switch name {
	case "guardian_housekeeper_input", "guardian_channel_selection_input", "guardian_original_evidence":
		return json.Valid([]byte(output)) && hasUniqueJSONMembers([]byte(output))
	}
	return false
}

// validCodexCloudDelegationEnvelope 识别 cloud_threads/send_message 投递的委派信封；
// 云端投递可能在常规信封里加 Dot 归属（source_orbit_id）。
func validCodexCloudDelegationEnvelope(output string) bool {
	const open, close = "<source_orbit_id>", "</source_orbit_id>"
	if before, rest, found := strings.Cut(output, open); found {
		orbit, after, ok := strings.Cut(rest, close)
		if !ok || strings.TrimSpace(orbit) == "" || strings.ContainsAny(orbit, "<>") ||
			!strings.HasSuffix(strings.TrimSpace(before), "</source_thread_id>") ||
			!strings.HasPrefix(strings.TrimSpace(after), "<input>") {
			return false
		}
		output = before + after
	}
	return validCodexDelegationEnvelope(output)
}

func validCodexPageContext(name, output string) bool {
	if name == "codex_apps_open_page" {
		return validCodexPageSnapshot(output)
	}
	parts := codexSelectedPageName.FindStringSubmatch(name)
	if len(parts) != 3 {
		return false
	}
	part, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	total, err := strconv.Atoi(parts[2])
	if err != nil || part > total {
		return false
	}
	header, snapshot, ok := strings.Cut(output, "\n")
	if !ok || strings.TrimSuffix(header, "\r") != fmt.Sprintf("Companion Page snapshot part %d of %d:", part, total) || strings.TrimSpace(snapshot) == "" {
		return false
	}
	// A multi-part snapshot may split inside a JSON string; preserve each chunk verbatim.
	return total > 1 || validCodexPageSnapshot(snapshot)
}

func validCodexPageSnapshot(text string) bool {
	if !hasUniqueJSONMembers([]byte(text)) {
		return false
	}
	var snapshot map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &snapshot) != nil {
		return false
	}
	id, exists := snapshot["page_id"]
	if !exists {
		return false
	}
	if bytes.Equal(bytes.TrimSpace(id), []byte("null")) {
		return true
	}
	var value string
	return json.Unmarshal(id, &value) == nil && strings.TrimSpace(value) != ""
}

var (
	codexFunctionCallOutputMarker = []byte("function_call_output")
	codexUntrustedContextMarkers  = [][]byte{
		[]byte("codex_apps_open_page"),
		[]byte("codex_apps_selected_page_part_"),
		[]byte("browser_info"),
		[]byte("browser_tabs"),
		[]byte("terminal_info"),
		[]byte("goal_objective"),
		[]byte("untrusted_context"),
		[]byte("proposed_plan"),
		[]byte("untrusted_content"),
		[]byte("ide_context"),
		[]byte("guardian_"),
		[]byte("cloud_threads"),
	}
)

// mayContainCodexUntrustedContext is a byte precheck before the uniqueness scan and
// full decode. Without the literal markers, only escaped letters could spell them.
func mayContainCodexUntrustedContext(body []byte) bool {
	if service.JSONMayEscapePlainASCII(body) {
		return true
	}
	if !bytes.Contains(body, codexFunctionCallOutputMarker) {
		return false
	}
	for _, marker := range codexUntrustedContextMarkers {
		if bytes.Contains(body, marker) {
			return true
		}
	}
	return false
}

// hasUniqueJSONMembers reports whether body is a single JSON value without
// duplicate object keys at any depth. Decoders disagree on duplicate keys, so
// such bodies are left unchanged.
func hasUniqueJSONMembers(body []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if !consumeUniqueJSONValue(decoder) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func consumeUniqueJSONValue(decoder *json.Decoder) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return true
	}

	switch delim {
	case '{':
		members := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return false
			}
			key, ok := keyToken.(string)
			if !ok {
				return false
			}
			if _, duplicate := members[key]; duplicate {
				return false
			}
			members[key] = struct{}{}
			if !consumeUniqueJSONValue(decoder) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for decoder.More() {
			if !consumeUniqueJSONValue(decoder) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim(']')
	default:
		return false
	}
}
