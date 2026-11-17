//go:build unit

package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// buildCodexResponsesBody 生成接近真实 Codex CLI /v1/responses 请求形状的约 targetSize 字节请求体：
// 多轮 message / reasoning(encrypted_content) / function_call / function_call_output，
// 不含 codex_app bootstrap，也不含 compaction_trigger（最常见的请求）。
func buildCodexResponsesBody(tb testing.TB, targetSize int) []byte {
	tb.Helper()
	mustJSON := func(v any) []byte {
		out, err := json.Marshal(v)
		if err != nil {
			tb.Fatalf("marshal: %v", err)
		}
		return out
	}
	output := strings.Repeat("drwxr-xr-x  12 user  staff   384 Sep 25 10:00 internal\n", 60)
	encrypted := strings.Repeat("gAAAAABo", 200)

	var input bytes.Buffer
	input.WriteByte('[')
	input.Write(mustJSON(map[string]any{
		"type": "message", "role": "user",
		"content": []any{map[string]any{"type": "input_text", "text": "列出仓库结构并修复测试"}},
	}))
	for turn := 0; input.Len() < targetSize; turn++ {
		callID := fmt.Sprintf("call_%020d", turn)
		input.WriteByte(',')
		input.Write(mustJSON(map[string]any{"type": "reasoning", "summary": []any{}, "encrypted_content": encrypted}))
		input.WriteByte(',')
		input.Write(mustJSON(map[string]any{"type": "function_call", "name": "shell", "call_id": callID, "arguments": `{"command":["ls","-la"]}`}))
		input.WriteByte(',')
		input.Write(mustJSON(map[string]any{"type": "function_call_output", "call_id": callID, "output": output}))
	}
	input.WriteByte(']')

	var body bytes.Buffer
	body.WriteString(`{"model":"gpt-5.1-codex","instructions":`)
	body.Write(mustJSON(strings.Repeat("You are Codex, a coding agent. ", 200)))
	body.WriteString(`,"input":`)
	body.Write(input.Bytes())
	body.WriteString(`,"tools":[{"type":"function","name":"shell","parameters":{"type":"object","properties":{"command":{"type":"array","items":{"type":"string"}}}}}],"tool_choice":"auto","parallel_tool_calls":false,"reasoning":{"effort":"medium","summary":"auto"},"store":false,"stream":true,"include":["reasoning.encrypted_content"],"prompt_cache_key":"6d1f3c2a-1b2c-4d5e-8f90-123456789abc"}`)
	return body.Bytes()
}

// runOpenAIResponsesBodyPrep 复现 /v1/responses 热路径上与 body 大小相关的两段预处理：
// handler 的两次 Codex bootstrap 归一化 + compaction 检测，以及 Forward 里的 compaction 顺序归一化。
func runOpenAIResponsesBodyPrep(tb testing.TB, body []byte) []byte {
	if normalized, changed := normalizeCodexAutomationBootstrap(body); changed {
		body = normalized
	}
	if normalized, changed := normalizeCodexDelegationBootstrap(body); changed {
		body = normalized
	}
	_ = isOpenAIRemoteCompactionV2Request(body)
	normalized, changed, err := service.NormalizeCompactionTriggerInputOrder(body)
	if err != nil {
		tb.Fatalf("normalize compaction: %v", err)
	}
	if changed {
		body = normalized
	}
	return body
}

func BenchmarkOpenAIResponsesBodyPrep(b *testing.B) {
	body := buildCodexResponsesBody(b, 1<<20)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := runOpenAIResponsesBodyPrep(b, body)
		runtime.KeepAlive(out)
	}
}
