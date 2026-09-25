//go:build unit

package service

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dashboardCountingUsageRepo struct {
	UsageLogRepository
	release     chan struct{}
	apiKeyCalls atomic.Int64
	userCalls   atomic.Int64
}

func (r *dashboardCountingUsageRepo) GetAPIKeyDashboardStats(_ context.Context, apiKeyID int64) (*usagestats.UserDashboardStats, error) {
	r.apiKeyCalls.Add(1)
	if r.release != nil {
		<-r.release
	}
	return &usagestats.UserDashboardStats{TotalRequests: apiKeyID, TodayCost: 1.25}, nil
}

func (r *dashboardCountingUsageRepo) GetUserDashboardStats(_ context.Context, userID int64) (*usagestats.UserDashboardStats, error) {
	r.userCalls.Add(1)
	return &usagestats.UserDashboardStats{TotalAPIKeys: userID}, nil
}

type memoryUsageDashboardCache struct {
	mu   sync.Mutex
	data map[string]string
	ttls map[string]time.Duration
}

func newMemoryUsageDashboardCache() *memoryUsageDashboardCache {
	return &memoryUsageDashboardCache{data: map[string]string{}, ttls: map[string]time.Duration{}}
}

func (c *memoryUsageDashboardCache) key(scope string, id int64) string {
	return scope + ":" + strconv.FormatInt(id, 10)
}

func (c *memoryUsageDashboardCache) GetUsageDashboardStats(_ context.Context, scope string, id int64) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.data[c.key(scope, id)]
	if !ok {
		return "", ErrUsageDashboardStatsCacheMiss
	}
	return v, nil
}

func (c *memoryUsageDashboardCache) SetUsageDashboardStats(_ context.Context, scope string, id int64, data string, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[c.key(scope, id)] = data
	c.ttls[c.key(scope, id)] = ttl
	return nil
}

// /v1/usage 连续两次调用：第二次应命中缓存，不再聚合 usage_logs。
func TestUsageServiceDashboardStats_SecondCallServedFromCache(t *testing.T) {
	repo := &dashboardCountingUsageRepo{}
	cache := newMemoryUsageDashboardCache()
	svc := NewUsageService(repo, nil, nil, nil, cache)

	first, err := svc.GetAPIKeyDashboardStats(context.Background(), 7)
	require.NoError(t, err)
	second, err := svc.GetAPIKeyDashboardStats(context.Background(), 7)
	require.NoError(t, err)

	t.Logf("two consecutive /v1/usage calls: api key dashboard aggregations=%d", repo.apiKeyCalls.Load())
	require.Equal(t, int64(1), repo.apiKeyCalls.Load())
	require.Equal(t, first, second)
	require.Equal(t, int64(7), second.TotalRequests)
	require.Equal(t, usageDashboardStatsCacheTTL, cache.ttls[cache.key(usageDashboardScopeAPIKey, 7)])

	_, err = svc.GetUserDashboardStats(context.Background(), 9)
	require.NoError(t, err)
	userStats, err := svc.GetUserDashboardStats(context.Background(), 9)
	require.NoError(t, err)
	require.Equal(t, int64(1), repo.userCalls.Load())
	require.Equal(t, int64(9), userStats.TotalAPIKeys)
}

// 没有缓存或缓存未命中时，同一 key 的并发请求合并成一次聚合。
func TestUsageServiceDashboardStats_ConcurrentMissesShareOneAggregation(t *testing.T) {
	repo := &dashboardCountingUsageRepo{release: make(chan struct{})}
	svc := NewUsageService(repo, nil, nil, nil, nil)

	const concurrency = 10
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stats, err := svc.GetAPIKeyDashboardStats(context.Background(), 3)
			if assert.NoError(t, err) {
				assert.Equal(t, int64(3), stats.TotalRequests)
			}
		}()
	}
	require.Eventually(t, func() bool { return repo.apiKeyCalls.Load() >= 1 }, 2*time.Second, time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	close(repo.release)
	wg.Wait()

	require.Equal(t, int64(1), repo.apiKeyCalls.Load())
}
