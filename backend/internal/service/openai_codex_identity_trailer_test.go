package service

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

// codex-rs 只在 app-server 收到 clientInfo 初始化时同时改写 originator 并在 UA 末尾追加
// `(clientInfo.name; clientInfo.version)`；默认 originator codex_cli_rs 的真实 UA 不带这一组。
func TestEnforceCodexIdentityHeaders_DefaultCLIUAHasNoClientTrailer(t *testing.T) {
	h := make(http.Header)
	h.Set("originator", "codex-tui")

	enforceCodexIdentityHeaders(h)

	require.Equal(t, openai.CodexCLIOriginator, h.Get("originator"))
	require.Equal(t, openai.CodexCLIOriginator+"/"+codexCLIVersion+codexCLIUserAgentSuffix, h.Get("user-agent"))
}

func TestEnforceCodexIdentityHeaders_OverrideUAWithoutTrailerGetsOne(t *testing.T) {
	h := make(http.Header)
	h.Set("originator", "codex-tui")

	enforceCodexIdentityHeadersWithUA(h, "codex_vscode/0.150.0 (Mac OS 15.0; arm64) vscode")

	require.Equal(t, "codex_vscode/"+codexCLIVersion+" (Mac OS 15.0; arm64) vscode (codex_vscode; "+codexCLIVersion+")", h.Get("user-agent"))
}

func TestEnforceCodexIdentityHeaders_SyncedVersionUpdatesTrailer(t *testing.T) {
	SetCodexCanonicalUserAgentResolver(func() string { return buildCodexCLIUserAgent("0.200.1") })
	t.Cleanup(func() { SetCodexCanonicalUserAgentResolver(nil) })
	h := make(http.Header)
	h.Set("originator", "codex-tui")

	enforceCodexIdentityHeadersWithUA(h, "codex_exec/0.150.0 (Mac OS 15.0; arm64) iTerm.app (codex_exec; 0.150.0)")

	require.Equal(t, "codex_exec/0.200.1 (Mac OS 15.0; arm64) iTerm.app (codex_exec; 0.200.1)", h.Get("user-agent"))
	require.Equal(t, "0.200.1", h.Get("version"))
}
