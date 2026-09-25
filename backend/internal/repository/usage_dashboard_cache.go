package repository

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// 格式: usage_dashboard:v1:{scope}:{id}
const usageDashboardStatsKeyPrefix = "usage_dashboard:v1:"

type usageDashboardStatsCache struct {
	rdb *redis.Client
}

func NewUsageDashboardStatsCache(rdb *redis.Client) service.UsageDashboardStatsCache {
	return &usageDashboardStatsCache{rdb: rdb}
}

func usageDashboardStatsKey(scope string, id int64) string {
	return usageDashboardStatsKeyPrefix + scope + ":" + strconv.FormatInt(id, 10)
}

func (c *usageDashboardStatsCache) GetUsageDashboardStats(ctx context.Context, scope string, id int64) (string, error) {
	val, err := c.rdb.Get(ctx, usageDashboardStatsKey(scope, id)).Result()
	if errors.Is(err, redis.Nil) {
		return "", service.ErrUsageDashboardStatsCacheMiss
	}
	if err != nil {
		return "", err
	}
	return val, nil
}

func (c *usageDashboardStatsCache) SetUsageDashboardStats(ctx context.Context, scope string, id int64, data string, ttl time.Duration) error {
	return c.rdb.Set(ctx, usageDashboardStatsKey(scope, id), data, ttl).Err()
}
