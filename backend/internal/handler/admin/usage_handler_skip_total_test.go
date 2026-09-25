//go:build unit

package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminUsageListSkipTotalParam(t *testing.T) {
	repo := &adminUsageRepoCapture{}
	router := newAdminUsageRequestTypeTestRouter(repo)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/usage?page=3&skip_total=true", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, repo.listFilters.SkipTotal)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/usage?exact_total=true", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.False(t, repo.listFilters.SkipTotal)
	require.True(t, repo.listFilters.ExactTotal)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/usage?skip_total=nope", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
