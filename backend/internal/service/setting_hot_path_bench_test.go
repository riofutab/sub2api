//go:build unit

package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// countingSettingRepoStub 统计 GetValue 调用次数，用于断言热路径设置读取的 DB 访问次数。
type countingSettingRepoStub struct {
	mu       sync.Mutex
	values   map[string]string
	getCalls atomic.Int64
}

func newCountingSettingRepoStub(values map[string]string) *countingSettingRepoStub {
	if values == nil {
		values = map[string]string{}
	}
	return &countingSettingRepoStub{values: values}
}

func (s *countingSettingRepoStub) Get(ctx context.Context, key string) (*Setting, error) {
	panic("unused")
}

func (s *countingSettingRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	s.getCalls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", ErrSettingNotFound
}

func (s *countingSettingRepoStub) Set(ctx context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *countingSettingRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	s.getCalls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if v, ok := s.values[key]; ok {
			out[key] = v
		}
	}
	return out, nil
}

func (s *countingSettingRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range settings {
		s.values[k] = v
	}
	return nil
}

func (s *countingSettingRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	panic("unused")
}

func (s *countingSettingRepoStub) Delete(ctx context.Context, key string) error {
	panic("unused")
}

// readHotPathSettings 模拟一次网关请求在热路径上读取的设置
// （Anthropic beta 策略、antigravity 身份补丁、OpenAI fast 策略）。
func readHotPathSettings(ctx context.Context, svc *SettingService) {
	_, _ = svc.GetBetaPolicySettings(ctx)
	_ = svc.IsIdentityPatchEnabled(ctx)
	_ = svc.GetIdentityPatchPrompt(ctx)
	_, _ = svc.GetOpenAIFastPolicySettings(ctx)
}

func BenchmarkSettingHotPathReads(b *testing.B) {
	repo := newCountingSettingRepoStub(map[string]string{
		SettingKeyBetaPolicySettings:       `{"rules":[{"beta_token":"context-1m-2025-08-07","action":"filter","scope":"oauth"}]}`,
		SettingKeyOpenAIFastPolicySettings: `{"rules":[{"service_tier":"priority","action":"filter","scope":"all"}]}`,
		SettingKeyEnableIdentityPatch:      "true",
		SettingKeyIdentityPatchPrompt:      "custom prompt",
	})
	svc := NewSettingService(repo, &config.Config{})
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		readHotPathSettings(ctx, svc)
	}
	b.StopTimer()
	b.ReportMetric(float64(repo.getCalls.Load())/float64(b.N), "dbcalls/op")
}
