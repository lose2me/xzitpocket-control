package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

func (s *Store) ListQuestionBanks(ctx context.Context, limit, offset int, status string) ([]QuestionBankSummary, error) {
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	args := []any{}
	query := `SELECT b.id, b.order_id, b.is_new, b.name, b.status, b.requires_cdk, COUNT(q.id), b.created_at, b.updated_at
		FROM question_banks b LEFT JOIN questions q ON q.bank_id = b.id`
	if status != "" {
		query += " WHERE b.status = ?"
		args = append(args, status)
	}
	query += " GROUP BY b.id ORDER BY b.order_id ASC, b.id LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]QuestionBankSummary, 0)
	for rows.Next() {
		var item QuestionBankSummary
		var orderID, isNew, requiresCDK int
		var created, updated int64
		if err := rows.Scan(&item.ID, &orderID, &isNew, &item.Name, &item.Status, &requiresCDK, &item.QuestionCount, &created, &updated); err != nil {
			return nil, err
		}
		item.OrderID = orderID
		item.IsNew = isNew != 0
		item.RequiresCDK = requiresCDK != 0
		item.CreatedAt, item.UpdatedAt = fromMillis(created), fromMillis(updated)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) CountQuestionBanks(ctx context.Context, status string) (int, error) {
	query := "SELECT COUNT(*) FROM question_banks"
	args := []any{}
	if status != "" {
		query += " WHERE status = ?"
		args = append(args, status)
	}
	var count int
	err := s.DB.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}

// ListAccessibleQuestionBanks returns every active bank. A CDK-protected bank
// is listed before it is unlocked; reading its questions remains protected by
// GetQuestionBankForUser.
func (s *Store) ListAccessibleQuestionBanks(ctx context.Context, limit, offset int) ([]QuestionBankSummary, int, error) {
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	query := `SELECT b.id, b.order_id, b.is_new, b.name, b.status, b.requires_cdk, COUNT(q.id), b.created_at, b.updated_at
		FROM question_banks b LEFT JOIN questions q ON q.bank_id = b.id
		WHERE b.status = 'active'
		GROUP BY b.id ORDER BY b.order_id ASC, b.id LIMIT ? OFFSET ?`
	rows, err := s.DB.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]QuestionBankSummary, 0)
	for rows.Next() {
		var item QuestionBankSummary
		var orderID, isNew, requiresCDK int
		var created, updated int64
		if err := rows.Scan(&item.ID, &orderID, &isNew, &item.Name, &item.Status, &requiresCDK, &item.QuestionCount, &created, &updated); err != nil {
			return nil, 0, err
		}
		item.OrderID = orderID
		item.IsNew, item.RequiresCDK = isNew != 0, requiresCDK != 0
		item.CreatedAt, item.UpdatedAt = fromMillis(created), fromMillis(updated)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	var total int
	countQuery := `SELECT COUNT(*) FROM question_banks b WHERE b.status = 'active'`
	if err := s.DB.QueryRowContext(ctx, countQuery).Scan(&total); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *Store) GetQuestionBank(ctx context.Context, id string) (QuestionBank, error) {
	row := s.DB.QueryRowContext(ctx,
		"SELECT id, order_id, is_new, name, status, requires_cdk, created_at, updated_at FROM question_banks WHERE id = ?", id)
	var bank QuestionBank
	var orderID, isNew, requiresCDK int
	var created, updated int64
	if err := row.Scan(&bank.ID, &orderID, &isNew, &bank.Name, &bank.Status, &requiresCDK, &created, &updated); err != nil {
		return QuestionBank{}, err
	}
	bank.OrderID = orderID
	bank.IsNew = isNew != 0
	bank.RequiresCDK = requiresCDK != 0
	bank.CreatedAt, bank.UpdatedAt = fromMillis(created), fromMillis(updated)
	bank.Questions = make([]Question, 0)
	rows, err := s.DB.QueryContext(ctx, `SELECT q.id, q.bank_id, q.question_number, q.type, q.title, q.question_text,
		q.correct_answer, q.sort_order, o.id, o.question_id, o.label, o.text, o.sort_order
		FROM questions q LEFT JOIN question_options o ON o.question_id = q.id
		WHERE q.bank_id = ? ORDER BY q.question_number, q.sort_order, q.id, o.sort_order, o.id`, id)
	if err != nil {
		return QuestionBank{}, err
	}
	defer rows.Close()
	questions := make([]Question, 0)
	var current *Question
	for rows.Next() {
		var q Question
		var optionID, optionQuestionID, optionLabel, optionText sql.NullString
		var optionSort sql.NullInt64
		if err := rows.Scan(&q.ID, &q.BankID, &q.QuestionNumber, &q.Type, &q.Title, &q.QuestionText, &q.CorrectAnswer, &q.SortOrder,
			&optionID, &optionQuestionID, &optionLabel, &optionText, &optionSort); err != nil {
			return QuestionBank{}, err
		}
		if current == nil || current.ID != q.ID {
			q.Options = make([]QuestionOption, 0)
			questions = append(questions, q)
			current = &questions[len(questions)-1]
		}
		if optionID.Valid {
			current.Options = append(current.Options, QuestionOption{ID: optionID.String, QuestionID: optionQuestionID.String, Label: optionLabel.String, Text: optionText.String, SortOrder: int(optionSort.Int64)})
		}
	}
	if err := rows.Close(); err != nil {
		return QuestionBank{}, err
	}
	if err := rows.Err(); err != nil {
		return QuestionBank{}, err
	}
	bank.Questions = questions
	return bank, nil
}

