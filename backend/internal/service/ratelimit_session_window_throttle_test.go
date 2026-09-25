//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func steadyOAuthSuccessHeaders(resetUnix, reset7dUnix int64, util5h, util7d string) http.Header {
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-5h-reset", fmt.Sprintf("%d", resetUnix))
	headers.Set("anthropic-ratelimit-unified-5h-utilization", util5h)
	headers.Set("anthropic-ratelimit-unified-7d-utilization", util7d)
	headers.Set("anthropic-ratelimit-unified-7d-reset", fmt.Sprintf("%d", reset7dUnix))
	return headers
}

// 稳态账号：状态、窗口、utilization 与已持久化的值一致，成功响应不应再写库。
func TestUpdateSessionWindow_SkipsWritesWhenNothingChanged(t *testing.T) {
	resetUnix := time.Now().Add(3 * time.Hour).Unix()
	reset7dUnix := time.Now().Add(72 * time.Hour).Unix()
	windowEnd := time.Unix(resetUnix, 0)
	account := &Account{
		ID: 501, SessionWindowEnd: &windowEnd, SessionWindowStatus: "allowed",
		// 调度快照里的 extra 来自 JSON 解码，数值是 float64
		Extra: map[string]any{
			"session_window_utilization":   0.42,
			"passive_usage_7d_utilization": 0.3,
			"passive_usage_7d_reset":       float64(reset7dUnix),
		},
	}
	repo := &sessionWindowMockRepo{}
	svc := NewRateLimitService(repo, nil, nil, nil, nil)

	const responses = 10
	for i := 0; i < responses; i++ {
		svc.UpdateSessionWindow(context.Background(), account, steadyOAuthSuccessHeaders(resetUnix, reset7dUnix, "0.42", "0.3"))
	}

	t.Logf("%d steady OAuth success responses: UpdateSessionWindow=%d UpdateExtra=%d", responses, len(repo.sessionWindowCalls), len(repo.updateExtraCalls))
	require.Empty(t, repo.sessionWindowCalls)
	require.Empty(t, repo.updateExtraCalls)
}

// utilization 持续变化时，同一账号的被动采样写入按节流间隔合并。
func TestUpdateSessionWindow_ThrottlesChangingPassiveUsage(t *testing.T) {
	resetUnix := time.Now().Add(3 * time.Hour).Unix()
	reset7dUnix := time.Now().Add(72 * time.Hour).Unix()
	windowEnd := time.Unix(resetUnix, 0)
	account := &Account{
		ID: 502, SessionWindowEnd: &windowEnd, SessionWindowStatus: "allowed",
		Extra: map[string]any{"session_window_utilization": 0.10},
	}
	repo := &sessionWindowMockRepo{}
	svc := NewRateLimitService(repo, nil, nil, nil, nil)

	const responses = 10
	for i := 0; i < responses; i++ {
		util := fmt.Sprintf("0.%02d", 11+i)
		svc.UpdateSessionWindow(context.Background(), account, steadyOAuthSuccessHeaders(resetUnix, reset7dUnix, util, "0.3"))
	}

	t.Logf("%d changing OAuth success responses: UpdateSessionWindow=%d UpdateExtra=%d", responses, len(repo.sessionWindowCalls), len(repo.updateExtraCalls))
	require.Empty(t, repo.sessionWindowCalls)
	require.Len(t, repo.updateExtraCalls, 1, "changes inside one throttle interval must collapse into one write")
	require.Equal(t, 0.11, repo.updateExtraCalls[0].Updates["session_window_utilization"])
}

// 5h 状态变化必须立即落库，且同次被动采样不受节流限制（顺带刷新调度快照里的状态）。
func TestUpdateSessionWindow_StatusChangeBypassesThrottle(t *testing.T) {
	resetUnix := time.Now().Add(3 * time.Hour).Unix()
	reset7dUnix := time.Now().Add(72 * time.Hour).Unix()
	windowEnd := time.Unix(resetUnix, 0)
	account := &Account{
		ID: 503, SessionWindowEnd: &windowEnd, SessionWindowStatus: "allowed",
		Extra: map[string]any{"session_window_utilization": 0.10},
	}
	repo := &sessionWindowMockRepo{}
	svc := NewRateLimitService(repo, nil, nil, nil, nil)

	svc.UpdateSessionWindow(context.Background(), account, steadyOAuthSuccessHeaders(resetUnix, reset7dUnix, "0.50", "0.3"))
	require.Len(t, repo.updateExtraCalls, 1)

	warning := steadyOAuthSuccessHeaders(resetUnix, reset7dUnix, "0.91", "0.3")
	warning.Set("anthropic-ratelimit-unified-5h-status", "allowed_warning")
	svc.UpdateSessionWindow(context.Background(), account, warning)

	require.Len(t, repo.sessionWindowCalls, 1)
	require.Equal(t, "allowed_warning", repo.sessionWindowCalls[0].Status)
	require.Len(t, repo.updateExtraCalls, 2, "status change must persist passive usage immediately")
	require.Equal(t, 0.91, repo.updateExtraCalls[1].Updates["session_window_utilization"])
}

// 窗口重置（清空旧 utilization）后，新窗口的首个采样必须立即写入。
func TestUpdateSessionWindow_WindowResetBypassesThrottle(t *testing.T) {
	resetUnix := time.Now().Add(3 * time.Hour).Unix()
	reset7dUnix := time.Now().Add(72 * time.Hour).Unix()
	repo := &sessionWindowMockRepo{}
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	svc.passiveUsageThrottle.Allow(504, time.Now()) // 模拟刚写过一次

	expired := time.Now().Add(-time.Minute)
	account := &Account{ID: 504, SessionWindowEnd: &expired, SessionWindowStatus: "allowed"}
	svc.UpdateSessionWindow(context.Background(), account, steadyOAuthSuccessHeaders(resetUnix, reset7dUnix, "0.02", "0.3"))

	require.Len(t, repo.sessionWindowCalls, 1)
	require.Len(t, repo.updateExtraCalls, 2, "clear + fresh sample")
	require.Equal(t, 0.02, repo.updateExtraCalls[1].Updates["session_window_utilization"])
}
