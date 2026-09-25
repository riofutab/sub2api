//go:build unit

package service

import (
	"testing"
	"unsafe"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/stretchr/testify/require"
)

// thinkingFilterCases 覆盖快路径判定的各个分支；每个用例都与 map 慢路径逐字节对照。
var thinkingFilterCases = []struct {
	name       string
	body       string
	wantFilter map[bool]bool // alwaysThinking -> 是否应当改写
}{
	{
		name:       "enabled_valid_signature_kept",
		body:       `{"thinking":{"type":"enabled"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"sig"},{"type":"text","text":"a"}]}]}`,
		wantFilter: map[bool]bool{false: false, true: false},
	},
	{
		name:       "adaptive_valid_signature_kept",
		body:       `{"thinking":{"type":"adaptive"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"sig"}]}]}`,
		wantFilter: map[bool]bool{false: false, true: false},
	},
	{
		name:       "enabled_missing_signature_filtered",
		body:       `{"thinking":{"type":"enabled"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"t"},{"type":"text","text":"a"}]}]}`,
		wantFilter: map[bool]bool{false: true, true: true},
	},
	{
		name:       "enabled_empty_signature_filtered",
		body:       `{"thinking":{"type":"enabled"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":""}]}]}`,
		wantFilter: map[bool]bool{false: true, true: true},
	},
	{
		name:       "enabled_non_string_signature_filtered",
		body:       `{"thinking":{"type":"enabled"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":123}]}]}`,
		wantFilter: map[bool]bool{false: true, true: true},
	},
	{
		name:       "enabled_dummy_signature_filtered",
		body:       `{"thinking":{"type":"enabled"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"` + antigravity.DummyThoughtSignature + `"}]}]}`,
		wantFilter: map[bool]bool{false: true, true: true},
	},
	{
		name:       "disabled_thinking_block_filtered",
		body:       `{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"sig"},{"type":"text","text":"a"}]}]}`,
		wantFilter: map[bool]bool{false: true, true: false},
	},
	{
		name:       "thinking_type_disabled_filtered",
		body:       `{"thinking":{"type":"disabled"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"sig"}]}]}`,
		wantFilter: map[bool]bool{false: true, true: false},
	},
	{
		name:       "user_role_thinking_block_filtered",
		body:       `{"thinking":{"type":"enabled"},"messages":[{"role":"user","content":[{"type":"thinking","thinking":"t","signature":"sig"},{"type":"text","text":"u"}]}]}`,
		wantFilter: map[bool]bool{false: true, true: true},
	},
	{
		name:       "redacted_with_signature_kept",
		body:       `{"thinking":{"type":"enabled"},"messages":[{"role":"assistant","content":[{"type":"redacted_thinking","data":"d","signature":"sig"}]}]}`,
		wantFilter: map[bool]bool{false: false, true: false},
	},
	{
		name:       "redacted_data_only_kept_only_when_always_thinking",
		body:       `{"thinking":{"type":"enabled"},"messages":[{"role":"assistant","content":[{"type":"redacted_thinking","data":"d"}]}]}`,
		wantFilter: map[bool]bool{false: true, true: false},
	},
	{
		name:       "untyped_thinking_key_filtered",
		body:       `{"thinking":{"type":"enabled"},"messages":[{"role":"assistant","content":[{"thinking":"t","signature":"sig"},{"type":"text","text":"a"}]}]}`,
		wantFilter: map[bool]bool{false: true, true: true},
	},
	{
		name:       "non_string_type_with_thinking_key_filtered",
		body:       `{"thinking":{"type":"enabled"},"messages":[{"role":"assistant","content":[{"type":7,"thinking":null}]}]}`,
		wantFilter: map[bool]bool{false: true, true: true},
	},
	{
		name:       "nested_thinking_field_in_tool_input_untouched",
		body:       `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"x","input":{"thinking":"deep"}}]}]}`,
		wantFilter: map[bool]bool{false: false, true: false},
	},
	{
		name:       "string_content_untouched",
		body:       `{"messages":[{"role":"assistant","content":"{\"type\":\"thinking\"}"}]}`,
		wantFilter: map[bool]bool{false: false, true: false},
	},
	{
		name:       "duplicate_thinking_key_last_wins_for_map_path",
		body:       `{"thinking":{"type":"enabled"},"thinking":{"type":"disabled"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"sig"}]}]}`,
		wantFilter: map[bool]bool{false: true, true: false},
	},
	{
		name:       "duplicate_signature_key_last_wins",
		body:       `{"thinking":{"type":"enabled"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"sig","signature":""}]}]}`,
		wantFilter: map[bool]bool{false: true, true: true},
	},
	{
		name:       "duplicate_role_key_last_wins",
		body:       `{"thinking":{"type":"enabled"},"messages":[{"role":"assistant","role":"user","content":[{"type":"thinking","thinking":"t","signature":"sig"}]}]}`,
		wantFilter: map[bool]bool{false: true, true: true},
	},
	{
		name:       "duplicate_messages_key_last_wins",
		body:       `{"thinking":{"type":"enabled"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"t"}]}],"messages":[{"role":"user","content":"hi"}]}`,
		wantFilter: map[bool]bool{false: false, true: false},
	},
	{
		name:       "escaped_type_value",
		body:       `{"thinking":{"type":"enabled"},"messages":[{"role":"assistant","content":[{"type":"thin` + jsonUnicodeEscapeForTest("006b") + `ing","thinking":"t"}]}]}`,
		wantFilter: map[bool]bool{false: true, true: true},
	},
	{
		name:       "escaped_signature_key_and_role",
		body:       `{"thinking":{"type":"enabled"},"messages":[{"role":"assist` + jsonUnicodeEscapeForTest("0061") + `nt","content":[{"type":"thinking","thinking":"t","sig` + jsonUnicodeEscapeForTest("006e") + `ature":"s"}]}]}`,
		wantFilter: map[bool]bool{false: false, true: false},
	},
	{
		name:       "messages_not_array",
		body:       `{"thinking":{"type":"enabled"},"messages":{"type":"thinking"}}`,
		wantFilter: map[bool]bool{false: false, true: false},
	},
	{
		name:       "non_object_blocks_and_messages",
		body:       `{"messages":[1,"x",{"role":"assistant","content":[null,2,{"type":"thinking","thinking":"t","signature":"s"}]}]}`,
		wantFilter: map[bool]bool{false: true, true: false},
	},
	{
		name:       "top_level_array",
		body:       `[{"type":"thinking"}]`,
		wantFilter: map[bool]bool{false: false, true: false},
	},
	{
		name:       "invalid_json",
		body:       `{"messages":[{"role":"assistant","content":[{"type":"thinking"`,
		wantFilter: map[bool]bool{false: false, true: false},
	},
}

