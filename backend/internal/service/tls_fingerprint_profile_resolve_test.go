//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func newResolveTestProfileService() *TLSFingerprintProfileService {
	svc := &TLSFingerprintProfileService{}
	svc.setLocalCache([]*model.TLSFingerprintProfile{{ID: 7, Name: "admin node profile"}})
	return svc
}

func tlsTestAccount(platform, accountType string, profileID any) *Account {
	extra := map[string]any{"enable_tls_fingerprint": true}
	if profileID != nil {
		extra["tls_fingerprint_profile_id"] = profileID
	}
	return &Account{ID: 1, Platform: platform, Type: accountType, Extra: extra}
}

func TestResolveTLSProfile_OpenAIRandomUsesCodexBuiltin(t *testing.T) {
	svc := newResolveTestProfileService()

	profile := svc.ResolveTLSProfile(tlsTestAccount(PlatformOpenAI, AccountTypeOAuth, -1))

	require.NotNil(t, profile)
	require.Equal(t, tlsfingerprint.CodexProfileName, profile.Name)
}

func TestResolveTLSProfile_OpenAIExplicitBindingIsHonored(t *testing.T) {
	svc := newResolveTestProfileService()

	profile := svc.ResolveTLSProfile(tlsTestAccount(PlatformOpenAI, AccountTypeOAuth, 7))

	require.NotNil(t, profile)
	require.Equal(t, "admin node profile", profile.Name)
}

func TestResolveTLSProfile_OpenAIUnboundUsesCodexBuiltin(t *testing.T) {
	svc := newResolveTestProfileService()

	profile := svc.ResolveTLSProfile(tlsTestAccount(PlatformOpenAI, AccountTypeOAuth, nil))

	require.NotNil(t, profile)
	require.Equal(t, tlsfingerprint.CodexProfileName, profile.Name)
}

func TestResolveTLSProfile_AnthropicRandomStillPicksAdminProfile(t *testing.T) {
	svc := newResolveTestProfileService()

	profile := svc.ResolveTLSProfile(tlsTestAccount(PlatformAnthropic, AccountTypeOAuth, -1))

	require.NotNil(t, profile)
	require.Equal(t, "admin node profile", profile.Name)
}

func TestResolveTLSProfile_OpenAIAPIKeyStaysDisabled(t *testing.T) {
	svc := newResolveTestProfileService()

	require.Nil(t, svc.ResolveTLSProfile(tlsTestAccount(PlatformOpenAI, AccountTypeAPIKey, nil)))
}
