//go:build integration

package repository

import (
	"context"
	"database/sql"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 保留期清理后：水位不退回（不会触发整段重建），只重算边界日，汇总金额与源表一致。
// 批次提交后、同步之前进程崩溃，也不会留下错账：读侧看到的是清理前的数，下一次同步修正。
func TestGroupUsageRollupRetentionCleanupRefreshesOnlyBoundary(t *testing.T) {
	ctx := context.Background()
	useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	db := openGroupUsageRollupSchemaDB(t, schema)

	_, err := db.ExecContext(ctx, `
		INSERT INTO groups (id) VALUES (10), (20);
		INSERT INTO users (id) VALUES (1);
		INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at) VALUES
			(1, 1, 10, 1, TIMESTAMPTZ '2026-05-01 12:00:00+08'),
			(2, 1, 10, 2, TIMESTAMPTZ '2026-05-02 06:00:00+08'),
			(3, 1, 20, 4, TIMESTAMPTZ '2026-05-02 06:30:00+08'),
			(4, 1, 10, 8, TIMESTAMPTZ '2026-05-02 18:00:00+08'),
			(5, 1, 20, 16, TIMESTAMPTZ '2026-06-10 12:00:00+08'),
			(6, 1, 10, 32, TIMESTAMPTZ '2026-08-13 12:00:00+08'),
			(7, 1, 10, 64, TIMESTAMPTZ '2026-08-14 09:00:00+08');
	`)
	require.NoError(t, err)

	fixedNow := time.Date(2026, 8, 14, 2, 0, 0, 0, time.UTC)
	todayStart := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	repo.clock = func() time.Time { return fixedNow }
	require.NoError(t, repo.SyncGroupUsageRollups(ctx, todayStart))
	requireGroupUsageTotals(t, ctx, db, todayStart, map[int64]float64{10: 107, 20: 20})

	cutoff := time.Date(2026, 5, 2, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))

	// 只跑删除批次、不跑同步，模拟批次提交后进程退出。
	require.NoError(t, repo.cleanupUsageLogsBatches(ctx, cutoff))
	closedBefore, retainedFrom := readGroupUsageRollupState(t, ctx, db)
	require.Equal(t, "2026-08-14", closedBefore, "retention cleanup must not roll the watermark back")
	require.True(t, retainedFrom.Equal(time.Unix(0, 0)), "retention cleanup marks the front as trimmed")
	requireGroupUsageTotals(t, ctx, db, todayStart, map[int64]float64{10: 107, 20: 20})

	require.NoError(t, repo.SyncGroupUsageRollups(ctx, todayStart))
	closedBefore, retainedFrom = readGroupUsageRollupState(t, ctx, db)
	require.Equal(t, "2026-08-14", closedBefore)
	require.True(t, retainedFrom.Equal(time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)))
	requireGroupUsageTotals(t, ctx, db, todayStart, map[int64]float64{10: 104, 20: 16})

	var boundaryG10 float64
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT actual_cost FROM usage_group_daily_rollups
		WHERE bucket_date = DATE '2026-05-02' AND group_id = 10
	`).Scan(&boundaryG10))
	require.InDelta(t, 8, boundaryG10, 0.0000001)
	var staleBuckets int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM usage_group_daily_rollups
		WHERE bucket_date < DATE '2026-05-02'
			OR (bucket_date = DATE '2026-05-02' AND group_id = 20)
	`).Scan(&staleBuckets))
	require.Zero(t, staleBuckets)

	// 完整入口（清理 + 同步）再跑一次是幂等的。
	require.NoError(t, repo.CleanupUsageLogs(ctx, cutoff))
	requireGroupUsageTotals(t, ctx, db, todayStart, map[int64]float64{10: 104, 20: 16})
}

func openGroupUsageRollupSchemaDB(t *testing.T, schema string) *sql.DB {
	t.Helper()
	u, err := url.Parse(integrationDSN)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func readGroupUsageRollupState(t *testing.T, ctx context.Context, db *sql.DB) (string, time.Time) {
	t.Helper()
	var closedBefore string
	var retainedFrom time.Time
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT closed_before::text, retained_from FROM usage_group_rollup_state WHERE id = 1
	`).Scan(&closedBefore, &retainedFrom))
	return closedBefore, retainedFrom
}

func requireGroupUsageTotals(t *testing.T, ctx context.Context, db *sql.DB, todayStart time.Time, want map[int64]float64) {
	t.Helper()
	result, err := newUsageLogRepositoryWithSQL(nil, db).GetAllGroupUsageSummary(ctx, todayStart)
	require.NoError(t, err)
	require.Len(t, result, len(want))
	for _, row := range result {
		require.InDelta(t, want[row.GroupID], row.TotalCost, 0.0000001, "group %d", row.GroupID)
	}
}
