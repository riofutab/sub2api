//go:build unit

package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserUsageListSkipTotalParam(t *testing.T) {
	repo := &userUsageRepoCapture{}
	router := newUserUsageRequestTypeTestRouter(repo)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/usage?page=2&page_size=1000&skip_total=true", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, repo.listFilters.SkipTotal)
	require.Equal(t, 1000, repo.listParams.PageSize)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/usage", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.False(t, repo.listFilters.SkipTotal)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/usage?skip_total=maybe", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
