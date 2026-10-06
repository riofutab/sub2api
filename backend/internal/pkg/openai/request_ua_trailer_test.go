package openai

import "testing"

func TestEnsureCodexUATrailer(t *testing.T) {
	tests := []struct {
		name       string
		ua         string
		originator string
		version    string
		want       string
	}{
		{
			name:       "缺尾段时补上客户端标识组",
			ua:         "codex-tui/0.160.1 (Ubuntu 22.4.0; x86_64) xterm-256color",
			originator: "codex-tui",
			version:    "0.160.1",
			want:       "codex-tui/0.160.1 (Ubuntu 22.4.0; x86_64) xterm-256color (codex-tui; 0.160.1)",
		},
		{
			name:       "已有官方尾段时原样保留",
			ua:         "codex_exec/0.160.1 (Mac OS 15.6.0; arm64) unknown (codex_exec; 0.160.1)",
			originator: "codex_exec",
			version:    "0.160.1",
			want:       "codex_exec/0.160.1 (Mac OS 15.6.0; arm64) unknown (codex_exec; 0.160.1)",
		},
		{
			name:       "OS 组不被当成尾段",
			ua:         "codex_vscode/0.160.1 (Mac OS 15.6.0; arm64)",
			originator: "codex_vscode",
			version:    "0.160.1",
			want:       "codex_vscode/0.160.1 (Mac OS 15.6.0; arm64) (codex_vscode; 0.160.1)",
		},
		{
			name:       "缺少 originator 或版本时不改",
			ua:         "codex-tui/0.160.1 (Ubuntu 22.4.0; x86_64) xterm-256color",
			originator: "",
			version:    "0.160.1",
			want:       "codex-tui/0.160.1 (Ubuntu 22.4.0; x86_64) xterm-256color",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EnsureCodexUATrailer(tt.ua, tt.originator, tt.version); got != tt.want {
				t.Fatalf("EnsureCodexUATrailer() = %q, want %q", got, tt.want)
			}
		})
	}
}
