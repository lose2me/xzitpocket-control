package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"xzitpocket-control/internal/ai"
	"xzitpocket-control/internal/config"
	"xzitpocket-control/internal/store/sqlite"
)

const (
	aiOutputPrefix   = "ai/papers/"
	aiPrecheckPages  = 4
	aiRetryBaseDelay = 30 * time.Second
	aiPollIdle       = 20 * time.Second
	aiPollBusy       = 3 * time.Second
	aiScanInterval   = 10 * time.Second
	aiScanBatch      = 20
)

// AITaskView is the administrator-facing job row.
type AITaskView struct {
	ID             string `json:"id"`
	ResourceID     string `json:"resource_id"`
	Mode           string `json:"mode"`
	ResourceName   string `json:"resource_name"`
	CourseCode     string `json:"course_code"`
	Status         string `json:"status"`
	Solvable       int    `json:"solvable"`
	PrecheckNote   string `json:"precheck_note,omitempty"`
	Attempt        int    `json:"attempt"`
	NextPage       int    `json:"next_page"`
	TotalPages     int    `json:"total_pages"`
	Progress       int    `json:"progress"`
	Markdown       string `json:"markdown,omitempty"`
	Error          string `json:"error,omitempty"`
	Model          string `json:"model,omitempty"`
	OutputSize     int64  `json:"output_size,omitempty"`
	SinglePass     bool   `json:"single_pass"`
	HasOutput      bool   `json:"has_output"`
	HasPrompts     bool   `json:"has_prompts"`
	Archived       bool   `json:"archived"`
	ElapsedSeconds int64  `json:"elapsed_seconds"`
	Prompts        string `json:"prompts,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	StartedAt      string `json:"started_at,omitempty"`
	FinishedAt     string `json:"finished_at,omitempty"`
}

func aiTaskView(row sqlite.AITaskRow, includeMarkdown bool, pagesPerChunk int) AITaskView {
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
	end := time.Now().UTC()
	if !row.FinishedAt.IsZero() {
		end = row.FinishedAt
	} else if row.Status == "paused" && !row.UpdatedAt.IsZero() {
		end = row.UpdatedAt
	}
	elapsedSeconds := int64(0)
	if !row.StartedAt.IsZero() && end.After(row.StartedAt) {
		elapsedSeconds = int64(end.Sub(row.StartedAt).Seconds())
	}
	view := AITaskView{
		ID: row.ID, ResourceID: row.ResourceID, Mode: row.Mode, ResourceName: row.ResourceName,
		CourseCode: row.CourseCode, Status: row.Status, Solvable: row.Solvable,
		PrecheckNote: row.PrecheckNote, Attempt: row.Attempt, NextPage: row.NextPage,
		TotalPages: row.TotalPages, Progress: progress, Error: row.Error, Model: row.Model,
		OutputSize: row.OutputSize, HasOutput: row.OutputKey != "", HasPrompts: row.PromptsJSON != "", Archived: row.ResourceArchived,
		SinglePass: row.TotalPages > 0 && (pagesPerChunk <= 0 || row.TotalPages <= pagesPerChunk),
		CreatedAt:  row.CreatedAt.Format(time.RFC3339), UpdatedAt: row.UpdatedAt.Format(time.RFC3339),
		ElapsedSeconds: elapsedSeconds,
	}
	if !row.StartedAt.IsZero() {
		view.StartedAt = row.StartedAt.Format(time.RFC3339)
	}
	if !row.FinishedAt.IsZero() {
		view.FinishedAt = row.FinishedAt.Format(time.RFC3339)
	}
	if includeMarkdown {
		view.Markdown = row.Markdown
		view.Prompts = row.PromptsJSON
	}
	return view
}

// initAIAnalysis prepares the AI worker state. Settings can change while the
// process runs, so this only seeds the snapshot and the wake channel; the
// worker itself keeps running and re-reads the snapshot on every loop.
func (a *App) initAIAnalysis() error {
	a.aiCancel = make(map[string]context.CancelFunc)
	a.aiWake = make(chan struct{}, 1)
	enabled, err := a.buildAIState(a.cfg())
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	for _, binary := range []string{"pdftohtml", "cwebp", "weasyprint", "node"} {
		if _, err := exec.LookPath(binary); err != nil {
			a.Logger.Warn("AI analysis dependency missing", "binary", binary, "hint", "apt-get install -y poppler-utils webp weasyprint fonts-noto-cjk nodejs npm")
		}
	}
	if a.cfg().AIMathJaxDir != "" {
		if _, err := os.Stat(filepath.Join(a.cfg().AIMathJaxDir, "render.js")); err != nil {
			a.Logger.Warn("AI math renderer missing; formulas stay as LaTeX", "dir", a.cfg().AIMathJaxDir, "hint", "cd ai-math && npm install --omit=dev")
		}
	}
	return nil
}

// buildAIState replaces the AI snapshot from cfg. It reports whether AI is
// enabled. A disabled configuration stores an empty snapshot, which the worker
// treats as "do nothing" and the API as 503.
func (a *App) buildAIState(cfg config.Config) (bool, error) {
	state, err := aiStateFromConfig(cfg, a.Logger)
	if err != nil {
		return false, err
	}
	a.aiState.Store(state)
	return state.model != nil, nil
}

// aiStateFromConfig validates the AI settings and builds the worker snapshot
// without touching the running state, so a rejected update changes nothing.
func aiStateFromConfig(cfg config.Config, logger *slog.Logger) (*aiRuntime, error) {
	if !cfg.AIAnalysisEnabled {
		return &aiRuntime{}, nil
	}
	windows, err := ai.ParseWindows(cfg.AIWindows)
	if err != nil {
		return nil, fmt.Errorf("CONTROL_AI_WINDOWS: %w", err)
	}
	location, err := time.LoadLocation(cfg.AITimezone)
	if err != nil {
		logger.Warn("AI timezone not found, falling back to UTC+8", "timezone", cfg.AITimezone, "error", err)
		location = time.FixedZone("CST", 8*3600)
	}
	model, err := ai.NewModel(ai.ModelOptions{
		APIKey:         cfg.AIAPIKey,
		BaseURL:        cfg.AIBaseURL,
		Name:           cfg.AIModel,
		PrecheckSystem: cfg.AIPrecheckSystem,
		AnalysisSystem: cfg.AIAnalysisSystem,
		ContinuePrompt: cfg.AIContinuePrompt,
	})
	if err != nil {
		return nil, err
	}
	return &aiRuntime{model: model, windows: windows, location: location}, nil
}

func (a *App) aiNow() time.Time {
	if a.ai().location == nil {
		return time.Now()
	}
	return time.Now().In(a.ai().location)
}

// StartAIAnalysis runs the single worker loop. Jobs are processed one at a
// time and only while the configured daily windows are open.
func (a *App) StartAIAnalysis(ctx context.Context) {
	// Always fix stale rows: AI may be disabled now and enabled from the
	// console later, and a crash would otherwise leave tasks stuck as running.
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
			case <-a.aiWake:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
			processed := false
			state := a.ai()
			if state.model != nil && ai.InWindow(state.windows, a.aiNow()) {
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
	go a.scanAIResources(ctx)
}

// scanAIResources periodically queues PDFs that have never been analysed.
// Papers already marked skipped/failed/succeeded, or with an active run, are
// left alone.
func (a *App) scanAIResources(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if a.ai().model == nil {
			timer.Reset(aiScanInterval)
			continue
		}
		tasks, err := a.Store.CreateAITasksForMissingResources(ctx, aiScanBatch, "auto")
		if err != nil {
			if ctx.Err() == nil {
				a.Logger.Error("scan AI resources", "error", err)
			}
		} else if len(tasks) > 0 {
			a.wakeAIWorker()
			a.Logger.Info("auto-enqueued AI tasks", "count", len(tasks))
			_ = a.Store.AddAudit(ctx, "auto", "ai_analysis_enqueue", "resource_ai_task", "", fmt.Sprintf(`{"count":%d,"source":"scan"}`, len(tasks)), time.Now().UTC())
		}
		timer.Reset(aiScanInterval)
	}
}

// EnqueueAIAnalyses creates a new run for each resource and mode. Earlier
// runs are kept as history.
func (a *App) EnqueueAIAnalyses(ctx context.Context, resourceIDs []string, modes []string, actor string) ([]AITaskView, []map[string]string, error) {
	if a.ai().model == nil {
		return nil, nil, Err("ai_disabled", "AI 解析未启用", 503)
	}
	if len(resourceIDs) == 0 {
		return nil, nil, Err("invalid_request", "请选择至少一份试卷", 400)
	}
	if len(resourceIDs) > 100 {
		return nil, nil, Err("invalid_request", "一次最多提交 100 份试卷", 400)
	}
	requested, err := normalizeAIModes(modes)
	if err != nil {
		return nil, nil, err
	}
	created := make([]AITaskView, 0, len(resourceIDs)*len(requested))
	failed := make([]map[string]string, 0)
	seen := make(map[string]bool)
	for _, rawID := range resourceIDs {
		id := strings.TrimSpace(rawID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		for _, mode := range requested {
			// An explicit re-parse restarts the paper in this mode: stop any
			// queued/running attempt (it stays in history as cancelled).
			if active, ok, err := a.Store.GetActiveAITaskByResource(ctx, id, mode); err != nil {
				return nil, nil, err
			} else if ok && (active.Status == "queued" || active.Status == "running") {
				if _, err := a.CancelAITask(ctx, active.ID, actor); err != nil {
					var apiErr *APIError
					if !errors.As(err, &apiErr) || apiErr.Code != "ai_inactive" {
						return nil, nil, err
					}
				}
			}
			task, inserted, err := a.Store.CreateAITask(ctx, id, mode, actor)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					failed = append(failed, map[string]string{"resource_id": id, "mode": mode, "reason": "试卷不存在、已归档或不是 PDF"})
					continue
				}
				return nil, nil, err
			}
			if !inserted {
				reason := "该试卷已有未完成的解析任务"
				switch task.Status {
				case "running":
					reason = "该试卷正在解析中"
				case "queued":
					reason = "该试卷已在解析队列中"
				case "paused":
					reason = "该试卷有暂停中的解析任务，请先继续解析"
				}
				failed = append(failed, map[string]string{"resource_id": id, "mode": mode, "reason": aiModeLabel(mode) + "：" + reason})
				continue
			}
			row, err := a.Store.GetAITaskRow(ctx, task.ID)
			if err != nil {
				return nil, nil, err
			}
			created = append(created, aiTaskView(row, false, a.cfg().AIPagesPerChunk))
		}
	}
	if len(created) > 0 {
		a.wakeAIWorker()
		_ = a.Store.AddAudit(ctx, actor, "ai_analysis_enqueue", "resource_ai_task", "", fmt.Sprintf(`{"count":%d}`, len(created)), time.Now().UTC())
	}
	return created, failed, nil
}

// aiAnalysisModes are the two report depths generated for every paper.
var aiAnalysisModes = []string{"basic", "advanced"}

// normalizeAIModes returns the requested modes, defaulting to both pipelines.
func normalizeAIModes(modes []string) ([]string, error) {
	if len(modes) == 0 {
		return append([]string(nil), aiAnalysisModes...), nil
	}
	seen := make(map[string]bool)
	out := make([]string, 0, len(modes))
	for _, raw := range modes {
		mode := strings.ToLower(strings.TrimSpace(raw))
		if mode != "basic" && mode != "advanced" {
			return nil, Err("invalid_request", "解析类型必须是 basic 或 advanced", 400)
		}
		if !seen[mode] {
			seen[mode] = true
			out = append(out, mode)
		}
	}
	return out, nil
}

func aiModeLabel(mode string) string {
	if mode == "advanced" {
		return "高级解析"
	}
	return "基础解析"
}

// wakeAIWorker asks the single worker loop to check the queue immediately
// instead of waiting for the next idle poll.
func (a *App) wakeAIWorker() {
	if a.aiWake == nil {
		return
	}
	select {
	case a.aiWake <- struct{}{}:
	default:
	}
}

// ListAITasksForAdmin lists jobs with their resource names.
func (a *App) ListAITasksForAdmin(ctx context.Context, status, search string, limit, offset int) ([]AITaskView, int, error) {
	rows, total, err := a.Store.ListAITasks(ctx, status, search, clampLimit(limit), max0(offset))
	if err != nil {
		return nil, 0, err
	}
	views := make([]AITaskView, 0, len(rows))
	for _, row := range rows {
		views = append(views, aiTaskView(row, false, a.cfg().AIPagesPerChunk))
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
	return aiTaskView(row, true, a.cfg().AIPagesPerChunk), nil
}

// RetryAITask resumes a paused job in place. Failed, cancelled and skipped
// jobs get a new history row so the previous attempt is never overwritten.
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
	if task.Status == "paused" {
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
		a.wakeAIWorker()
		_ = a.Store.AddAudit(ctx, actor, "ai_analysis_retry", "resource_ai_task", task.ID, "{}", time.Now().UTC())
		row, err := a.Store.GetAITaskRow(ctx, task.ID)
		if err != nil {
			return AITaskView{}, err
		}
		return aiTaskView(row, false, a.cfg().AIPagesPerChunk), nil
	}
	created, inserted, err := a.Store.CreateAITask(ctx, task.ResourceID, task.Mode, actor)
	if err != nil {
		return AITaskView{}, err
	}
	if !inserted {
		if created.Status == "running" {
			return AITaskView{}, Err("ai_running", "该试卷正在解析中", 409)
		}
		return AITaskView{}, Err("ai_active", "该试卷已有未完成的解析任务", 409)
	}
	a.wakeAIWorker()
	_ = a.Store.AddAudit(ctx, actor, "ai_analysis_retry", "resource_ai_task", created.ID, "{}", time.Now().UTC())
	row, err := a.Store.GetAITaskRow(ctx, created.ID)
	if err != nil {
		return AITaskView{}, err
	}
	return aiTaskView(row, false, a.cfg().AIPagesPerChunk), nil
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
	return aiTaskView(row, false, a.cfg().AIPagesPerChunk), nil
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

	// Settings can be changed from the console; work with the snapshot that was
	// current when the task started.
	model := a.ai().model
	if model == nil {
		return
	}

	isCancelled := func() bool {
		current, err := a.Store.GetAITask(context.Background(), task.ID)
		return err == nil && current.Status == "cancelled"
	}

	finish := func(status, reason string) {
		// A user cancel wins over any worker completion or failure path.
		if isCancelled() {
			return
		}
		// Use a background context: the worker context may already be
		// cancelled by a shutdown or a cancel request.
		writeCtx, writeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer writeCancel()
		now := time.Now().UTC()
		patch := sqlite.AITaskPatch{Status: &status, Error: &reason, SkipIfCancelled: true}
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
			Status: stringPtr("paused"), Attempt: intPtr(0), Error: &reason, SkipIfCancelled: true,
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

	workDir := filepath.Join(a.cfg().AIWorkDir, task.ID)
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
		ImageQuality: a.cfg().AIImageQuality,
		ImageMaxEdge: a.cfg().AIImageMaxEdge,
	})
	extractCancel()
	if err != nil {
		finish("failed", "解析 PDF 失败："+err.Error())
		return
	}
	totalPages := paper.TotalPages
	if err := a.Store.UpdateAITask(ctx, task.ID, sqlite.AITaskPatch{
		TotalPages: &totalPages, Model: stringPtr(model.Name()),
	}); err != nil {
		a.Logger.Error("update AI task pages", "error", err, "task", task.ID)
	}

	history := ai.DecodeMessages(task.MessagesJSON)
	if task.Solvable < 0 {
		verdict, _, promptCalls, err := a.precheckPaper(ctx, paper)
		a.saveAIPrompts(task.ID, &task.PromptsJSON, promptCalls)
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
			if isCancelled() {
				_ = os.RemoveAll(workDir)
				return
			}
			markdown := ""
			finishPatch := sqlite.AITaskPatch{
				Status: stringPtr("skipped"), Error: stringPtr("预判为不可解答：" + verdict.Reason),
				Markdown: &markdown, FinishedAt: timePtr(time.Now().UTC()), SkipIfCancelled: true,
			}
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = a.Store.UpdateAITask(cleanupCtx, task.ID, finishPatch)
			cleanupCancel()
			_ = os.RemoveAll(workDir)
			return
		}
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
		chunkEnd := minInt(nextPage+a.cfg().AIPagesPerChunk-1, paper.TotalPages)
		instruction := fmt.Sprintf(chunkInstruction(a.cfg(), task.Mode), paper.Title, nextPage, chunkEnd, paper.TotalPages)
		parts, plain, err := ai.BuildContent(paper, nextPage, chunkEnd, instruction)
		if err != nil {
			finish("failed", "构建请求失败："+err.Error())
			return
		}
		callCtx, callCancel, windowEnd := a.aiCallContext(ctx)
		system := a.cfg().AIAnalysisSystem
		if task.Mode == "advanced" {
			system = a.cfg().AIAdvancedAnalysisSystem
		}
		content, promptCalls, err := model.AnalyzeWithSystem(callCtx, system, history, parts)
		callCancel()
		a.saveAIPrompts(task.ID, &task.PromptsJSON, promptCalls)
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
			if attempt >= a.cfg().AIMaxAttempts {
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
			if attempt >= a.cfg().AIMaxAttempts {
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
	document := "# " + resource.Name + " · AI 解析（" + aiModeLabel(task.Mode) + "）\n\n" + task.Markdown
	pdf, err := ai.RenderMarkdownPDF(renderCtx, document, a.cfg().AIMathJaxDir)
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
	outputKey := aiOutputPrefix + task.ResourceID + "/" + task.ID + ".pdf"
	if isCancelled() {
		_ = os.RemoveAll(workDir)
		return
	}
	if err := client.PutObject(ctx, outputKey, "application/pdf", pdf); err != nil {
		finish("failed", "上传解析 PDF 失败："+err.Error())
		return
	}
	outputSize := int64(len(pdf))
	succeeded := "succeeded"
	if isCancelled() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = client.DeleteObject(cleanupCtx, outputKey)
		cleanupCancel()
		_ = os.RemoveAll(workDir)
		return
	}
	if err := a.Store.UpdateAITask(ctx, task.ID, sqlite.AITaskPatch{
		Status: &succeeded, OutputKey: &outputKey, OutputSize: &outputSize,
		Error: stringPtr(""), FinishedAt: timePtr(time.Now().UTC()), NextPage: &nextPage,
		Markdown: &task.Markdown, SkipIfCancelled: true,
	}); err == nil {
		a.cleanupOlderAITasks(task)
	}
	_ = os.RemoveAll(workDir)
}

// cleanupOlderAITasks keeps only the newest successful run for a paper. Older
// rows and their PDFs are removed once the new result is safely stored.
func (a *App) cleanupOlderAITasks(task sqlite.AITask) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	keys, err := a.Store.DeleteOlderAITasks(ctx, task.ResourceID, task.ID, task.Mode)
	if err != nil {
		a.Logger.Warn("delete older AI tasks", "error", err, "task", task.ID)
		return
	}
	if len(keys) == 0 {
		return
	}
	client, err := a.requireStorage()
	if err != nil {
		a.Logger.Warn("delete older AI outputs", "error", err, "task", task.ID)
		return
	}
	for _, key := range keys {
		if err := client.DeleteObject(ctx, key); err != nil {
			a.Logger.Warn("delete older AI output", "error", err, "key", key)
		}
	}
}

// chunkInstruction returns the configured page-range instruction, falling back
// to the built-in template when the setting is blank.
func chunkInstruction(cfg config.Config, mode string) string {
	if mode == "advanced" {
		if strings.TrimSpace(cfg.AIAdvancedChunkInstruction) == "" {
			return ai.DefaultAdvancedChunkInstruction
		}
		return cfg.AIAdvancedChunkInstruction
	}
	if strings.TrimSpace(cfg.AIChunkInstruction) == "" {
		return ai.DefaultChunkInstruction
	}
	return cfg.AIChunkInstruction
}

// precheckInstruction returns the configured verdict request.
func precheckInstruction(cfg config.Config) string {
	if strings.TrimSpace(cfg.AIPrecheckInstruction) == "" {
		return ai.DefaultPrecheckInstruction
	}
	return cfg.AIPrecheckInstruction
}

func (a *App) precheckPaper(ctx context.Context, paper *ai.Paper) (ai.Verdict, string, []ai.PromptCall, error) {
	attempts := 0
	calls := make([]ai.PromptCall, 0)
	for {
		if !a.aiInWindow() {
			return ai.Verdict{}, "", calls, fmt.Errorf("等待解析时间窗")
		}
		instruction := fmt.Sprintf(precheckInstruction(a.cfg()), paper.Title, minInt(aiPrecheckPages, paper.TotalPages), paper.TotalPages)
		parts, _, err := ai.BuildContent(paper, 1, minInt(aiPrecheckPages, paper.TotalPages), instruction)
		if err != nil {
			return ai.Verdict{}, "", calls, err
		}
		callCtx, callCancel, windowEnd := a.aiCallContext(ctx)
		verdict, raw, promptCalls, err := a.ai().model.Precheck(callCtx, parts)
		callCancel()
		calls = append(calls, promptCalls...)
		if err == nil {
			return verdict, raw, calls, nil
		}
		if ai.IsPermanentError(err) {
			return ai.Verdict{}, "", calls, err
		}
		if ctx.Err() != nil || !time.Now().Before(windowEnd) || a.aiWindowClosed() {
			return ai.Verdict{}, "", calls, err
		}
		attempts++
		if attempts >= a.cfg().AIMaxAttempts {
			return ai.Verdict{}, "", calls, err
		}
		if !a.sleepInWindow(ctx, aiRetryDelay(attempts)) {
			return ai.Verdict{}, "", calls, err
		}
	}
}

func (a *App) saveAIPrompts(taskID string, prompts *string, calls []ai.PromptCall) {
	if len(calls) == 0 {
		return
	}
	*prompts = ai.AppendPromptCalls(*prompts, calls)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Store.UpdateAITask(ctx, taskID, sqlite.AITaskPatch{PromptsJSON: prompts}); err != nil {
		a.Logger.Warn("save AI prompts", "error", err, "task", taskID)
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
	return a.ai().model != nil && ai.InWindow(a.ai().windows, a.aiNow())
}

func (a *App) aiWindowClosed() bool {
	return a.ai().model != nil && !ai.InWindow(a.ai().windows, a.aiNow())
}

func (a *App) aiCallContext(parent context.Context) (context.Context, context.CancelFunc, time.Time) {
	now := a.aiNow()
	windowEnd, ok := ai.WindowEnd(a.ai().windows, now)
	if !ok {
		windowEnd = now
	}
	deadline := windowEnd
	if timeout := now.Add(a.cfg().AIRequestTimeout); timeout.Before(deadline) {
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
