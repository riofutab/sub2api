//go:build unit

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayModelAvailability_RespectsGroupModelAllowlist(t *testing.T) {
	for _, tt := range []struct {
		name      string
		allowlist service.GroupModelAllowlist
		available bool
	}{
		{name: "disabled", available: true},
		{
			name:      "exact match",
			allowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-ready", "gpt-unknown"}},
			available: true,
		},
		{
			name:      "wildcard match",
			allowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-*"}},
			available: true,
		},
		{
			name:      "blocked model",
			allowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-other"}},
		},
		{
			name:      "empty enabled allowlist",
			allowlist: service.GroupModelAllowlist{Enabled: true},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			groupID := int64(24)
			h := newGatewayModelsHandlerForTest(&gatewayModelsAccountRepoStub{
				byGroup: map[int64][]service.Account{
					groupID: {{
						ID:          1,
						Platform:    service.PlatformOpenAI,
						Status:      service.StatusActive,
						Schedulable: true,
						Credentials: map[string]any{
							"model_mapping": map[string]any{"gpt-ready": "gpt-ready"},
						},
					}},
				},
			})
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/models/availability",
				strings.NewReader(`{"models":["gpt-ready","gpt-unknown"]}`))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
				GroupID: &groupID,
				Group: &service.Group{
					ID: groupID, Platform: service.PlatformOpenAI, ModelAllowlist: tt.allowlist,
				},
			})

			h.ModelAvailability(c)

			require.Equal(t, http.StatusOK, rec.Code)
			var got struct {
				Data []service.ModelAvailability `json:"data"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			require.Equal(t, []service.ModelAvailability{
				{Model: "gpt-ready", Available: tt.available},
				{Model: "gpt-unknown", Available: false},
			}, got.Data)
		})
	}
}