func (s *Store) CreateQuestionBank(ctx context.Context, bank QuestionBank) error {
	if strings.TrimSpace(bank.ID) == "" {
		_, err := s.CreateQuestionBankAuto(ctx, bank)
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if bank.OrderID <= 0 {
		bank.OrderID, err = reserveQuestionBankOrderTx(ctx, tx)
		if err != nil {
			return err
		}
	} else if err := ensureQuestionBankOrderAvailableTx(ctx, tx, bank.OrderID, ""); err != nil {
		return err
	} else if err := advanceQuestionBankOrderCounterTx(ctx, tx, bank.OrderID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO question_banks(id, order_id, is_new, name, status, requires_cdk, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, bank.ID, bank.OrderID, boolInt(bank.IsNew), bank.Name, bank.Status, boolInt(bank.RequiresCDK), millis(bank.CreatedAt), millis(bank.UpdatedAt)); err != nil {
		return err
	}
	if err := insertQuestions(ctx, tx, bank); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateQuestionBankAuto allocates a monotonic QB identifier and inserts the
// complete aggregate in one transaction. Identifiers are never reused.
func (s *Store) CreateQuestionBankAuto(ctx context.Context, bank QuestionBank) (QuestionBank, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return QuestionBank{}, err
	}
	defer func() { _ = tx.Rollback() }()
	for {
		var next int64
		if err := tx.QueryRowContext(ctx, "SELECT next_number FROM question_bank_id_counter WHERE id = 1").Scan(&next); err != nil {
			return QuestionBank{}, err
		}
		if next < 1 {
			next = 1
		}
		if next == math.MaxInt64 {
			return QuestionBank{}, errors.New("question bank ID counter exhausted")
		}
		if _, err := tx.ExecContext(ctx, "UPDATE question_bank_id_counter SET next_number = ? WHERE id = 1", next+1); err != nil {
			return QuestionBank{}, err
		}
		candidate := fmt.Sprintf("QB-%03d", next)
		var exists int
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM question_banks WHERE id = ? LIMIT 1", candidate).Scan(&exists)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return QuestionBank{}, err
		}
		bank.ID = candidate
		if bank.OrderID <= 0 {
			bank.OrderID, err = reserveQuestionBankOrderTx(ctx, tx)
			if err != nil {
				return QuestionBank{}, err
			}
		} else if err := ensureQuestionBankOrderAvailableTx(ctx, tx, bank.OrderID, ""); err != nil {
			return QuestionBank{}, err
		} else if err := advanceQuestionBankOrderCounterTx(ctx, tx, bank.OrderID); err != nil {
			return QuestionBank{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO question_banks(id, order_id, is_new, name, status, requires_cdk, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, bank.ID, bank.OrderID, boolInt(bank.IsNew), bank.Name, bank.Status, boolInt(bank.RequiresCDK), millis(bank.CreatedAt), millis(bank.UpdatedAt)); err != nil {
			return QuestionBank{}, err
		}
		if err := insertQuestions(ctx, tx, bank); err != nil {
			return QuestionBank{}, err
		}
		if err := tx.Commit(); err != nil {
			return QuestionBank{}, err
		}
		return bank, nil
	}
}

func (s *Store) UpdateQuestionBank(ctx context.Context, bank QuestionBank) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if bank.OrderID <= 0 {
		if err := tx.QueryRowContext(ctx, "SELECT order_id FROM question_banks WHERE id = ?", bank.ID).Scan(&bank.OrderID); err != nil {
			return err
		}
	}
	if err := ensureQuestionBankOrderAvailableTx(ctx, tx, bank.OrderID, bank.ID); err != nil {
		return err
	}
	if err := advanceQuestionBankOrderCounterTx(ctx, tx, bank.OrderID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE question_banks SET order_id = ?, is_new = ?, name = ?, status = ?, requires_cdk = ?, updated_at = ? WHERE id = ?`,
		bank.OrderID, boolInt(bank.IsNew), bank.Name, bank.Status, boolInt(bank.RequiresCDK), millis(bank.UpdatedAt), bank.ID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM questions WHERE bank_id = ?", bank.ID); err != nil {
		return err
	}
	if err := insertQuestions(ctx, tx, bank); err != nil {
		return err
	}
	return tx.Commit()
}

// ErrQuestionBankOrderTaken is returned when an administrator attempts to
// assign an order already used by another question bank.
var ErrQuestionBankOrderTaken = errors.New("question bank order already used")

// reserveQuestionBankOrderTx allocates the next monotonic order value. The
// counter and the bank insert share one transaction, so a successful write
// always leaves automatic allocation ahead of the assigned value.
func reserveQuestionBankOrderTx(ctx context.Context, tx *sql.Tx) (int, error) {
	var next int64
	if err := tx.QueryRowContext(ctx, "SELECT next_number FROM question_bank_order_counter WHERE id = 1").Scan(&next); err != nil {
		return 0, err
	}
	if next < 1 || next >= math.MaxInt64 {
		return 0, errors.New("question bank order counter exhausted")
	}
	if _, err := tx.ExecContext(ctx, "UPDATE question_bank_order_counter SET next_number = ? WHERE id = 1", next+1); err != nil {
		return 0, err
	}
	return int(next), nil
}

// ensureQuestionBankOrderAvailableTx checks uniqueness inside the same
// transaction used for the bank write. excludeID is used while editing the
// bank that already owns the requested order.
func ensureQuestionBankOrderAvailableTx(ctx context.Context, tx *sql.Tx, orderID int, excludeID string) error {
	if orderID <= 0 {
		return errors.New("question bank order must be positive")
	}
	var exists int
	query := "SELECT 1 FROM question_banks WHERE order_id = ?"
	args := []any{orderID}
	if excludeID != "" {
		query += " AND id <> ?"
		args = append(args, excludeID)
	}
	err := tx.QueryRowContext(ctx, query+" LIMIT 1", args...).Scan(&exists)
	if err == nil {
		return ErrQuestionBankOrderTaken
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

// advanceQuestionBankOrderCounterTx keeps automatic allocation ahead of any
// manually assigned order. It is intentionally idempotent for lower values.
func advanceQuestionBankOrderCounterTx(ctx context.Context, tx *sql.Tx, orderID int) error {
	if orderID <= 0 {
		return errors.New("question bank order must be positive")
	}
	var next int64
	if err := tx.QueryRowContext(ctx, "SELECT next_number FROM question_bank_order_counter WHERE id = 1").Scan(&next); err != nil {
		return err
	}
	if int64(orderID) < next {
		return nil
	}
	if int64(orderID) >= math.MaxInt64 {
		return errors.New("question bank order counter exhausted")
	}
	_, err := tx.ExecContext(ctx, "UPDATE question_bank_order_counter SET next_number = ? WHERE id = 1", int64(orderID)+1)
	return err
}

func (s *Store) SetQuestionBankStatus(ctx context.Context, id, status string, now time.Time) error {
	if status != "active" && status != "draft" && status != "disabled" {
		return errors.New("invalid question bank status")
	}
	result, err := s.DB.ExecContext(ctx, "UPDATE question_banks SET status = ?, updated_at = ? WHERE id = ?", status, millis(now), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func insertQuestions(ctx context.Context, tx *sql.Tx, bank QuestionBank) error {
	for _, q := range bank.Questions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO questions(id, bank_id, question_number, type, title, question_text, correct_answer, sort_order)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, q.ID, bank.ID, q.QuestionNumber, q.Type, q.Title, q.QuestionText, q.CorrectAnswer, q.SortOrder); err != nil {
			return err
		}
		for _, option := range q.Options {
			if _, err := tx.ExecContext(ctx, `INSERT INTO question_options(id, question_id, label, text, sort_order)
				VALUES (?, ?, ?, ?, ?)`, option.ID, q.ID, option.Label, option.Text, option.SortOrder); err != nil {
				return err
			}
		}
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Store) HasLibraryAccess(ctx context.Context, bankID, studentHash string) (bool, error) {
	var requires, count int
	if err := s.DB.QueryRowContext(ctx, "SELECT requires_cdk FROM question_banks WHERE id = ?", bankID).Scan(&requires); err != nil {
		return false, err
	}
	if requires == 0 {
		return true, nil
	}
	if studentHash == "" {
		return false, nil
	}
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM library_cdks
		WHERE question_bank_id = ? AND bound_student_id_hash = ? AND status = 'used'`, bankID, studentHash).Scan(&count)
	return count > 0, err
}

var (
	ErrLibraryCDKNotFound = errors.New("library cdk not found")
	ErrLibraryCDKDisabled = errors.New("library cdk disabled")
	ErrLibraryCDKBound    = errors.New("library cdk bound to another student")
)

func (s *Store) CreateLibraryCDK(ctx context.Context, cdk LibraryCDK) error {
	return s.CreateLibraryCDKs(ctx, []LibraryCDK{cdk})
}

// CreateLibraryCDKs inserts a batch atomically. This prevents a failed batch
// request from leaving a partially generated set of codes in the database.
func (s *Store) CreateLibraryCDKs(ctx context.Context, cdks []LibraryCDK) error {
	if len(cdks) == 0 {
		return errors.New("library cdk batch is empty")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, cdk := range cdks {
		if _, err := tx.ExecContext(ctx, `INSERT INTO library_cdks
			(id, code_hash, question_bank_id, bound_student_id_hash, bound_student_id_ciphertext, bound_user_id, status, created_at, used_at)
			VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?)`,
			cdk.ID, cdk.CodeHash, cdk.QuestionBankID, cdk.BoundStudentIDHash, cdk.BoundStudentIDCiphertext, cdk.BoundUserID,
			cdk.Status, millis(cdk.CreatedAt), nullableMillis(cdk.UsedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListLibraryCDKs(ctx context.Context, limit, offset int, search ...string) ([]LibraryCDK, int, error) {
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	query, args := libraryCDKListQuery(limit, offset, search...)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]LibraryCDK, 0)
	for rows.Next() {
		item, err := scanLibraryCDK(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	countQuery, countArgs := libraryCDKCountQuery(search...)
	var total int
	if err := s.DB.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func libraryCDKSearch(search ...string) (string, string, string) {
	query := ""
	studentHash := ""
	codeHash := ""
	if len(search) > 0 {
		query = strings.TrimSpace(search[0])
	}
	if len(search) > 1 {
		studentHash = strings.TrimSpace(search[1])
	}
	if len(search) > 2 {
		codeHash = strings.TrimSpace(search[2])
	}
	return query, studentHash, codeHash
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func libraryCDKWhere(search ...string) (string, []any) {
	query, studentHash, codeHash := libraryCDKSearch(search...)
	if query == "" && studentHash == "" && codeHash == "" {
		return "", nil
	}
	like := "%" + escapeLike(query) + "%"
	where := " WHERE (c.id LIKE ? ESCAPE '\\' OR c.question_bank_id LIKE ? ESCAPE '\\' OR b.name LIKE ? ESCAPE '\\' OR c.status LIKE ? ESCAPE '\\'"
	args := []any{like, like, like, like}
	if studentHash != "" {
		where += " OR c.bound_student_id_hash = ?"
		args = append(args, studentHash)
	}
	if codeHash != "" {
		where += " OR c.code_hash = ?"
		args = append(args, codeHash)
	}
	where += ")"
	return where, args
}

func libraryCDKListQuery(limit, offset int, search ...string) (string, []any) {
	where, args := libraryCDKWhere(search...)
	query := `SELECT c.id, c.code_hash, c.question_bank_id, b.name,
		COALESCE(c.bound_student_id_hash, ''), COALESCE(c.bound_student_id_ciphertext, ''), COALESCE(c.bound_user_id, ''), c.status,
		c.created_at, c.used_at
		FROM library_cdks c JOIN question_banks b ON b.id = c.question_bank_id
		` + where + ` ORDER BY c.created_at DESC, c.id LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	return query, args
}

func libraryCDKCountQuery(search ...string) (string, []any) {
	where, args := libraryCDKWhere(search...)
	return "SELECT COUNT(*) FROM library_cdks c JOIN question_banks b ON b.id = c.question_bank_id" + where, args
}

// RedeemLibraryCDK atomically binds an unused code to one student. Repeating
// the same code for the same student is idempotent; another student gets a
// conflict and the code remains bound to its original student.
func (s *Store) RedeemLibraryCDK(ctx context.Context, codeHash, studentHash, studentCiphertext, userID string, now time.Time) (LibraryCDK, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return LibraryCDK{}, err
	}
	defer func() { _ = tx.Rollback() }()
	row := tx.QueryRowContext(ctx, `SELECT c.id, c.code_hash, c.question_bank_id, b.name,
		COALESCE(c.bound_student_id_hash, ''), COALESCE(c.bound_student_id_ciphertext, ''), COALESCE(c.bound_user_id, ''), c.status,
		c.created_at, c.used_at
		FROM library_cdks c JOIN question_banks b ON b.id = c.question_bank_id WHERE c.code_hash = ?`, codeHash)
	cdk, err := scanLibraryCDK(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LibraryCDK{}, ErrLibraryCDKNotFound
	}
	if err != nil {
		return LibraryCDK{}, err
	}
	if cdk.Status == "disabled" {
		return LibraryCDK{}, ErrLibraryCDKDisabled
	}
	if cdk.BoundStudentIDHash != "" {
		if cdk.BoundStudentIDHash != studentHash {
			return LibraryCDK{}, ErrLibraryCDKBound
		}
		if err := tx.Commit(); err != nil {
			return LibraryCDK{}, err
		}
		return cdk, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE library_cdks SET bound_student_id_hash = ?, bound_student_id_ciphertext = ?, bound_user_id = ?, status = 'used', used_at = ?
		WHERE id = ? AND status = 'active' AND bound_student_id_hash IS NULL`, studentHash, studentCiphertext, nullableString(userID), millis(now), cdk.ID)
	if err != nil {
		return LibraryCDK{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return LibraryCDK{}, err
	}
	if affected != 1 {
		return LibraryCDK{}, ErrLibraryCDKBound
	}
	cdk.BoundStudentIDHash, cdk.BoundStudentIDCiphertext, cdk.BoundUserID, cdk.Status = studentHash, studentCiphertext, userID, "used"
	used := now
	cdk.UsedAt = &used
	if err := tx.Commit(); err != nil {
		return LibraryCDK{}, err
	}
	return cdk, nil
}

func (s *Store) SetLibraryCDKStatus(ctx context.Context, id, status string) error {
	if status != "active" && status != "disabled" {
		return errors.New("invalid library cdk status")
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE library_cdks
		SET status = CASE WHEN ? = 'disabled' THEN 'disabled' WHEN bound_student_id_hash IS NULL THEN 'active' ELSE 'used' END
		WHERE id = ?`, status, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}

type scanner interface{ Scan(dest ...any) error }

func scanLibraryCDK(row scanner) (LibraryCDK, error) {
	var item LibraryCDK
	var created int64
	var used sql.NullInt64
	if err := row.Scan(&item.ID, &item.CodeHash, &item.QuestionBankID, &item.QuestionBankName,
		&item.BoundStudentIDHash, &item.BoundStudentIDCiphertext, &item.BoundUserID, &item.Status,
		&created, &used); err != nil {
		return LibraryCDK{}, err
	}
	item.CreatedAt = fromMillis(created)
	item.UsedAt = nullableTime(used)
	return item, nil
}

func nullableMillis(value *time.Time) any {
	if value == nil {
		return nil
	}
	return millis(*value)
}
