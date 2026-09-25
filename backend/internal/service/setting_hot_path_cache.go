package service

import (
	"context"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// 网关请求热路径上读取的设置（Anthropic beta 策略、OpenAI fast 策略、antigravity 身份补丁）
// 进程内缓存。TTL 与 codex_cli_only 策略缓存一致；本进程写入时主动失效，其它实例靠 TTL 收敛。
const (
	hotPathSettingCacheTTL       = 60 * time.Second
	hotPathSettingDBTimeout      = 5 * time.Second
	hotPathSettingSingleflightID = "load"
)

type hotPathSettingEntry[T any] struct {
	value     T
	expiresAt int64 // unix nano
}

// hotPathSettingCache 是 atomic.Value + TTL + singleflight 的通用实现，零值可用，不可复制。
type hotPathSettingCache[T any] struct {
	entry atomic.Value // *hotPathSettingEntry[T]
	sf    singleflight.Group
}

func (c *hotPathSettingCache[T]) fresh() (T, bool) {
	if e, ok := c.entry.Load().(*hotPathSettingEntry[T]); ok && e != nil && time.Now().UnixNano() < e.expiresAt {
		return e.value, true
	}
	var zero T
	return zero, false
}

// get 命中未过期缓存直接返回；否则经 singleflight 回源。load 返回 cacheable=false（如 DB 故障）时
// 结果只返回给本轮调用方，不写入缓存，下次继续回源。
func (c *hotPathSettingCache[T]) get(load func() (T, bool)) T {
	if v, ok := c.fresh(); ok {
		return v
	}
	result, _, _ := c.sf.Do(hotPathSettingSingleflightID, func() (any, error) {
		if v, ok := c.fresh(); ok {
			return v, nil
		}
		v, cacheable := load()
		if cacheable {
			c.entry.Store(&hotPathSettingEntry[T]{value: v, expiresAt: time.Now().Add(hotPathSettingCacheTTL).UnixNano()})
		}
		return v, nil
	})
	v, _ := result.(T)
	return v
}

func (c *hotPathSettingCache[T]) invalidate() {
	c.sf.Forget(hotPathSettingSingleflightID)
	c.entry.Store((*hotPathSettingEntry[T])(nil))
}

// hotPathSettingDBContext 回源查询与发起请求的生命周期解耦：singleflight 的结果会共享给其它等待者，
// 不能因为首个调用方取消而让所有人拿到错误。
func hotPathSettingDBContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), hotPathSettingDBTimeout)
}

type betaPolicySettingsResult struct {
	settings *BetaPolicySettings
	err      error
}

type openAIFastPolicySettingsResult struct {
	settings *OpenAIFastPolicySettings
	err      error
}

type identityPatchSettings struct {
	enabled bool
	prompt  string
}
