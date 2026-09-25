//go:build unit

package repository

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 批量插入耗尽超时后，逐行兜底必须拿到新的超时，否则兜底必然全部失败、用量记录丢失。
func TestFlushBestEffortBatch_SingleFallbackGetsFreshTimeoutAfterBatchTimesOut(t *testing.T) {
	previous := usageLogBestEffortTimeout
	usageLogBestEffortTimeout = 50 * time.Millisecond
	t.Cleanup(func() { usageLogBestEffortTimeout = previous })

	db, mock := newSQLMock(t)
	prepared := prepareUsageLogInsert(&service.UsageLog{
		UserID:    1,
		APIKeyID:  2,
		AccountID: 3,
		RequestID: "req-best-effort-fallback",
		Model:     "gpt-5",
		CreatedAt: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC),
	})

	mock.ExpectExec(`(?s)WITH input.*INSERT INTO usage_logs`).
		WillDelayFor(200 * time.Millisecond).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO usage_logs`).
		WithArgs(anySliceToDriverValues(prepared.args)...).
		WillReturnResult(sqlmock.NewResult(0, 1))

	resultCh := make(chan error, 1)
	repo := &usageLogRepository{}
	repo.flushBestEffortBatch(db, []usageLogBestEffortRequest{{prepared: prepared, apiKeyID: 2, resultCh: resultCh}})

	require.NoError(t, <-resultCh)
	require.NoError(t, mock.ExpectationsWereMet())
}
