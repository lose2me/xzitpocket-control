package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// AITask is one exam-PDF analysis job. The row doubles as the resumable job
// state: NextPage points at the first page that has not been analysed and
// MessagesJSON holds the text-only conversation so far.
type AITask struct {
	ID           string
	ResourceID   string
	Status       string
	Solvable     int
	PrecheckNote string
	Attempt      int
	NextPage     int
	TotalPages   int
	MessagesJSON string
	Markdown     string
	OutputKey    string
	OutputSize   int64
	Error        string
	Model        string
	CreatedBy    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	StartedAt    time.Time
	FinishedAt   time.Time
}

// AITaskRow is an administrator-facing task joined with its resource.
type AITaskRow struct {
	AITask
	ResourceName     string
	CourseCode       string
	ResourceArchived bool
}

// AITaskPatch updates only the fields that are set. It is used both by the
// worker (progress) and by administrator actions (cancel/retry).
type AITaskPatch struct {
	Status       *string
	Solvable     *int
	PrecheckNote *string
	Attempt      *int
	NextPage     *int
	TotalPages   *int
	MessagesJSON *string
	Markdown     *string
	OutputKey    *string
	OutputSize   *int64
	Error        *string
	Model        *string
	StartedAt    *time.Time
	FinishedAt   *time.Time
}

const aiTaskColumns = "id, resource_id, status, solvable, precheck_note, attempt, next_page, total_pages, messages_json, markdown, output_key, output_size, error, model, created_by, created_at, updated_at, started_at, finished_at"

func scanAITask(row rowScanner) (AITask, error) {
	var item AITask
	var createdAt, updatedAt, startedAt, finishedAt int64
	err := row.Scan(
		&item.ID, &item.ResourceID, &item.Status, &item.Solvable, &item.PrecheckNote,
		&item.Attempt, &item.NextPage, &item.TotalPages, &item.MessagesJSON,
		&item.Markdown, &item.OutputKey, &item.OutputSize, &item.Error, &item.Model,
		&item.CreatedBy, &createdAt, &updatedAt, &startedAt, &finishedAt,
	)
	if err != nil {
		return AITask{}, err
	}
	item.CreatedAt = time.Unix(createdAt, 0).UTC()
	item.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	if startedAt > 0 {
		item.StartedAt = time.Unix(startedAt, 0).UTC()
	}
	if finishedAt > 0 {
		item.FinishedAt = time.Unix(finishedAt, 0).UTC()
	}
	return item, nil
}

