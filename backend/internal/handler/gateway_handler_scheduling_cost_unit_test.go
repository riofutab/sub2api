//go:build unit

package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// schedulingCountingAccountRepo 只实现调度快照回源用到的查询，并统计回源次数。
type schedulingCountingAccountRepo struct {
	service.AccountRepository
	accounts []service.Account
	queries  atomic.Int64
}

func (r *schedulingCountingAccountRepo) filter(groupID int64, platforms []string) []service.Account {
	var out []service.Account
	for _, acc := range r.accounts {
		platformMatched := false
		for _, p := range platforms {
			if acc.Platform == p {
				platformMatched = true
				break
			}
		}
		if !platformMatched {
			continue
		}
		for _, ag := range acc.AccountGroups {
			if ag.GroupID == groupID {
				out = append(out, acc)
				break
			}
		}
	}
	return out
}

func (r *schedulingCountingAccountRepo) ListSchedulableByGroupIDAndPlatforms(_ context.Context, groupID int64, platforms []string) ([]service.Account, error) {
	r.queries.Add(1)
	return r.filter(groupID, platforms), nil
}

func (r *schedulingCountingAccountRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, groupID int64, platform string) ([]service.Account, error) {
	r.queries.Add(1)
	return r.filter(groupID, []string{platform}), nil
}

// 量化一次 Anthropic 分组正常 /v1/messages 请求（稳态，快照已预热）在选号阶段的 DB 回源次数与 Redis 往返次数。
// 分组内没有 antigravity 账号：antigravity forced 桶永远是空快照，旧实现每个请求都会回源 DB 并重建该桶。
func TestGatewayMessagesSchedulingCostWithoutAntigravityAccounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(9400)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive}
	accountRepo := &schedulingCountingAccountRepo{accounts: []service.Account{{
		ID: 9401, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 1,
		AccountGroups: []service.AccountGroup{{AccountID: 9401, GroupID: groupID}},
	}}}
	schedulerCache, redisTrips := testutil.NewCountingRedisSchedulerCache(t)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	schedulerSnapshot := service.NewSchedulerSnapshotService(schedulerCache, nil, accountRepo, nil, nil)
	gatewayService := service.NewGatewayService(
		nil, &fakeGroupRepo{group: group}, nil, nil, nil, nil, nil, nil, nil,
		schedulerSnapshot, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	billingCacheService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheService.Stop)
	h := &GatewayHandler{
		gatewayService:      gatewayService,
		billingCacheService: billingCacheService,
		concurrencyHelper:   NewConcurrencyHelper(service.NewConcurrencyService(&fakeConcurrencyCache{}), SSEPingFormatClaude, 0),
		maxAccountSwitches:  1,
		cfg:                 cfg,
	}
	apiKey := &service.APIKey{
		ID: 9402, UserID: 9403, GroupID: &groupID, Group: group, Status: service.StatusActive,
		User: &service.User{ID: 9403, Concurrency: 10, Balance: 100},
	}

	serve := func() {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		ctx := context.WithValue(context.Background(), ctxkey.Group, group)
		body := `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		c.Request = req
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})
		h.Messages(c)
		selected, ok := c.Get(opsAccountIDKey)
		require.True(t, ok, "request must reach account selection")
		require.Equal(t, int64(9401), selected)
	}

	serve() // 预热 anthropic 桶
	accountRepo.queries.Store(0)
	redisTrips.Reset()

	serve()

	dbQueries := accountRepo.queries.Load()
	trips := redisTrips.Load()
	t.Logf("steady-state anthropic request: account db queries=%d redis round trips=%d", dbQueries, trips)
	require.Zero(t, dbQueries, "a warmed-up request must not fall back to the database")
}
