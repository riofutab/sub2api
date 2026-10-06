package dto

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountFromServiceShallow_ExposesOpenAITLSFingerprint(t *testing.T) {
	account := &service.Account{
		ID:       1,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Extra:    map[string]any{"enable_tls_fingerprint": true, "tls_fingerprint_profile_id": float64(7)},
	}

	out := AccountFromServiceShallow(account)

	require.NotNil(t, out.EnableTLSFingerprint)
	require.True(t, *out.EnableTLSFingerprint)
	require.NotNil(t, out.TLSFingerprintProfileID)
	require.Equal(t, int64(7), *out.TLSFingerprintProfileID)
}

func TestAccountFromServiceShallow_OmitsTLSFingerprintForOpenAIAPIKey(t *testing.T) {
	account := &service.Account{
		ID:       2,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Extra:    map[string]any{"enable_tls_fingerprint": true, "tls_fingerprint_profile_id": float64(7)},
	}

	out := AccountFromServiceShallow(account)

	require.Nil(t, out.EnableTLSFingerprint)
	require.Nil(t, out.TLSFingerprintProfileID)
}
