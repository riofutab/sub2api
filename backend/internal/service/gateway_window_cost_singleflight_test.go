//go:build unit

package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

// blockingWindowCostRepo 的批量聚合在 release 关闭前阻塞，模拟慢 SQL，让并发请求在同一时刻都处于未命中状态。
type blockingWindowCostRepo struct {
	UsageLogRepository
	release    chan struct{}
	batchCalls atomic.Int64
}

func (r *blockingWindowCostRepo) GetAccountWindowStatsBatch(_ context.Context, accountIDs []int64, _ time.Time) (map[int64]*usagestats.AccountStats, error) {
	r.batchCalls.Add(1)
	<-r.release
	out := make(map[int64]*usagestats.AccountStats, len(accountIDs))
	for _, id := range accountIDs {
		out[id] = &usagestats.AccountStats{StandardCost: float64(id)}
	}
	return out, nil
}

func (r *blockingWindowCostRepo) GetAccountWindowStats(context.Context, int64, time.Time) (*usagestats.AccountStats, error) {
	panic("batch path must be used")
}

// countingWindowCostCache 永远未命中，统计读取次数与回写往返次数（单条 SET 与批量写各记一次）。
type countingWindowCostCache struct {
	SessionLimitCache
	reads  atomic.Int64
	writes atomic.Int64
	mu     sync.Mutex
	stored map[int64]float64
}

func (c *countingWindowCostCache) GetWindowCostBatch(context.Context, []int64) (map[int64]float64, error) {
	c.reads.Add(1)
	return map[int64]float64{}, nil
}

func (c *countingWindowCostCache) SetWindowCost(_ context.Context, accountID int64, cost float64) error {
	c.writes.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stored == nil {
		c.stored = map[int64]float64{}
	}
	c.stored[accountID] = cost
	return nil
}

func (c *countingWindowCostCache) SetWindowCostBatch(_ context.Context, costs map[int64]float64) error {
	c.writes.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stored == nil {
		c.stored = map[int64]float64{}
	}
	for id, cost := range costs {
		c.stored[id] = cost
	}
	return nil
}

// 缓存同时过期时，同一分组同一窗口的 N 个并发请求只应触发一次聚合查询和一次批量回写。
func TestWithWindowCostPrefetch_ConcurrentMissesShareOneQuery(t *testing.T) {
	resetGatewayHotpathStatsForTest()
	windowStart := time.Now().Add(-30 * time.Minute).Truncate(time.Hour)
	windowEnd := windowStart.Add(5 * time.Hour)
	const accountCount = 5
	accounts := make([]Account, 0, accountCount)
	for i := int64(1); i <= accountCount; i++ {
		accounts = append(accounts, Account{
			ID: i, Platform: PlatformAnthropic, Type: AccountTypeOAuth,
			Extra:              map[string]any{"window_cost_limit": 100.0},
			SessionWindowStart: &windowStart, SessionWindowEnd: &windowEnd,
		})
	}
	repo := &blockingWindowCostRepo{release: make(chan struct{})}
	cache := &countingWindowCostCache{}
	svc := &GatewayService{sessionLimitCache: cache, usageLogRepo: repo}

	const concurrency = 20
	results := make([]context.Context, concurrency)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = svc.withWindowCostPrefetch(context.Background(), accounts)
		}(i)
	}
	require.Eventually(t, func() bool { return cache.reads.Load() == concurrency }, 2*time.Second, time.Millisecond)
	time.Sleep(50 * time.Millisecond) // 让所有请求走到聚合查询入口
	close(repo.release)
	wg.Wait()

	t.Logf("%d concurrent misses: aggregate queries=%d cache write round trips=%d", concurrency, repo.batchCalls.Load(), cache.writes.Load())
	require.Equal(t, int64(1), repo.batchCalls.Load())
	require.Equal(t, int64(1), cache.writes.Load())
	for _, ctx := range results {
		for i := int64(1); i <= accountCount; i++ {
			cost, ok := windowCostFromPrefetchContext(ctx, i)
			require.True(t, ok)
			require.Equal(t, float64(i), cost)
		}
	}
}
