//go:build unit

package repository

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestSessionLimitCacheSetWindowCostBatchWritesAllKeysWithJitteredTTL(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewSessionLimitCache(client, 5)
	ctx := context.Background()

	require.NoError(t, cache.SetWindowCostBatch(ctx, map[int64]float64{1: 1.5, 2: 0, 3: 42.25}))

	got, err := cache.GetWindowCostBatch(ctx, []int64{1, 2, 3, 4})
	require.NoError(t, err)
	require.Equal(t, map[int64]float64{1: 1.5, 2: 0, 3: 42.25}, got)
	firstTTL := redisServer.TTL(windowCostKey(1))
	require.GreaterOrEqual(t, firstTTL, windowCostCacheTTL)
	require.Less(t, firstTTL, windowCostCacheTTL+windowCostCacheTTLJitter)
	for _, id := range []int64{2, 3} {
		require.Equal(t, firstTTL, redisServer.TTL(windowCostKey(id)), "one batch shares one TTL")
	}

	require.NoError(t, cache.SetWindowCostBatch(ctx, nil))
}
