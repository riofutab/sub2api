//go:build unit

package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

const benchAnthropicChainModel = "claude-sonnet-4-5-20250929"

// buildClaudeCodeThinkingBody 生成接近真实 Claude Code 请求形状的请求体：
// 顶层键顺序 model/messages/system/tools/metadata/max_tokens/thinking/stream，
// 历史 assistant 回合带合法签名的 thinking 块 + tool_use，user 回合带大段 tool_result。
func buildClaudeCodeThinkingBody(tb testing.TB, targetSize int) []byte {
	tb.Helper()
	mustJSON := func(v any) []byte {
		out, err := json.Marshal(v)
		if err != nil {
			tb.Fatalf("marshal: %v", err)
		}
		return out
	}
	signature := strings.Repeat("EqQBCkgIBxABGAIqQKzJ", 20)
	toolOutput := strings.Repeat("func handler(w http.ResponseWriter, r *http.Request) { /* 代码片段 */ }\n", 80)

	var msgs bytes.Buffer
	msgs.WriteByte('[')
	msgs.Write(mustJSON(map[string]any{
		"role":    "user",
		"content": []any{map[string]any{"type": "text", "text": "请帮我重构 gateway 模块"}},
	}))
	for turn := 0; msgs.Len() < targetSize; turn++ {
		toolID := fmt.Sprintf("toolu_%024d", turn)
		msgs.WriteByte(',')
		msgs.Write(mustJSON(map[string]any{
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "thinking", "thinking": strings.Repeat("先分析调用链，再决定改哪里。", 30), "signature": signature},
				map[string]any{"type": "text", "text": "我先读一下相关文件。"},
				map[string]any{"type": "tool_use", "id": toolID, "name": "Read", "input": map[string]any{"file_path": fmt.Sprintf("/repo/internal/file_%d.go", turn)}},
			},
		}))
		msgs.WriteByte(',')
		msgs.Write(mustJSON(map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": toolID, "content": []any{map[string]any{"type": "text", "text": toolOutput}}},
			},
		}))
	}
	msgs.WriteByte(',')
	msgs.Write(mustJSON(map[string]any{
		"role": "user",
		"content": []any{map[string]any{
			"type": "text", "text": "继续",
			"cache_control": map[string]any{"type": "ephemeral"},
		}},
	}))
	msgs.WriteByte(']')

	system := mustJSON([]any{
		map[string]any{"type": "text", "text": "x-anthropic-billing-header: cc_version=2.1.0.abc; cc_entrypoint=cli; cch=00000;"},
		map[string]any{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude."},
		map[string]any{"type": "text", "text": strings.Repeat("Follow the repository conventions. ", 400), "cache_control": map[string]any{"type": "ephemeral"}},
	})
	tools := make([]any, 0, 20)
	for i := 0; i < 20; i++ {
		tools = append(tools, map[string]any{
			"name":        fmt.Sprintf("Tool%d", i),
			"description": strings.Repeat("Tool description. ", 40),
			"input_schema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"path": map[string]any{"type": "string"}, "limit": map[string]any{"type": "number"}},
				"required":   []string{"path"},
			},
		})
	}

	var body bytes.Buffer
	body.WriteString(`{"model":"` + benchAnthropicChainModel + `","messages":`)
	body.Write(msgs.Bytes())
	body.WriteString(`,"system":`)
	body.Write(system)
	body.WriteString(`,"tools":`)
	body.Write(mustJSON(tools))
	body.WriteString(`,"metadata":{"user_id":"user_abc_account__session_6d1f3c2a-1b2c-4d5e-8f90-123456789abc"},"max_tokens":32000,"thinking":{"type":"enabled","budget_tokens":31999},"stream":true}`)
	return body.Bytes()
}

// runAnthropicBodyChain 复现 handler → Forward 发送前的 body 处理链
// （Claude Code 客户端 + Anthropic OAuth 账号、无 mimicry、无模型改写的常见路径）。
func runAnthropicBodyChain(tb testing.TB, raw []byte) []byte {
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(raw), domain.PlatformAnthropic)
	if err != nil {
		tb.Fatalf("parse: %v", err)
	}
	// handler: 每次账号尝试克隆一次 body 视图。
	attempt, err := parsed.CloneForBody(parsed.Body.Bytes())
	if err != nil {
		tb.Fatalf("clone: %v", err)
	}
	// handler: ApplyBedrockCCCompat 对非 Bedrock 渠道原样返回 body。
	if err := attempt.ReplaceBody(attempt.Body.Bytes()); err != nil {
		tb.Fatalf("bedrock compat replace: %v", err)
	}
	body := attempt.Body.Bytes()
	_ = systemHasBillingAttributionBlock(body)
	replace := func(next []byte) {
		if err := attempt.ReplaceBody(next); err != nil {
			tb.Fatalf("replace: %v", err)
		}
		body = attempt.Body.Bytes()
	}
	replace(enforceCacheControlLimit(body))
	replace(StripEmptyTextBlocks(body))
	replace(FilterWebSearchHistoryBlocks(body, attempt.Model))
	replace(FilterThinkingBlocks(body, attempt.Model))
	return body
}

func BenchmarkAnthropicBodyChain(b *testing.B) {
	body := buildClaudeCodeThinkingBody(b, 1<<20)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := runAnthropicBodyChain(b, body)
		runtime.KeepAlive(out)
	}
}

func BenchmarkAnthropicFilterThinkingBlocksValidSignatures(b *testing.B) {
	body := buildClaudeCodeThinkingBody(b, 1<<20)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := FilterThinkingBlocks(body, benchAnthropicChainModel)
		runtime.KeepAlive(out)
	}
}

func BenchmarkAnthropicReplaceBodyUnchanged(b *testing.B) {
	body := buildClaudeCodeThinkingBody(b, 1<<20)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), domain.PlatformAnthropic)
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := parsed.ReplaceBody(parsed.Body.Bytes()); err != nil {
			b.Fatalf("replace: %v", err)
		}
	}
}

func BenchmarkAnthropicParseGatewayRequest(b *testing.B) {
	body := buildClaudeCodeThinkingBody(b, 1<<20)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), domain.PlatformAnthropic)
		if err != nil {
			b.Fatalf("parse: %v", err)
		}
		runtime.KeepAlive(parsed)
	}
}
