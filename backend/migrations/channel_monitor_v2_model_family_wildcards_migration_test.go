package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 250 号迁移只给非空名单追加模型族通配项，空名单（显示全部真实模型）保持不变，且可重复执行。
func TestChannelMonitorV2ModelFamilyWildcardsMigration(t *testing.T) {
	content, err := FS.ReadFile("250_channel_monitor_v2_model_family_wildcards.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	for _, family := range []string{
		`('anthropic', '["claude-*"]'::jsonb)`,
		`('kiro', '["claude-*"]'::jsonb)`,
		`('antigravity', '["claude-*", "gemini-*"]'::jsonb)`,
		`('openai', '["gpt-*"]'::jsonb)`,
		`('grok', '["grok-*"]'::jsonb)`,
		`('gemini', '["gemini-*"]'::jsonb)`,
	} {
		require.Contains(t, sql, family)
	}
	require.Contains(t, sql, "jsonb_array_length(t.elem -> 'models') > 0")
	require.Contains(t, sql, "WHERE NOT (t.elem -> 'models') @> jsonb_build_array(p.pattern)")
	require.Contains(t, sql, "ORDER BY t.ord")
	require.Contains(t, sql, "c.platforms IS DISTINCT FROM patched.platforms")
}