func TestFilterThinkingBlocksInternal_MatchesMapPath(t *testing.T) {
	for _, tc := range thinkingFilterCases {
		for _, alwaysThinking := range []bool{false, true} {
			body := []byte(tc.body)
			want := filterThinkingBlocksSlow(body, alwaysThinking)
			got := filterThinkingBlocksInternal(body, alwaysThinking)
			require.Equal(t, string(want), string(got), "case=%s alwaysThinking=%v", tc.name, alwaysThinking)
			changed := string(got) != tc.body
			require.Equal(t, tc.wantFilter[alwaysThinking], changed, "case=%s alwaysThinking=%v", tc.name, alwaysThinking)
			if !changed {
				require.Equal(t, unsafe.SliceData(body), unsafe.SliceData(got), "未改写时必须原样返回入参切片 case=%s", tc.name)
			}
		}
	}
}

func TestFilterThinkingBlocks_ValidSignaturesTakeFastPath(t *testing.T) {
	body := buildClaudeCodeThinkingBody(t, 256<<10)
	var out []byte
	allocs := testing.AllocsPerRun(5, func() {
		out = FilterThinkingBlocks(body, benchAnthropicChainModel)
	})
	require.Equal(t, unsafe.SliceData(body), unsafe.SliceData(out))
	// 快路径只遍历不反序列化；map 慢路径在该请求体上是上千次分配。
	require.LessOrEqual(t, allocs, float64(16))
}

func TestFilterThinkingBlocks_MissingSignatureStillFilteredOnLargeBody(t *testing.T) {
	body := buildClaudeCodeThinkingBody(t, 64<<10)
	broken := []byte(string(body[:len(body)-1]))
	// 在 messages 末尾追加一个缺签名的 assistant thinking 块。
	idx := len(`{"model":"` + benchAnthropicChainModel + `","messages":[`)
	inject := `{"role":"assistant","content":[{"type":"thinking","thinking":"no sig"},{"type":"text","text":"x"}]},`
	broken = append([]byte(string(broken[:idx])+inject), broken[idx:]...)
	broken = append(broken, '}')

	out := FilterThinkingBlocks(broken, benchAnthropicChainModel)
	require.Equal(t, string(filterThinkingBlocksSlow(broken, false)), string(out))
	require.NotContains(t, string(out), "no sig")
}
