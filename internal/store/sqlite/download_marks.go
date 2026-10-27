package sqlite

import (
	"context"
	"strings"
	"time"
)

// MarkDownloadOnce records that a user downloaded one item today. It reports
// whether this is the first download of that item by that user today, which is
// what the counters should count. Marks older than yesterday are pruned here so
// the table stays small without a cleanup job.
func (s *Store) MarkDownloadOnce(ctx context.Context, kind, itemID, userID string, now time.Time) (bool, error) {
	kind, itemID, userID = strings.TrimSpace(kind), strings.TrimSpace(itemID), strings.TrimSpace(userID)
	if kind == "" || itemID == "" || userID == "" {
		return false, nil
	}
	day := now.UTC().Format("2006-01-02")
	result, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO download_marks(kind, item_id, user_id, day, created_at)
		VALUES (?, ?, ?, ?, ?)`, kind, itemID, userID, day, millis(now))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM download_marks WHERE day < ?`,
		now.UTC().AddDate(0, 0, -1).Format("2006-01-02")); err != nil {
		return false, err
	}
	return affected == 1, nil
}
