//go:build unit

package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func syncMapLen(m *sync.Map) int {
	n := 0
	m.Range(func(any, any) bool {
		n++
		return true
	})
	return n
}

// 过期的会话续链绑定只靠读侧惰性删除，永远不再被读到的会话会一直留在内存里；写入侧的机会式清扫要把它们清掉。
func TestOpenAICompatSessionResponses_SweepsExpiredBindingsOnWrite(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 1}
	const staleSessions = 1000
	for i := 0; i < staleSessions; i++ {
		svc.openaiCompatSessionResponses.Store(fmt.Sprintf("stale-%d", i), openAICompatSessionResponseBinding{
			ResponseID: "resp_old",
			ExpiresAt:  time.Now().Add(-time.Minute),
		})
	}
	svc.openaiCompatSessionResponses.Store("never-expires", openAICompatSessionResponseBinding{ResponseID: "resp_keep"})

	for i := 0; i < openAISessionMapSweepEvery; i++ {
		svc.bindOpenAICompatSessionResponseID(context.Background(), nil, account, fmt.Sprintf("live-%d", i), "resp_new")
	}

	remaining := syncMapLen(&svc.openaiCompatSessionResponses)
	t.Logf("session response map after %d writes: %d entries (stale=%d)", openAISessionMapSweepEvery, remaining, staleSessions)
	require.Equal(t, openAISessionMapSweepEvery+1, remaining, "expired bindings must be swept, live and non-expiring ones kept")
	_, kept := svc.openaiCompatSessionResponses.Load("never-expires")
	require.True(t, kept)
}

func TestOpenAICompatAnthropicDigestSessions_SweepsExpiredBindingsOnWrite(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 1}
	const staleSessions = 1000
	for i := 0; i < staleSessions; i++ {
		svc.openaiCompatAnthropicDigestSessions.Store(fmt.Sprintf("1|0|stale-%d", i), openAICompatAnthropicDigestBinding{
			PromptCacheKey: "old",
			ExpiresAt:      time.Now().Add(-time.Minute),
		})
	}

	for i := 0; i < openAISessionMapSweepEvery; i++ {
		svc.bindOpenAICompatAnthropicDigestPromptCacheKey(account, 0, fmt.Sprintf("u:%d", i), "key", "")
	}

	remaining := syncMapLen(&svc.openaiCompatAnthropicDigestSessions)
	t.Logf("digest session map after %d writes: %d entries (stale=%d)", openAISessionMapSweepEvery, remaining, staleSessions)
	require.Equal(t, openAISessionMapSweepEvery, remaining)
}
