//go:build unit

package handler

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeCodexBootstrap_NoCodexNamespaceSkipsDecoding(t *testing.T) {
	body := buildCodexResponsesBody(t, 128<<10)
	for name, normalize := range map[string]func([]byte) ([]byte, bool){
		"automation": normalizeCodexAutomationBootstrap,
		"delegation": normalizeCodexDelegationBootstrap,
	} {
		var got []byte
		var changed bool
		allocs := testing.AllocsPerRun(5, func() {
			got, changed = normalize(body)
		})
		require.False(t, changed, name)
		require.Equal(t, unsafe.SliceData(body), unsafe.SliceData(got), name)
		require.Zero(t, allocs, "请求体不含 codex_app/codex_tui 时不应解码 (%s)", name)
	}
}

func TestNormalizeCodexDelegationBootstrap_EscapedNamespaceStillNormalized(t *testing.T) {
	// JSON 转义后的 namespace 在原始字节里看不到 codex_app，预检不能因此漏掉。
	escapedUnderscore := "\\" + "u005f"
	body := []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex` + escapedUnderscore + `app","name":"create_thread","output":"` + delegationEnvelope + `"}]}`)
	require.NotContains(t, string(body), "codex_app")
	got, changed := normalizeCodexDelegationBootstrap(body)
	require.True(t, changed)
	require.Equal(t, "message", gjson.GetBytes(got, "input.0.type").String())
	require.Equal(t, delegationEnvelope, gjson.GetBytes(got, "input.0.content.0.text").String())
}

func TestNormalizeCodexDelegationBootstrap_TUINamespaceStillNormalized(t *testing.T) {
	body := []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_tui","name":"send_message_to_thread","output":"` + delegationEnvelope + `"}]}`)
	_, changed := normalizeCodexDelegationBootstrap(body)
	require.True(t, changed)
}
