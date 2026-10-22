package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"xzitpocket-control/internal/ai"
	"xzitpocket-control/internal/store/sqlite"
)

const (
	aiOutputPrefix   = "ai/papers/"
	aiPrecheckPages  = 4
	aiRetryBaseDelay = 30 * time.Second
	aiPollIdle       = 20 * time.Second
	aiPollBusy       = 3 * time.Second
)

// AITaskView is the administrator-facing job row.
type AITaskView struct {
	ID           string `json:"id"`
	ResourceID   string `json:"resource_id"`
	ResourceName string `json:"resource_name"`
	CourseCode   string `json:"course_code"`
	Status       string `json:"status"`
	Solvable     int    `json:"solvable"`
	PrecheckNote string `json:"precheck_note,omitempty"`
	Attempt      int    `json:"attempt"`
	NextPage     int    `json:"next_page"`
	TotalPages   int    `json:"total_pages"`
	Progress     int    `json:"progress"`
	Markdown     string `json:"markdown,omitempty"`
	Error        string `json:"error,omitempty"`
	Model        string `json:"model,omitempty"`
	OutputSize   int64  `json:"output_size,omitempty"`
	HasOutput    bool   `json:"has_output"`
	Archived     bool   `json:"archived"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	StartedAt    string `json:"started_at,omitempty"`
	FinishedAt   string `json:"finished_at,omitempty"`
}

func aiTaskView(row sqlite.AITaskRow, includeMarkdown bool) AITaskView {
	progress := 0
	if row.TotalPages > 0 {
		done := row.NextPage - 1
		if done < 0 {
			done = 0
		}
		if done > row.TotalPages {
			done = row.TotalPages
		}
		progress = done * 100 / row.TotalPages
	}
	if row.Status == "succeeded" {
		progress = 100
	}
	view := AITaskView{
		ID: row.ID, ResourceID: row.ResourceID, ResourceName: row.ResourceName,
		CourseCode: row.CourseCode, Status: row.Status, Solvable: row.Solvable,
		PrecheckNote: row.PrecheckNote, Attempt: row.Attempt, NextPage: row.NextPage,
		TotalPages: row.TotalPages, Progress: progress, Error: row.Error, Model: row.Model,
		OutputSize: row.OutputSize, HasOutput: row.OutputKey != "", Archived: row.ResourceArchived,
		CreatedAt: row.CreatedAt.Format(time.RFC3339), UpdatedAt: row.UpdatedAt.Format(time.RFC3339),
	}
	if !row.StartedAt.IsZero() {
		view.StartedAt = row.StartedAt.Format(time.RFC3339)
	}
	if !row.FinishedAt.IsZero() {
		view.FinishedAt = row.FinishedAt.Format(time.RFC3339)
	}
	if includeMarkdown {
		view.Markdown = row.Markdown
	}
	return view
}

// initAIAnalysis prepares the worker. It is a no-op unless the feature is
// enabled in .env.
func (a *App) initAIAnalysis() error {
	if !a.Cfg.AIAnalysisEnabled {
		return nil
	}
	windows, err := ai.ParseWindows(a.Cfg.AIWindows)
	if err != nil {
		return fmt.Errorf("CONTROL_AI_WINDOWS: %w", err)
	}
	location, err := time.LoadLocation(a.Cfg.AITimezone)
	if err != nil {
		a.Logger.Warn("AI timezone not found, falling back to UTC+8", "timezone", a.Cfg.AITimezone, "error", err)
		location = time.FixedZone("CST", 8*3600)
	}
	model, err := ai.NewModel(ai.ModelOptions{
		APIKey:  a.Cfg.AIAPIKey,
		BaseURL: a.Cfg.AIBaseURL,
		Name:    a.Cfg.AIModel,
	})
	if err != nil {
		return err
	}
	a.aiModel = model
	a.aiWindows = windows
	a.aiLocation = location
	a.aiCancel = make(map[string]context.CancelFunc)
	for _, binary := range []string{"pdftohtml", "cwebp", "weasyprint", "node"} {
		if _, err := exec.LookPath(binary); err != nil {
			a.Logger.Warn("AI analysis dependency missing", "binary", binary, "hint", "apt-get install -y poppler-utils webp weasyprint fonts-noto-cjk nodejs npm")
		}
	}
	if a.Cfg.AIMathJaxDir != "" {
		if _, err := os.Stat(filepath.Join(a.Cfg.AIMathJaxDir, "render.js")); err != nil {
			a.Logger.Warn("AI math renderer missing; formulas stay as LaTeX", "dir", a.Cfg.AIMathJaxDir, "hint", "cd ai-math && npm install --omit=dev")
		}
	}
	return nil
}

func (a *App) aiNow() time.Time {
	if a.aiLocation == nil {
		return time.Now()
	}
	return time.Now().In(a.aiLocation)
}

// StartAIAnalysis runs the single worker loop. Jobs are processed one at a
// time and only while the configured daily windows are open.
func (a *App) StartAIAnalysis(ctx context.Context) {
	if a.aiModel == nil {
		return
	}
	if count, err := a.Store.ResetRunningAITasks(ctx); err != nil {
		a.Logger.Error("reset AI tasks after restart", "error", err)
	} else if count > 0 {
		a.Logger.Info("AI tasks returned to queue after restart", "count", count)
	}
	go func() {
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			processed := false
			if ai.InWindow(a.aiWindows, a.aiNow()) {
				task, ok, err := a.Store.ClaimNextAITask(ctx)
				if err != nil {
					a.Logger.Error("claim AI task", "error", err)
				} else if ok {
					a.runAITask(ctx, task)
					processed = true
				}
			}
			if processed {
				timer.Reset(aiPollBusy)
			} else {
				timer.Reset(aiPollIdle)
			}
		}
	}()
}

// EnqueueAIAnalyses creates or resets one task per resource.
func (a *App) EnqueueAIAnalyses(ctx context.Context, resourceIDs []string, actor string) ([]AITaskView, []map[string]string, error) {
	if a.aiModel == nil {
		return nil, nil, Err("ai_disabled", "AI 解析未启用", 503)
	}
	if len(resourceIDs) == 0 {
		return nil, nil, Err("invalid_request", "请选择至少一份试卷", 400)
	}
	if len(resourceIDs) > 100 {
		return nil, nil, Err("invalid_request", "一次最多提交 100 份试卷", 400)
	}
	created := make([]AITaskView, 0, len(resourceIDs))
	failed := make([]map[string]string, 0)
	seen := make(map[string]bool)
	for _, rawID := range resourceIDs {
		id := strings.TrimSpace(rawID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		task, err := a.Store.CreateOrResetAITask(ctx, id, actor)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				failed = append(failed, map[string]string{"resource_id": id, "reason": "试卷不存在、已归档或不是 PDF"})
				continue
			}
			return nil, nil, err
		}
		if task.Status == "running" {
			failed = append(failed, map[string]string{"resource_id": id, "reason": "该试卷正在解析中"})
			continue
		}
		row, err := a.Store.GetAITaskRow(ctx, task.ID)
		if err != nil {
			return nil, nil, err
		}
		created = append(created, aiTaskView(row, false))
	}
	if len(created) > 0 {
		_ = a.Store.AddAudit(ctx, actor, "ai_analysis_enqueue", "resource_ai_task", "", fmt.Sprintf(`{"count":%d}`, len(created)), time.Now().UTC())
	}
	return created, failed, nil
}

// ListAITasksForAdmin lists jobs with their resource names.
func (a *App) ListAITasksForAdmin(ctx context.Context, status, search string, limit, offset int) ([]AITaskView, int, error) {
	rows, total, err := a.Store.ListAITasks(ctx, status, search, clampLimit(limit), max0(offset))
	if err != nil {
		return nil, 0, err
	}
	views := make([]AITaskView, 0, len(rows))
	for _, row := range rows {
		views = append(views, aiTaskView(row, false))
	}
	return views, total, nil
}

// GetAITaskForAdmin returns one job including its markdown.
func (a *App) GetAITaskForAdmin(ctx context.Context, id string) (AITaskView, error) {
	row, err := a.Store.GetAITaskRow(ctx, strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AITaskView{}, ErrNotFound
		}
		return AITaskView{}, err
	}
	return aiTaskView(row, true), nil
}

// RetryAITask resumes a failed/paused/cancelled/skipped job from its last
// completed page.
func (a *App) RetryAITask(ctx context.Context, id, actor string) (AITaskView, error) {
	task, err := a.Store.GetAITask(ctx, strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AITaskView{}, ErrNotFound
		}
		return AITaskView{}, err
	}
	if task.Status == "running" {
		return AITaskView{}, Err("ai_running", "该试卷正在解析中", 409)
	}
	status := "queued"
	attempt := 0
	zero := int64(0)
	patch := sqlite.AITaskPatch{
		Status: &status, Attempt: &attempt, Error: stringPtr(""), FinishedAt: timePtr(time.Time{}), OutputSize: &zero,
	}
	if task.Solvable == 0 {
		unknown := -1
		empty := "[]"
		one := 1
		markdown := ""
		patch.Solvable = &unknown
		patch.MessagesJSON = &empty
		patch.NextPage = &one
		patch.Markdown = &markdown
	}
	if err := a.Store.UpdateAITask(ctx, task.ID, patch); err != nil {
		return AITaskView{}, err
	}
	_ = a.Store.AddAudit(ctx, actor, "ai_analysis_retry", "resource_ai_task", task.ID, "{}", time.Now().UTC())
	row, err := a.Store.GetAITaskRow(ctx, task.ID)
	if err != nil {
		return AITaskView{}, err
	}
	return aiTaskView(row, false), nil
}

// CancelAITask stops a job and cancels any in-flight model request.
func (a *App) CancelAITask(ctx context.Context, id, actor string) (AITaskView, error) {
	task, err := a.Store.GetAITask(ctx, strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AITaskView{}, ErrNotFound
		}
		return AITaskView{}, err
	}
	if task.Status != "queued" && task.Status != "running" && task.Status != "paused" {
		return AITaskView{}, Err("ai_inactive", "该任务当前不可取消", 409)
	}
	status := "cancelled"
	reason := "已取消"
	if err := a.Store.UpdateAITask(ctx, task.ID, sqlite.AITaskPatch{
		Status: &status, Error: &reason, FinishedAt: timePtr(time.Now().UTC()),
	}); err != nil {
		return AITaskView{}, err
	}
	a.cancelAIRequest(task.ID)
	_ = a.Store.AddAudit(ctx, actor, "ai_analysis_cancel", "resource_ai_task", task.ID, "{}", time.Now().UTC())
	row, err := a.Store.GetAITaskRow(ctx, task.ID)
	if err != nil {
		return AITaskView{}, err
	}
	return aiTaskView(row, false), nil
}

// OpenAITaskOutput streams the generated analysis PDF.
func (a *App) OpenAITaskOutput(ctx context.Context, id string) (*http.Response, string, error) {
	row, err := a.Store.GetAITaskRow(ctx, strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	if row.OutputKey == "" {
		return nil, "", Err("ai_output_missing", "解析结果尚未生成", 404)
	}
	resp, err := a.openStoredFile(ctx, row.OutputKey)
	if err != nil {
		return nil, "", err
	}
	return resp, downloadFileName(row.ResourceName+"-AI解析", fileFormatPDF), nil
}

func (a *App) cancelAIRequest(taskID string) {
	a.aiCancelMu.Lock()
	cancel := a.aiCancel[taskID]
	a.aiCancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *App) registerAIRequest(taskID string, cancel context.CancelFunc) {
	a.aiCancelMu.Lock()
	a.aiCancel[taskID] = cancel
	a.aiCancelMu.Unlock()
}

func (a *App) unregisterAIRequest(taskID string) {
	a.aiCancelMu.Lock()
	delete(a.aiCancel, taskID)
	a.aiCancelMu.Unlock()
}

func (a *App) runAITask(parent context.Context, task sqlite.AITask) {
	ctx, cancel := context.WithCancel(parent)
	a.registerAIRequest(task.ID, cancel)
	defer a.unregisterAIRequest(task.ID)
	defer cancel()

	finish := func(status, reason string) {
		// Use a background context: the worker context may already be
		// cancelled by a shutdown or a cancel request.
		writeCtx, writeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer writeCancel()
		now := time.Now().UTC()
		patch := sqlite.AITaskPatch{Status: &status, Error: &reason}
		if status != "paused" && status != "queued" {
			patch.FinishedAt = &now
		}
		if err := a.Store.UpdateAITask(writeCtx, task.ID, patch); err != nil {
			a.Logger.Error("update AI task status", "error", err, "task", task.ID)
		}
	}

	pause := func(reason string) {
		if current, err := a.Store.GetAITask(context.Background(), task.ID); err == nil && current.Status == "cancelled" {
			return
		}
		writeCtx, writeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer writeCancel()
		_ = a.Store.UpdateAITask(writeCtx, task.ID, sqlite.AITaskPatch{
			Status: stringPtr("paused"), Attempt: intPtr(0), Error: &reason,
		})
	}

	resource, err := a.requireResource(ctx, task.ResourceID)
	if err != nil {
		finish("failed", "试卷不存在或已删除")
		return
	}
	if resource.PdfKey == "" {
		finish("failed", "试卷没有 PDF 文件")
		return
	}

	workDir := filepath.Join(a.Cfg.AIWorkDir, task.ID)
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		finish("failed", "创建临时目录失败")
		return
	}
	pdfPath := filepath.Join(workDir, "source.pdf")
	if _, err := os.Stat(pdfPath); err != nil {
		if err := a.downloadAIPdf(ctx, resource.PdfKey, pdfPath); err != nil {
			pause("下载 PDF 失败：" + err.Error())
			return
		}
	}

	extractCtx, extractCancel := context.WithTimeout(ctx, 10*time.Minute)
	paper, err := ai.ExtractPaper(extractCtx, pdfPath, resource.Name, ai.ExtractOptions{
		WorkDir:      workDir,
		ImageQuality: a.Cfg.AIImageQuality,
		ImageMaxEdge: a.Cfg.AIImageMaxEdge,
	})
	extractCancel()
	if err != nil {
		finish("failed", "解析 PDF 失败："+err.Error())
		return
	}
	totalPages := paper.TotalPages
	if err := a.Store.UpdateAITask(ctx, task.ID, sqlite.AITaskPatch{
		TotalPages: &totalPages, Model: stringPtr(a.aiModel.Name()),
	}); err != nil {
		a.Logger.Error("update AI task pages", "error", err, "task", task.ID)
	}

	history := ai.DecodeMessages(task.MessagesJSON)
	if task.Solvable < 0 {
		verdict, raw, err := a.precheckPaper(ctx, paper)
		if err != nil {
			if ctx.Err() != nil || a.aiWindowClosed() {
				pause("等待下一个解析时间窗")
				return
			}
			finish("failed", "预判失败："+err.Error())
			return
		}
		solvable := 1
		if !verdict.Solvable {
			solvable = 0
		}
		if err := a.Store.UpdateAITask(ctx, task.ID, sqlite.AITaskPatch{
			Solvable: &solvable, PrecheckNote: stringPtr(verdict.Reason),
		}); err != nil {
			a.Logger.Error("save AI precheck", "error", err, "task", task.ID)
		}
		if !verdict.Solvable {
			markdown := ""
			finishPatch := sqlite.AITaskPatch{
				Status: stringPtr("skipped"), Error: stringPtr("预判为不可解答：" + verdict.Reason),
				Markdown: &markdown, FinishedAt: timePtr(time.Now().UTC()),
			}
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = a.Store.UpdateAITask(cleanupCtx, task.ID, finishPatch)
			cleanupCancel()
			_ = os.RemoveAll(workDir)
			return
		}
		precheckInstruction := fmt.Sprintf("下面是《%s》的前 %d 页（共 %d 页）。请判断它是否是可用于作答的试卷，并只输出 JSON。", paper.Title, minInt(aiPrecheckPages, paper.TotalPages), paper.TotalPages)
		_, plain, buildErr := ai.BuildContent(paper, 1, minInt(aiPrecheckPages, paper.TotalPages), precheckInstruction)
		if buildErr == nil {
			history = append(history, ai.Message{Role: "user", Text: plain}, ai.Message{Role: "assistant", Text: raw})
		}
		task.MessagesJSON = ai.EncodeMessages(history)
		_ = a.Store.UpdateAITask(ctx, task.ID, sqlite.AITaskPatch{MessagesJSON: &task.MessagesJSON})
	}

	nextPage := task.NextPage
	if nextPage < 1 {
		nextPage = 1
	}
	attempt := task.Attempt
	for nextPage <= paper.TotalPages {
		if !a.aiInWindow() {
			pause("等待下一个解析时间窗")
			return
		}
		chunkEnd := minInt(nextPage+a.Cfg.AIPagesPerChunk-1, paper.TotalPages)
		instruction := fmt.Sprintf(ai.ChunkInstruction, paper.Title, nextPage, chunkEnd, paper.TotalPages)
		parts, plain, err := ai.BuildContent(paper, nextPage, chunkEnd, instruction)
		if err != nil {
			finish("failed", "构建请求失败："+err.Error())
			return
		}
		callCtx, callCancel, windowEnd := a.aiCallContext(ctx)
		content, err := a.aiModel.Analyze(callCtx, history, parts)
		callCancel()
		if err != nil {
			if ctx.Err() != nil {
				pause("服务停止，等待恢复")
				return
			}
			if ai.IsPermanentError(err) {
				finish("failed", fmt.Sprintf("第 %d-%d 页解析失败：%s", nextPage, chunkEnd, err.Error()))
				return
			}
			if !time.Now().Before(windowEnd) || a.aiWindowClosed() {
				pause("时间窗结束，等待下一个时间窗")
				return
			}
			attempt++
			if attempt >= a.Cfg.AIMaxAttempts {
				finish("failed", fmt.Sprintf("第 %d-%d 页解析失败（已重试 %d 次）：%s", nextPage, chunkEnd, attempt, err.Error()))
				return
			}
			_ = a.Store.UpdateAITask(ctx, task.ID, sqlite.AITaskPatch{Attempt: &attempt, Error: stringPtr(err.Error())})
			if !a.sleepInWindow(ctx, aiRetryDelay(attempt)) {
				pause("等待下一个解析时间窗")
				return
			}
			continue
		}
		if strings.TrimSpace(content) == "" {
			attempt++
			if attempt >= a.Cfg.AIMaxAttempts {
				finish("failed", "模型返回空内容")
				return
			}
			_ = a.Store.UpdateAITask(ctx, task.ID, sqlite.AITaskPatch{Attempt: &attempt, Error: stringPtr("模型返回空内容")})
			if !a.sleepInWindow(ctx, 5*time.Second) {
				pause("等待下一个解析时间窗")
				return
			}
			continue
		}

		history = append(history, ai.Message{Role: "user", Text: plain}, ai.Message{Role: "assistant", Text: content})
		task.Markdown = joinMarkdown(task.Markdown, content)
		nextPage = chunkEnd + 1
		attempt = 0
		messagesJSON := ai.EncodeMessages(history)
		if err := a.Store.UpdateAITask(ctx, task.ID, sqlite.AITaskPatch{
			NextPage: &nextPage, Attempt: &attempt, MessagesJSON: &messagesJSON,
			Markdown: &task.Markdown, Error: stringPtr(""),
		}); err != nil {
			a.Logger.Error("save AI progress", "error", err, "task", task.ID)
		}
	}

	if strings.TrimSpace(task.Markdown) == "" {
		finish("failed", "解析结果为空")
		return
	}
	renderCtx, renderCancel := context.WithTimeout(ctx, 5*time.Minute)
	document := "# " + resource.Name + " · AI 解析\n\n" + task.Markdown
	pdf, err := ai.RenderMarkdownPDF(renderCtx, document, a.Cfg.AIMathJaxDir)
	renderCancel()
	if err != nil {
		finish("failed", "生成 PDF 失败："+err.Error())
		return
	}
	client, err := a.requireStorage()
	if err != nil {
		finish("failed", "对象存储不可用")
		return
	}
	outputKey := aiOutputPrefix + task.ResourceID + ".pdf"
	if err := client.PutObject(ctx, outputKey, "application/pdf", pdf); err != nil {
		finish("failed", "上传解析 PDF 失败："+err.Error())
		return
	}
	outputSize := int64(len(pdf))
	succeeded := "succeeded"
	_ = a.Store.UpdateAITask(ctx, task.ID, sqlite.AITaskPatch{
		Status: &succeeded, OutputKey: &outputKey, OutputSize: &outputSize,
		Error: stringPtr(""), FinishedAt: timePtr(time.Now().UTC()), NextPage: &nextPage,
		Markdown: &task.Markdown,
	})
	_ = os.RemoveAll(workDir)
}

func (a *App) precheckPaper(ctx context.Context, paper *ai.Paper) (ai.Verdict, string, error) {
	attempts := 0
	for {
		if !a.aiInWindow() {
			return ai.Verdict{}, "", fmt.Errorf("等待解析时间窗")
		}
		instruction := fmt.Sprintf("下面是《%s》的前 %d 页（共 %d 页）。请判断它是否是可用于作答的试卷，并只输出 JSON。",
			paper.Title, minInt(aiPrecheckPages, paper.TotalPages), paper.TotalPages)
		parts, _, err := ai.BuildContent(paper, 1, minInt(aiPrecheckPages, paper.TotalPages), instruction)
		if err != nil {
			return ai.Verdict{}, "", err
		}
		callCtx, callCancel, windowEnd := a.aiCallContext(ctx)
		verdict, raw, err := a.aiModel.Precheck(callCtx, parts)
		callCancel()
		if err == nil {
			return verdict, raw, nil
		}
		if ai.IsPermanentError(err) {
			return ai.Verdict{}, "", err
		}
		if ctx.Err() != nil || !time.Now().Before(windowEnd) || a.aiWindowClosed() {
			return ai.Verdict{}, "", err
		}
		attempts++
		if attempts >= a.Cfg.AIMaxAttempts {
			return ai.Verdict{}, "", err
		}
		if !a.sleepInWindow(ctx, aiRetryDelay(attempts)) {
			return ai.Verdict{}, "", err
		}
	}
}

func (a *App) downloadAIPdf(ctx context.Context, key, dest string) error {
	client, err := a.requireStorage()
	if err != nil {
		return err
	}
	resp, err := client.GetObject(ctx, key)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	tmp := dest + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, resp.Body); err != nil {
		file.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

func (a *App) aiInWindow() bool {
	return a.aiModel != nil && ai.InWindow(a.aiWindows, a.aiNow())
}

func (a *App) aiWindowClosed() bool {
	return a.aiModel != nil && !ai.InWindow(a.aiWindows, a.aiNow())
}

func (a *App) aiCallContext(parent context.Context) (context.Context, context.CancelFunc, time.Time) {
	now := a.aiNow()
	windowEnd, ok := ai.WindowEnd(a.aiWindows, now)
	if !ok {
		windowEnd = now
	}
	deadline := windowEnd
	if timeout := now.Add(a.Cfg.AIRequestTimeout); timeout.Before(deadline) {
		deadline = timeout
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	return ctx, cancel, windowEnd
}

func (a *App) sleepInWindow(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	}
	return a.aiInWindow()
}

func aiRetryDelay(attempt int) time.Duration {
	delay := aiRetryBaseDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay > 10*time.Minute {
			return 10 * time.Minute
		}
	}
	return delay
}

func joinMarkdown(existing, next string) string {
	existing = strings.TrimSpace(existing)
	next = strings.TrimSpace(next)
	if existing == "" {
		return next
	}
	return existing + "\n\n" + next
}

func stringPtr(value string) *string { return &value }
func intPtr(value int) *int          { return &value }
func timePtr(value time.Time) *time.Time {
	return &value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
