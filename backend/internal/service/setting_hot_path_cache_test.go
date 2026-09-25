//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSettingHotPathReads_HitDatabaseOncePerTTL(t *testing.T) {
	repo := newCountingSettingRepoStub(map[string]string{
		SettingKeyBetaPolicySettings:       `{"rules":[{"beta_token":"t1","action":"filter","scope":"all"}]}`,
		SettingKeyOpenAIFastPolicySettings: `{"rules":[{"service_tier":"priority","action":"block","scope":"all"}]}`,
		SettingKeyEnableIdentityPatch:      "false",
		SettingKeyIdentityPatchPrompt:      "custom",
	})
	svc := NewSettingService(repo, &config.Config{})
	ctx := context.Background()

	readHotPathSettings(ctx, svc)
	firstRound := repo.getCalls.Load()
	for i := 0; i < 10; i++ {
		readHotPathSettings(ctx, svc)
	}
	require.Equal(t, firstRound, repo.getCalls.Load(), "TTL 内重复读取不应再访问 DB")
	// beta + fast 各 1 次，身份补丁两个键各 1 次。
	require.Equal(t, int64(4), firstRound)

	beta, err := svc.GetBetaPolicySettings(ctx)
	require.NoError(t, err)
	require.Len(t, beta.Rules, 1)
	require.Equal(t, "t1", beta.Rules[0].BetaToken)
	fast, err := svc.GetOpenAIFastPolicySettings(ctx)
	require.NoError(t, err)
	require.Equal(t, BetaPolicyActionBlock, fast.Rules[0].Action)
	require.False(t, svc.IsIdentityPatchEnabled(ctx))
	require.Equal(t, "custom", svc.GetIdentityPatchPrompt(ctx))
}

func TestSettingHotPathReads_DefaultsWhenMissing(t *testing.T) {
	svc := NewSettingService(newCountingSettingRepoStub(nil), &config.Config{})
	ctx := context.Background()

	beta, err := svc.GetBetaPolicySettings(ctx)
	require.NoError(t, err)
	require.Equal(t, DefaultBetaPolicySettings(), beta)
	fast, err := svc.GetOpenAIFastPolicySettings(ctx)
	require.NoError(t, err)
	require.Equal(t, DefaultOpenAIFastPolicySettings(), fast)
	require.True(t, svc.IsIdentityPatchEnabled(ctx), "缺省时身份补丁默认开启")
	require.Empty(t, svc.GetIdentityPatchPrompt(ctx))
}

func TestSetBetaPolicySettings_InvalidatesCache(t *testing.T) {
	repo := newCountingSettingRepoStub(map[string]string{
		SettingKeyBetaPolicySettings: `{"rules":[{"beta_token":"old","action":"filter","scope":"all"}]}`,
	})
	svc := NewSettingService(repo, &config.Config{})
	ctx := context.Background()

	before, err := svc.GetBetaPolicySettings(ctx)
	require.NoError(t, err)
	require.Equal(t, "old", before.Rules[0].BetaToken)

	require.NoError(t, svc.SetBetaPolicySettings(ctx, &BetaPolicySettings{Rules: []BetaPolicyRule{{BetaToken: "new", Action: BetaPolicyActionBlock, Scope: BetaPolicyScopeAll}}}))
	after, err := svc.GetBetaPolicySettings(ctx)
	require.NoError(t, err)
	require.Equal(t, "new", after.Rules[0].BetaToken)
}

func TestSetOpenAIFastPolicySettings_InvalidatesCache(t *testing.T) {
	repo := newCountingSettingRepoStub(nil)
	svc := NewSettingService(repo, &config.Config{})
	ctx := context.Background()

	before, err := svc.GetOpenAIFastPolicySettings(ctx)
	require.NoError(t, err)
	require.Empty(t, before.Rules)

	require.NoError(t, svc.SetOpenAIFastPolicySettings(ctx, &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{{ServiceTier: OpenAIFastTierPriority, Action: BetaPolicyActionFilter, Scope: BetaPolicyScopeAll}}}))
	after, err := svc.GetOpenAIFastPolicySettings(ctx)
	require.NoError(t, err)
	require.Len(t, after.Rules, 1)
	require.Equal(t, BetaPolicyActionFilter, after.Rules[0].Action)
}

func TestUpdateSettings_InvalidatesIdentityPatchCache(t *testing.T) {
	repo := newCountingSettingRepoStub(map[string]string{
		SettingKeyEnableIdentityPatch: "true",
		SettingKeyIdentityPatchPrompt: "old",
	})
	svc := NewSettingService(repo, &config.Config{})
	ctx := context.Background()

	require.True(t, svc.IsIdentityPatchEnabled(ctx))
	require.Equal(t, "old", svc.GetIdentityPatchPrompt(ctx))

	require.NoError(t, svc.UpdateSettings(ctx, &SystemSettings{EnableIdentityPatch: false, IdentityPatchPrompt: "new"}))
	require.False(t, svc.IsIdentityPatchEnabled(ctx))
	require.Equal(t, "new", svc.GetIdentityPatchPrompt(ctx))
}

// failingSettingRepoStub 模拟 DB 故障：GetValue 一律返回非 NotFound 错误。
type failingSettingRepoStub struct {
	*countingSettingRepoStub
}

func (s *failingSettingRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	s.getCalls.Add(1)
	return "", errors.New("db down")
}

func TestSettingHotPathReads_DatabaseErrorsAreNotCached(t *testing.T) {
	repo := &failingSettingRepoStub{countingSettingRepoStub: newCountingSettingRepoStub(nil)}
	svc := NewSettingService(repo, &config.Config{})
	ctx := context.Background()

	_, err := svc.GetBetaPolicySettings(ctx)
	require.Error(t, err)
	_, err = svc.GetOpenAIFastPolicySettings(ctx)
	require.Error(t, err)
	require.True(t, svc.IsIdentityPatchEnabled(ctx), "读取失败时保持默认开启")
	require.Empty(t, svc.GetIdentityPatchPrompt(ctx))
	first := repo.getCalls.Load()

	_, err = svc.GetBetaPolicySettings(ctx)
	require.Error(t, err)
	_, _ = svc.GetOpenAIFastPolicySettings(ctx)
	_ = svc.IsIdentityPatchEnabled(ctx)
	require.Greater(t, repo.getCalls.Load(), first, "DB 错误不缓存，下次继续回源")
}
