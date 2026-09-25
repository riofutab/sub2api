//go:build unit

package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

// 模拟导出 M 页用户用量：countPages 标记哪些页会带精确 COUNT，返回实际 COUNT 次数。
// sqlmock 按顺序匹配，多发或少发 COUNT 都会让期望落空。
func runUsageExportPages(t *testing.T, pages int, skipTotalAfterFirst bool) int {
	t.Helper()
	db, mock := newSQLMock(t)
	repo := newUsageLogRepositoryWithSQL(nil, db)

	counts := 0
	for page := 1; page <= pages; page++ {
		skip := skipTotalAfterFirst && page > 1
		if !skip {
			counts++
			mock.ExpectQuery(`SELECT COUNT\(\*\) FROM usage_logs WHERE user_id = \$1`).
				WithArgs(int64(42)).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(pages * 1000)))
		}
		mock.ExpectQuery(`SELECT .* FROM usage_logs WHERE user_id = \$1 ORDER BY`).
			WillReturnRows(sqlmock.NewRows([]string{"id"}))

		_, _, err := repo.ListWithFilters(context.Background(),
			pagination.PaginationParams{Page: page, PageSize: 1000},
			UsageLogFilters{UserID: 42, SkipTotal: skip})
		require.NoError(t, err)
	}
	require.NoError(t, mock.ExpectationsWereMet())
	return counts
}

func TestUsageLogListWithFilters_SkipTotalAvoidsCountForLaterExportPages(t *testing.T) {
	const pages = 5
	before := runUsageExportPages(t, pages, false)
	after := runUsageExportPages(t, pages, true)
	t.Logf("export %d pages: COUNT queries before=%d after=%d", pages, before, after)
	require.Equal(t, pages, before)
	require.Equal(t, 1, after)
}

func TestShouldUseFastUsageLogTotal_SkipTotalOverridesExactTotal(t *testing.T) {
	require.True(t, shouldUseFastUsageLogTotal(UsageLogFilters{UserID: 1, SkipTotal: true}))
	require.True(t, shouldUseFastUsageLogTotal(UsageLogFilters{ExactTotal: true, SkipTotal: true}))
	require.False(t, shouldUseFastUsageLogTotal(UsageLogFilters{UserID: 1}))
	require.False(t, shouldUseFastUsageLogTotal(UsageLogFilters{ExactTotal: true}))
}
