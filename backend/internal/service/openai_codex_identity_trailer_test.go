package service

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

// 真实 Codex 客户端（TUI / exec / VS Code）经 app-server 初始化后，UA 末尾都带
// `(clientInfo.name; clientInfo.version)`；缺这一段的形态不是真实客户端能发出的。
func TestEnforceCodexIdentityHeaders_DefaultUAHasClientTrailer(t *testing.T) {
	h := make(http.Header)
	h.Set("originator", "codex-tui")

	enforceCodexIdentityHeaders(h)

	want := openai.CodexDefaultOriginator + "/" + codexCLIVersion + codexCLIUserAgentSuffix +
		" (" + openai.CodexDefaultOriginator + "; " + codexCLIVersion + ")"
	require.Equal(t, want, h.Get("user-agent"))
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

	enforceCodexIdentityHeaders(h)

	require.Equal(t, "codex-tui/0.200.1"+codexCLIUserAgentSuffix+" (codex-tui; 0.200.1)", h.Get("user-agent"))
	require.Equal(t, "0.200.1", h.Get("version"))
}
