//go:build unit

package testutil

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// RedisRoundTrips 统计 go-redis 客户端发往服务端的往返次数：单条命令记 1 次，pipeline/事务整体记 1 次。
type RedisRoundTrips struct {
	count atomic.Int64
}

func (r *RedisRoundTrips) Load() int64 { return r.count.Load() }
func (r *RedisRoundTrips) Reset()      { r.count.Store(0) }

type roundTripCountingHook struct {
	counter *RedisRoundTrips
}

func (h roundTripCountingHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return next(ctx, network, addr)
	}
}

func (h roundTripCountingHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.counter.count.Add(1)
		return next(ctx, cmd)
	}
}

func (h roundTripCountingHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.counter.count.Add(1)
		return next(ctx, cmds)
	}
}

// NewCountingRedisClient 返回连到 miniredis 的客户端和它的往返计数器。
func NewCountingRedisClient(t *testing.T) (*redis.Client, *RedisRoundTrips) {
	t.Helper()
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	counter := &RedisRoundTrips{}
	client.AddHook(roundTripCountingHook{counter: counter})
	return client, counter
}

// NewCountingRedisSchedulerCache 返回基于 miniredis 的真实调度快照缓存及其往返计数器，
// 供不能直接 import repository/go-redis 的 handler/service 测试量化 Redis 开销。
func NewCountingRedisSchedulerCache(t *testing.T) (service.SchedulerCache, *RedisRoundTrips) {
	t.Helper()
	client, counter := NewCountingRedisClient(t)
	return repository.NewSchedulerCache(client), counter
}
