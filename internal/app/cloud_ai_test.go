package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xzitpocket-control/internal/cloud"
	"xzitpocket-control/internal/config"
	"xzitpocket-control/internal/store/sqlite"
)

func newAIApp(t *testing.T) (*App, *sqlite.Store) {
	t.Helper()
	a, store := newCourseApp(t)
	a.SetConfigForTesting(config.Config{
		TokenPepper: []byte("token"), IDPepper: []byte("id"),
		EncryptionKey:     []byte("01234567890123456789012345678901"),
		AdminKey:          "test",
		AIAnalysisEnabled: true,
		AIAPIKey:          "test-key",
		AIBaseURL:         "http://127.0.0.1:1/v1",
		AIModel:           "deepseek-flash",
		AIWorkDir:         filepath.Join(t.TempDir(), "ai-work"),
		AIMaxAttempts:     3,
		AITimezone:        "Asia/Shanghai",
		AIWindows:         "00:00-24:00",
		COSRegion:         "ap-shanghai",
		COSBucket:         "xzitpocket-assets-1330114136",
		COSSecretID:       "id",
		COSSecretKey:      "key",
		COSWebsiteBucket:  "xuda-website-1330114136",
		CDNDomain:         "https://dl.xuda.live",
	})
	if err := a.initAIAnalysis(); err != nil {
		t.Fatal(err)
	}
	return a, store
}