// CreateOrResetAITask makes sure a task exists for the resource. An existing
// task is reset to the initial state unless it is currently running.
func (s *Store) CreateOrResetAITask(ctx context.Context, resourceID, actor string) (AITask, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return AITask{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var exists string
	err = tx.QueryRowContext(ctx, `SELECT id FROM resources WHERE id = ? AND pdf_key <> '' AND LOWER(pdf_key) LIKE '%.pdf' AND archived_at = 0`, resourceID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return AITask{}, sql.ErrNoRows
	}
	if err != nil {
		return AITask{}, err
	}

	now := time.Now().UTC().Unix()
	existing, err := scanAITask(tx.QueryRowContext(ctx, `SELECT `+aiTaskColumns+` FROM resource_ai_tasks WHERE resource_id = ?`, resourceID))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id, idErr := newStoreID("ai")
		if idErr != nil {
			return AITask{}, idErr
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO resource_ai_tasks(id, resource_id, status, created_by, created_at, updated_at)
			VALUES (?, ?, 'queued', ?, ?, ?)`, id, resourceID, actor, now, now); err != nil {
			return AITask{}, err
		}
		existing, err = scanAITask(tx.QueryRowContext(ctx, `SELECT `+aiTaskColumns+` FROM resource_ai_tasks WHERE id = ?`, id))
		if err != nil {
			return AITask{}, err
		}
	case err != nil:
		return AITask{}, err
	case existing.Status == "running":
		// The worker owns the row; the caller reports "already running"
		// instead of fighting it for the same PDF.
	case existing.Status == "succeeded" || existing.Status == "skipped" || existing.Status == "failed" ||
		existing.Status == "cancelled" || existing.Status == "paused" || existing.Status == "queued":
		if _, err := tx.ExecContext(ctx, `UPDATE resource_ai_tasks SET
			status = 'queued', solvable = -1, precheck_note = '', attempt = 0, next_page = 1,
			total_pages = 0, messages_json = '[]', markdown = '', output_key = '', output_size = 0,
			error = '', model = '', created_by = ?, started_at = 0, finished_at = 0, updated_at = ?
			WHERE id = ?`, actor, now, existing.ID); err != nil {
			return AITask{}, err
		}
		existing, err = scanAITask(tx.QueryRowContext(ctx, `SELECT `+aiTaskColumns+` FROM resource_ai_tasks WHERE id = ?`, existing.ID))
		if err != nil {
			return AITask{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return AITask{}, err
	}
	return existing, nil
}

// GetAITask returns one task by id.
func (s *Store) GetAITask(ctx context.Context, id string) (AITask, error) {
	item, err := scanAITask(s.DB.QueryRowContext(ctx, `SELECT `+aiTaskColumns+` FROM resource_ai_tasks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return AITask{}, sql.ErrNoRows
	}
	return item, err
}

// GetAITaskByResource returns the task for a resource, if any.
func (s *Store) GetAITaskByResource(ctx context.Context, resourceID string) (AITask, error) {
	item, err := scanAITask(s.DB.QueryRowContext(ctx, `SELECT `+aiTaskColumns+` FROM resource_ai_tasks WHERE resource_id = ?`, resourceID))
	if errors.Is(err, sql.ErrNoRows) {
		return AITask{}, sql.ErrNoRows
	}
	return item, err
}

// GetAITaskRow returns a task joined with its resource.
func (s *Store) GetAITaskRow(ctx context.Context, id string) (AITaskRow, error) {
	var item AITaskRow
	var createdAt, updatedAt, startedAt, finishedAt, archivedAt int64
	err := s.DB.QueryRowContext(ctx, `SELECT
			t.id, t.resource_id, t.status, t.solvable, t.precheck_note, t.attempt, t.next_page,
			t.total_pages, t.messages_json, t.markdown, t.output_key, t.output_size, t.error,
			t.model, t.created_by, t.created_at, t.updated_at, t.started_at, t.finished_at,
			r.name, r.course_code, r.archived_at
		FROM resource_ai_tasks t JOIN resources r ON r.id = t.resource_id
		WHERE t.id = ?`, id).Scan(
		&item.ID, &item.ResourceID, &item.Status, &item.Solvable, &item.PrecheckNote, &item.Attempt,
		&item.NextPage, &item.TotalPages, &item.MessagesJSON, &item.Markdown, &item.OutputKey,
		&item.OutputSize, &item.Error, &item.Model, &item.CreatedBy, &createdAt, &updatedAt,
		&startedAt, &finishedAt, &item.ResourceName, &item.CourseCode, &archivedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return AITaskRow{}, sql.ErrNoRows
	}
	if err != nil {
		return AITaskRow{}, err
	}
	item.CreatedAt = time.Unix(createdAt, 0).UTC()
	item.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	if startedAt > 0 {
		item.StartedAt = time.Unix(startedAt, 0).UTC()
	}
	if finishedAt > 0 {
		item.FinishedAt = time.Unix(finishedAt, 0).UTC()
	}
	item.ResourceArchived = archivedAt > 0
	return item, nil
}

// ListAITasks returns tasks filtered by status, resource name or course code.
func (s *Store) ListAITasks(ctx context.Context, status, search string, limit, offset int) ([]AITaskRow, int, error) {
	where := []string{"1=1"}
	args := []any{}
	if status = strings.TrimSpace(status); status != "" {
		where = append(where, "t.status = ?")
		args = append(args, status)
	}
	if search = strings.TrimSpace(search); search != "" {
		like := "%" + search + "%"
		where = append(where, "(r.name LIKE ? OR r.course_code LIKE ?)")
		args = append(args, like, like)
	}
	clause := strings.Join(where, " AND ")

	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM resource_ai_tasks t JOIN resources r ON r.id = t.resource_id WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.DB.QueryContext(ctx, `SELECT
			t.id, t.resource_id, t.status, t.solvable, t.precheck_note, t.attempt, t.next_page,
			t.total_pages, t.messages_json, t.markdown, t.output_key, t.output_size, t.error,
			t.model, t.created_by, t.created_at, t.updated_at, t.started_at, t.finished_at,
			r.name, r.course_code, r.archived_at
		FROM resource_ai_tasks t JOIN resources r ON r.id = t.resource_id
		WHERE `+clause+` ORDER BY t.updated_at DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]AITaskRow, 0, limit)
	for rows.Next() {
		var item AITaskRow
		var createdAt, updatedAt, startedAt, finishedAt, archivedAt int64
		if err := rows.Scan(
			&item.ID, &item.ResourceID, &item.Status, &item.Solvable, &item.PrecheckNote, &item.Attempt,
			&item.NextPage, &item.TotalPages, &item.MessagesJSON, &item.Markdown, &item.OutputKey,
			&item.OutputSize, &item.Error, &item.Model, &item.CreatedBy, &createdAt, &updatedAt,
			&startedAt, &finishedAt, &item.ResourceName, &item.CourseCode, &archivedAt,
		); err != nil {
			return nil, 0, err
		}
		item.CreatedAt = time.Unix(createdAt, 0).UTC()
		item.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		if startedAt > 0 {
			item.StartedAt = time.Unix(startedAt, 0).UTC()
		}
		if finishedAt > 0 {
			item.FinishedAt = time.Unix(finishedAt, 0).UTC()
		}
		item.ResourceArchived = archivedAt > 0
		items = append(items, item)
	}
	return items, total, rows.Err()
}

// ClaimNextAITask atomically moves the oldest queued/paused task to running.
func (s *Store) ClaimNextAITask(ctx context.Context) (AITask, bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return AITask{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	item, err := scanAITask(tx.QueryRowContext(ctx, `SELECT `+aiTaskColumns+`
		FROM resource_ai_tasks WHERE status IN ('queued', 'paused')
		ORDER BY created_at ASC LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return AITask{}, false, nil
	}
	if err != nil {
		return AITask{}, false, err
	}
	now := time.Now().UTC().Unix()
	res, err := tx.ExecContext(ctx, `UPDATE resource_ai_tasks SET status = 'running', error = '',
		started_at = CASE WHEN started_at = 0 THEN ? ELSE started_at END, updated_at = ?
		WHERE id = ? AND status IN ('queued', 'paused')`, now, now, item.ID)
	if err != nil {
		return AITask{}, false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return AITask{}, false, err
	}
	if affected == 0 {
		return AITask{}, false, nil
	}
	if err := tx.Commit(); err != nil {
		return AITask{}, false, err
	}
	item.Status = "running"
	item.UpdatedAt = time.Unix(now, 0).UTC()
	if item.StartedAt.IsZero() {
		item.StartedAt = time.Unix(now, 0).UTC()
	}
	return item, true, nil
}

// UpdateAITask applies a partial update.
func (s *Store) UpdateAITask(ctx context.Context, id string, p AITaskPatch) error {
	sets := []string{"updated_at = ?"}
	args := []any{time.Now().UTC().Unix()}
	add := func(column string, value any) {
		sets = append(sets, column+" = ?")
		args = append(args, value)
	}
	if p.Status != nil {
		add("status", *p.Status)
	}
	if p.Solvable != nil {
		add("solvable", *p.Solvable)
	}
	if p.PrecheckNote != nil {
		add("precheck_note", *p.PrecheckNote)
	}
	if p.Attempt != nil {
		add("attempt", *p.Attempt)
	}
	if p.NextPage != nil {
		add("next_page", *p.NextPage)
	}
	if p.TotalPages != nil {
		add("total_pages", *p.TotalPages)
	}
	if p.MessagesJSON != nil {
		add("messages_json", *p.MessagesJSON)
	}
	if p.Markdown != nil {
		add("markdown", *p.Markdown)
	}
	if p.OutputKey != nil {
		add("output_key", *p.OutputKey)
	}
	if p.OutputSize != nil {
		add("output_size", *p.OutputSize)
	}
	if p.Error != nil {
		add("error", *p.Error)
	}
	if p.Model != nil {
		add("model", *p.Model)
	}
	if p.StartedAt != nil {
		if p.StartedAt.IsZero() {
			add("started_at", int64(0))
		} else {
			add("started_at", p.StartedAt.UTC().Unix())
		}
	}
	if p.FinishedAt != nil {
		if p.FinishedAt.IsZero() {
			add("finished_at", int64(0))
		} else {
			add("finished_at", p.FinishedAt.UTC().Unix())
		}
	}
	args = append(args, id)
	res, err := s.DB.ExecContext(ctx, `UPDATE resource_ai_tasks SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ResetRunningAITasks returns in-flight jobs to the queue after a restart.
func (s *Store) ResetRunningAITasks(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE resource_ai_tasks SET status = 'queued', error = '', updated_at = ?
		WHERE status = 'running'`, time.Now().UTC().Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
