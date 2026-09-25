package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

// usageDashboardStatsCacheTTL 是用户/API Key 仪表盘汇总的缓存时长，也是 /v1/usage 与用户仪表盘数据的最大滞后。
const usageDashboardStatsCacheTTL = 30 * time.Second

const (
	usageDashboardScopeUser   = "user"
	usageDashboardScopeAPIKey = "api_key"
)

// ErrUsageDashboardStatsCacheMiss 标记用户/API Key 仪表盘缓存未命中。
var ErrUsageDashboardStatsCacheMiss = errors.New("usage dashboard stats cache miss")

// UsageDashboardStatsCache 缓存按用户或 API Key 聚合的仪表盘汇总（整个保留期的累计 + 今日）。
type UsageDashboardStatsCache interface {
	GetUsageDashboardStats(ctx context.Context, scope string, id int64) (string, error)
	SetUsageDashboardStats(ctx context.Context, scope string, id int64, data string, ttl time.Duration) error
}

// cachedUsageDashboardStats 先读缓存，未命中时用 singleflight 合并同一 scope+id 的并发聚合，再回写缓存。
// 缓存读写失败只记日志并回退到直接聚合。返回的指针可能被并发调用方共享，调用方只读。
func (s *UsageService) cachedUsageDashboardStats(
	ctx context.Context,
	scope string,
	id int64,
	load func(ctx context.Context) (*usagestats.UserDashboardStats, error),
) (*usagestats.UserDashboardStats, error) {
	if s.dashboardCache != nil {
		raw, err := s.dashboardCache.GetUsageDashboardStats(ctx, scope, id)
		if err == nil {
			var stats usagestats.UserDashboardStats
			jsonErr := json.Unmarshal([]byte(raw), &stats)
			if jsonErr == nil {
				return &stats, nil
			}
			logger.LegacyPrintf("service.usage", "usage dashboard cache decode failed: scope=%s id=%d err=%v", scope, id, jsonErr)
		} else if !errors.Is(err, ErrUsageDashboardStatsCacheMiss) {
			logger.LegacyPrintf("service.usage", "usage dashboard cache read failed: scope=%s id=%d err=%v", scope, id, err)
		}
	}

	ch := s.dashboardSF.DoChan(scope+":"+strconv.FormatInt(id, 10), func() (any, error) {
		loadCtx := context.WithoutCancel(ctx)
		stats, err := load(loadCtx)
		if err != nil {
			return nil, err
		}
		if s.dashboardCache != nil && stats != nil {
			if payload, marshalErr := json.Marshal(stats); marshalErr == nil {
				if setErr := s.dashboardCache.SetUsageDashboardStats(loadCtx, scope, id, string(payload), usageDashboardStatsCacheTTL); setErr != nil {
					logger.LegacyPrintf("service.usage", "usage dashboard cache write failed: scope=%s id=%d err=%v", scope, id, setErr)
				}
			}
		}
		return stats, nil
	})
	select {
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		stats, _ := res.Val.(*usagestats.UserDashboardStats)
		return stats, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