func createAITestResource(t *testing.T, a *App, store *sqlite.Store, name string) sqlite.Resource {
	t.Helper()
	ctx := context.Background()
	resource, err := store.CreateResource(ctx, sqlite.ResourceInput{Name: name, CourseCode: "C1"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetResourcePdfKey(ctx, resource.ID, "resources/"+resource.ID+".pdf", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return resource
}

func TestEnqueueAIAnalysesValidation(t *testing.T) {
	a, store := newAIApp(t)
	ctx := context.Background()
	if _, _, err := a.EnqueueAIAnalyses(ctx, nil, "admin"); err == nil {
		t.Fatal("empty ids should fail")
	}
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = "id"
	}
	if _, _, err := a.EnqueueAIAnalyses(ctx, tooMany, "admin"); err == nil {
		t.Fatal("too many ids should fail")
	}
	created, failed, err := a.EnqueueAIAnalyses(ctx, []string{" ", ""}, "admin")
	if err != nil || len(created) != 0 || len(failed) != 0 {
		t.Fatalf("blank ids = %v %v %v", created, failed, err)
	}
	resource := createAITestResource(t, a, store, "试卷一.pdf")
	created, failed, err = a.EnqueueAIAnalyses(ctx, []string{resource.ID, resource.ID, "missing"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 || len(failed) != 1 || created[0].Status != "queued" {
		t.Fatalf("enqueue = %+v %+v", created, failed)
	}
	if failed[0]["reason"] != "试卷不存在、已归档或不是 PDF" {
		t.Fatalf("failed reason = %+v", failed)
	}

	// A queued task is cancelled first when the operator asks again.
	created, failed, err = a.EnqueueAIAnalyses(ctx, []string{resource.ID}, "admin")
	if err != nil || len(created) != 1 || len(failed) != 0 {
		t.Fatalf("re-enqueue = %+v %+v %v", created, failed, err)
	}
	active, ok, err := store.GetActiveAITaskByResource(ctx, resource.ID)
	if err != nil || !ok || active.Status != "queued" {
		t.Fatalf("active task = %+v %v %v", active, ok, err)
	}
	// A paused task blocks a new enqueue with a readable reason.
	if err := store.UpdateAITask(ctx, active.ID, sqlite.AITaskPatch{Status: stringPointer("paused")}); err != nil {
		t.Fatal(err)
	}
	created, failed, err = a.EnqueueAIAnalyses(ctx, []string{resource.ID}, "admin")
	if err != nil || len(created) != 0 || len(failed) != 1 || !strings.Contains(failed[0]["reason"], "暂停") {
		t.Fatalf("enqueue paused = %+v %+v %v", created, failed, err)
	}
}

func stringPointer(value string) *string { return &value }

func TestRetryAndCancelAITasks(t *testing.T) {
	a, store := newAIApp(t)
	ctx := context.Background()
	resource := createAITestResource(t, a, store, "试卷二.pdf")

	if _, err := a.RetryAITask(ctx, "missing", "admin"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retry missing = %v", err)
	}
	if _, err := a.CancelAITask(ctx, "missing", "admin"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancel missing = %v", err)
	}
	task, _, err := store.CreateAITask(ctx, resource.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RetryAITask(ctx, task.ID, "admin"); err == nil {
		t.Fatal("retry queued task should fail")
	}
	// Paused runs resume in place and reset the progress of unstarted runs.
	if err := store.UpdateAITask(ctx, task.ID, sqlite.AITaskPatch{Status: stringPointer("paused")}); err != nil {
		t.Fatal(err)
	}
	resumed, err := a.RetryAITask(ctx, task.ID, "admin")
	if err != nil || resumed.ID != task.ID || resumed.Status != "queued" {
		t.Fatalf("resume = %+v %v", resumed, err)
	}
	// Running runs are rejected while the worker owns them.
	if err := store.UpdateAITask(ctx, task.ID, sqlite.AITaskPatch{Status: stringPointer("running")}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RetryAITask(ctx, task.ID, "admin"); err == nil {
		t.Fatal("retry running task should fail")
	}
	if _, err := a.CancelAITask(ctx, task.ID, "admin"); err != nil {
		t.Fatalf("cancel running = %v", err)
	}
	if _, err := a.CancelAITask(ctx, task.ID, "admin"); err == nil {
		t.Fatal("cancel finished task should fail")
	}
	// A failed task gets a new history row instead of being overwritten.
	if err := store.UpdateAITask(ctx, task.ID, sqlite.AITaskPatch{Status: stringPointer("failed")}); err != nil {
		t.Fatal(err)
	}
	retried, err := a.RetryAITask(ctx, task.ID, "admin")
	if err != nil || retried.ID == task.ID || retried.Status != "queued" {
		t.Fatalf("new retry = %+v %v", retried, err)
	}
}

func TestOpenAITaskOutputVariants(t *testing.T) {
	a, store := newAIApp(t)
	ctx := context.Background()
	resource := createAITestResource(t, a, store, "试卷三.pdf")
	if _, _, err := a.OpenAITaskOutput(ctx, "missing", "basic"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing output = %v", err)
	}
	task, _, err := store.CreateAITask(ctx, resource.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.OpenAITaskOutput(ctx, task.ID, "basic"); err == nil {
		t.Fatal("output before generation should fail")
	}
	if _, _, err := a.OpenAITaskOutput(ctx, task.ID, "weird"); err == nil {
		t.Fatal("invalid mode should fail")
	}
	if err := store.UpdateAITask(ctx, task.ID, sqlite.AITaskPatch{
		Status: stringPointer("succeeded"), OutputKey: stringPointer("ai/papers/one.pdf"),
		MoreOutputKey: stringPointer("ai/papers/two.pdf"),
	}); err != nil {
		t.Fatal(err)
	}
	cosClient := cloud.NewCOSClient("AKIDtest", "secretkey", "bucket-1250000000", "ap-shanghai")
	cosClient.HTTP = &http.Client{Transport: cosRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := "%PDF-basic"
		if strings.Contains(req.URL.Path, "two.pdf") {
			body = "%PDF-more"
		}
		return cosStubResponse(http.StatusOK, body, int64(len(body))), nil
	})}
	a.cosClientOverride = cosClient
	for mode, want := range map[string]string{"": "%PDF-basic", "basic": "%PDF-basic", "more": "%PDF-more"} {
		resp, filename, err := a.OpenAITaskOutput(ctx, task.ID, mode)
		if err != nil {
			t.Fatalf("open %q: %v", mode, err)
		}
		body := readAll(resp)
		if body != want || filename == "" {
			t.Fatalf("open %q = %q %q", mode, body, filename)
		}
	}
}

func TestAIHelperFunctions(t *testing.T) {
	a, store := newAIApp(t)
	ctx := context.Background()
	if aiModeLabel("more") != "小学生版" || aiModeLabel("basic") != "精简版" {
		t.Fatal("aiModeLabel mismatch")
	}
	if aiRetryDelay(1) != aiRetryBaseDelay {
		t.Fatalf("aiRetryDelay(1) = %v", aiRetryDelay(1))
	}
	if aiRetryDelay(4) <= aiRetryDelay(2) {
		t.Fatal("retry delay should grow")
	}
	if aiRetryDelay(99) > time.Hour {
		t.Fatalf("retry delay should stay bounded: %v", aiRetryDelay(99))
	}
	if joinMarkdown("", "") != "" || joinMarkdown("a", "") != "a\n\n" || joinMarkdown("", "b") != "b" {
		t.Fatalf("joinMarkdown empty handling = %q %q %q", joinMarkdown("", ""), joinMarkdown("a", ""), joinMarkdown("", "b"))
	}
	if joined := joinMarkdown("a\n", "b"); joined != "a\n\nb" {
		t.Fatalf("joinMarkdown = %q", joined)
	}
	if reportDocument("试卷", "basic", "内容") == "" || reportDocument("", "more", "") == "" {
		t.Fatal("reportDocument should always render a document")
	}
	if minInt(1, 2) != 1 || minInt(2, 1) != 1 {
		t.Fatal("minInt mismatch")
	}
	if *intPtr(5) != 5 {
		t.Fatal("intPtr mismatch")
	}
	if !a.aiInWindow() {
		t.Fatal("window 00:00-24:00 should be open")
	}
	cfg := a.cfg()
	nowInZone := a.aiNow()
	if nowInZone.Hour() == 0 && nowInZone.Minute() < 2 {
		cfg.AIWindows = "00:02-00:03"
	} else {
		cfg.AIWindows = "00:00-00:01"
	}
	a.SetConfigForTesting(cfg)
	if err := a.initAIAnalysis(); err != nil {
		t.Fatal(err)
	}
	if a.aiInWindow() {
		t.Fatalf("window %q should be closed at %v", cfg.AIWindows, nowInZone)
	}
	cfg.AIWindows = "00:00-24:00"
	a.SetConfigForTesting(cfg)
	if err := a.initAIAnalysis(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if !a.sleepInWindow(ctx, time.Millisecond) {
		t.Fatal("sleepInWindow should finish inside the window")
	}
	if time.Since(start) > time.Second {
		t.Fatal("sleepInWindow took too long")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if a.sleepInWindow(cancelled, time.Hour) {
		t.Fatal("cancelled sleep should stop")
	}

	// Stale work directories are removed, live tasks are kept.
	root := a.cfg().AIWorkDir
	if err := os.MkdirAll(filepath.Join(root, "stale-task"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "live-task"), 0o700); err != nil {
		t.Fatal(err)
	}
	resource := createAITestResource(t, a, store, "试卷四.pdf")
	live, _, err := store.CreateAITask(ctx, resource.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, live.ID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cleanupAIWorkDirs(ctx)
	if _, err := os.Stat(filepath.Join(root, "stale-task")); !os.IsNotExist(err) {
		t.Fatalf("stale dir should be removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, live.ID)); err != nil {
		t.Fatalf("live task dir should stay: %v", err)
	}
	closedCfg := a.cfg()
	closedCfg.AIWorkDir = ""
	a.SetConfigForTesting(closedCfg)
	a.cleanupAIWorkDirs(ctx)
}

func TestAIWindowHelpersUseTimezone(t *testing.T) {
	a, _ := newAIApp(t)
	cfg := a.cfg()
	cfg.AIWindows = "00:00-00:00"
	a.SetConfigForTesting(cfg)
	if err := a.initAIAnalysis(); err == nil {
		t.Fatal("degenerate window should be rejected")
	}
	cfg.AIWindows = "not-a-window"
	a.SetConfigForTesting(cfg)
	if err := a.initAIAnalysis(); err == nil {
		t.Fatal("invalid window should be rejected")
	}
	cfg.AIWindows = "00:00-24:00"
	cfg.AITimezone = "Not/AZone"
	a.SetConfigForTesting(cfg)
	if err := a.initAIAnalysis(); err != nil {
		t.Fatal(err)
	}
	if zone, offset := a.aiNow().Zone(); zone == "" || offset != 8*3600 {
		t.Fatalf("invalid timezone fallback = %q %d", zone, offset)
	}
	if !a.aiInWindow() {
		t.Fatal("all-day window should be open")
	}
}

func TestStartAIAnalysisStopsWithContext(t *testing.T) {
	a, _ := newAIApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		a.StartAIAnalysis(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StartAIAnalysis did not stop with the context")
	}
}

func TestRefreshStatsOnceWithCloudDisabled(t *testing.T) {
	a, _ := newCourseApp(t)
	cfg := a.cfg()
	cfg.COSSecretID = ""
	cfg.COSSecretKey = ""
	cfg.MonitorSecretID = ""
	cfg.MonitorSecretKey = ""
	a.SetConfigForTesting(cfg)
	a.RefreshStatsOnce(context.Background())
}

func TestCDNStatsCacheAndUnavailable(t *testing.T) {
	a, _ := newCourseApp(t)
	ctx := context.Background()
	cfg := a.cfg()
	cfg.COSSecretID = ""
	cfg.COSSecretKey = ""
	a.SetConfigForTesting(cfg)
	if _, err := a.CDNStats(ctx, 30, false); err == nil {
		t.Fatal("cdn without credentials should fail")
	}

	cfg.COSSecretID = "id"
	cfg.COSSecretKey = "key"
	a.SetConfigForTesting(cfg)
	a.cdnMu.Lock()
	a.cdnCache = cdnStatsCache{stats: &cloud.CDNStats{Days: 30}, days: 30, expires: time.Now().Add(time.Hour)}
	a.cdnMu.Unlock()
	cached, err := a.CDNStats(ctx, 30, false)
	if err != nil || cached.Days != 30 || cached.Stale {
		t.Fatalf("cache hit = %+v %v", cached, err)
	}
	// A different day range bypasses the cache and hits the cloud, which fails
	// without network access.
	if _, err := a.CDNStats(ctx, 7, false); err == nil {
		t.Fatal("cache miss should call the provider and fail")
	}
	a.cdnMu.Lock()
	a.cdnCache.expires = time.Now().Add(-time.Minute)
	a.cdnMu.Unlock()
	stale, err := a.CDNStats(ctx, 30, false)
	if err != nil || !stale.Stale {
		t.Fatalf("stale cache = %+v %v", stale, err)
	}
	if _, err := a.CDNStats(ctx, 30, true); err == nil {
		t.Fatal("forced refresh should fail without network")
	}
}

func TestHostStatsCacheAndUnavailable(t *testing.T) {
	a, _ := newCourseApp(t)
	ctx := context.Background()
	cfg := a.cfg()
	cfg.MonitorSecretID = ""
	cfg.MonitorSecretKey = ""
	a.SetConfigForTesting(cfg)
	if _, err := a.HostStats(ctx, 30, false); err == nil {
		t.Fatal("host without credentials should fail")
	}
	cfg.MonitorSecretID = "id"
	cfg.MonitorSecretKey = "key"
	a.SetConfigForTesting(cfg)
	a.hostMu.Lock()
	a.hostCache = hostStatsCache{stats: &cloud.HostStats{Days: 30}, expires: time.Now().Add(time.Hour)}
	a.hostMu.Unlock()
	if stats, err := a.HostStats(ctx, 30, false); err != nil || stats.Days != 30 {
		t.Fatalf("host cache = %+v %v", stats, err)
	}
	if _, err := a.HostStats(ctx, 30, true); err == nil {
		t.Fatal("forced host refresh should fail without metadata service")
	}
}

func TestBucketStatsCacheAndTargets(t *testing.T) {
	a, _ := newCourseApp(t)
	ctx := context.Background()
	cfg := a.cfg()
	cfg.COSSecretID = ""
	cfg.COSSecretKey = ""
	cfg.COSBucket = ""
	cfg.COSWebsiteBucket = ""
	a.SetConfigForTesting(cfg)
	if _, err := a.BucketStats(ctx, 30, false); err == nil {
		t.Fatal("buckets without credentials should fail")
	}
	if bucketLabel("xzitpocket-assets-1330114136") != "xzitpocket-assets" {
		t.Fatalf("bucketLabel = %q", bucketLabel("xzitpocket-assets-1330114136"))
	}
	if bucketLabel("nohyphen") != "nohyphen" || bucketLabel("-leading") != "-leading" {
		t.Fatalf("bucketLabel edge = %q %q", bucketLabel("nohyphen"), bucketLabel("-leading"))
	}
	if len(a.bucketTargets()) != 0 {
		t.Fatalf("no buckets should produce no targets: %+v", a.bucketTargets())
	}
	cfg.COSSecretID, cfg.COSSecretKey = "id", "key"
	cfg.COSBucket, cfg.COSWebsiteBucket = "assets-1", "website-2"
	a.SetConfigForTesting(cfg)
	targets := a.bucketTargets()
	if len(targets) != 2 || targets[0].key != "assets" || targets[1].key != "website" {
		t.Fatalf("targets = %+v", targets)
	}
	a.bucketMu.Lock()
	a.bucketCache = bucketStatsCache{stats: &cloud.BucketStatsResponse{Days: 30}, days: 30, expires: time.Now().Add(time.Hour)}
	a.bucketMu.Unlock()
	if stats, err := a.BucketStats(ctx, 30, false); err != nil || stats.Days != 30 {
		t.Fatalf("bucket cache = %+v %v", stats, err)
	}
	a.bucketMu.Lock()
	a.bucketCache.expires = time.Now().Add(-time.Minute)
	a.bucketMu.Unlock()
	stale, err := a.BucketStats(ctx, 30, false)
	if err != nil || !stale.Stale {
		t.Fatalf("stale buckets = %+v %v", stale, err)
	}
	// A configured bucket without reachable cloud credentials fails cleanly.
	if _, err := a.fetchBucketStats(ctx, 30, time.Now()); err == nil {
		t.Fatal("fetch without network should fail")
	}
}

func readAll(resp *http.Response) string {
	defer resp.Body.Close()
	raw := make([]byte, 0, 64)
	buffer := make([]byte, 32)
	for {
		n, err := resp.Body.Read(buffer)
		raw = append(raw, buffer[:n]...)
		if err != nil {
			return string(raw)
		}
	}
}
