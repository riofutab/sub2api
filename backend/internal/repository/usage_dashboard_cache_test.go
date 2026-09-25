//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestUsageDashboardStatsCacheRoundTripAndMiss(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewUsageDashboardStatsCache(client)
	ctx := context.Background()

	_, err := cache.GetUsageDashboardStats(ctx, "api_key", 7)
	require.ErrorIs(t, err, service.ErrUsageDashboardStatsCacheMiss)

	require.NoError(t, cache.SetUsageDashboardStats(ctx, "api_key", 7, `{"total_requests":3}`, 30*time.Second))
	got, err := cache.GetUsageDashboardStats(ctx, "api_key", 7)
	require.NoError(t, err)
	require.Equal(t, `{"total_requests":3}`, got)
	require.Equal(t, 30*time.Second, redisServer.TTL("usage_dashboard:v1:api_key:7"))

	_, err = cache.GetUsageDashboardStats(ctx, "user", 7)
	require.ErrorIs(t, err, service.ErrUsageDashboardStatsCacheMiss, "scopes must not collide")
}
