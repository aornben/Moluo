package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildLegacyScaleEnvelope returns a fictional legacy v1 envelope with 300
// chapters and at least one million Han characters. All data is synthetic.
func buildLegacyScaleEnvelope(chapters int, longChapterHan int) map[string]any {
	list := make([]any, 0, chapters)
	for n := 1; n <= chapters; n++ {
		size := 3178
		title := fmt.Sprintf("第%d章", n)
		note := ""
		if n == 1 {
			size = longChapterHan
			note = "保留"
		}
		id := fmt.Sprintf("ch_%03d", n)
		list = append(list, map[string]any{
			"id":          id,
			"title":       title,
			"content":     strings.Repeat("纸", size),
			"customNote":  note,
			"chapterNote": title + "备注",
		})
	}
	return map[string]any{
		"app":           "moluo-novel-manager",
		"format":        "moluo-novel-backup",
		"schemaVersion": 1,
		"revision":      0,
		"savedAt":       "虚构",
		"extraEnvelope": map[string]any{"keep": true},
		"data": map[string]any{
			"schemaVersion": 1,
			"meta":          map[string]any{"title": "虚构：百万字旧作"},
			"characters": []any{
				map[string]any{"id": "char_a", "name": "阿甲", "extra": "保留"},
				map[string]any{"id": "char_b", "name": "阿乙"},
			},
			"chapters": list,
			"plotTasks": []any{
				map[string]any{"id": "task_1", "title": "旧版已完成有证据", "plannedChapter": float64(1), "priority": "high", "status": "done", "characterIds": []any{"char_a"}, "completedChapterId": "ch_001", "completionEvidence": "纸纸纸"},
				map[string]any{"id": "task_2", "title": "旧版已完成无证据", "plannedChapter": float64(2), "priority": "normal", "status": "done", "characterIds": []any{"char_b"}, "completedChapterId": "ch_002", "completionEvidence": "不存在的正文"},
			},
			"extraData": "保留",
		},
	}
}

func writeLegacyEnvelope(t *testing.T, w *workspace, name string, envelope map[string]any) (string, []byte) {
	t.Helper()
	dir := filepath.Join(w.root, "moluo-v4-imports")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path, data
}

func TestLegacyImportScaleIdempotentBackupRepairAndStaleStage(t *testing.T) {
	w := testWorkspace(t)
	// A leftover staging directory from an interrupted prior run must not be
	// treated as a work and must not block a new import.
	stale := filepath.Join(w.works, ".legacy-stale")
	if err := os.MkdirAll(stale, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "partial.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}

	source, sourceBytes := writeLegacyEnvelope(t, w, "fictional-scale-v1.json", buildLegacyScaleEnvelope(300, 50000))
	sourceHash := sumBytes(sourceBytes)

	result, err := w.importLegacy(source)
	if err != nil {
		t.Fatal(err)
	}
	got := result.(map[string]any)
	id := got["workId"].(string)
	if got["chapters"] != 300 || got["legacyUnverifiedTasks"] != 1 {
		t.Fatalf("旧版规模导入结果不正确：%v", got)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatalf("导入后健康检查失败：%v", err)
	}
	if !w.backupExists(id, 1) {
		t.Fatal("导入后缺少独立备份")
	}

	metrics, err := w.metrics(id)
	if err != nil {
		t.Fatal(err)
	}
	m := metrics.(map[string]any)
	if m["chapters"] != 300 {
		t.Fatalf("metrics 章节数错误：%v", m["chapters"])
	}
	if hanzi, _ := m["chapterHanzi"].(int); hanzi < 1000000 {
		t.Fatalf("metrics 汉字数不足：%v", m["chapterHanzi"])
	}

	// Source copy is never modified.
	after, err := os.ReadFile(source)
	if err != nil || sumBytes(after) != sourceHash {
		t.Fatal("旧版源副本被修改")
	}

	root, _, manifest, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.LegacyEnvelopeExtra["extraEnvelope"] == nil || manifest.LegacyDataExtra["extraData"] != "保留" {
		t.Fatal("旧版扩展字段未保留")
	}
	entries, err := w.entries(root, manifest)
	if err != nil || len(entries) != 300 {
		t.Fatalf("章节目录错误：%v %v", len(entries), err)
	}
	var longChapter map[string]any
	if err := loadJSON(root, entries[0].Path, &longChapter); err != nil {
		t.Fatal(err)
	}
	if longChapter["customNote"] != "保留" || len([]rune(longChapter["content"].(string))) != 50000 {
		t.Fatal("长章节或自定义字段未正确导入")
	}

	// Paged readback of the long chapter must be consistent.
	first, err := w.readChapter(id, "ch_001", 0, 8000)
	if err != nil {
		t.Fatal(err)
	}
	page := first.(map[string]any)
	if page["complete"] != false {
		t.Fatal("长章节未分页")
	}
	next := page["nextCursor"].(int)
	if next <= 0 || next > 8000 {
		t.Fatalf("续页游标错误：%d", next)
	}
	second, err := w.readChapter(id, "ch_001", next, 8000)
	if err != nil {
		t.Fatal(err)
	}
	if second.(map[string]any)["cursor"] != next {
		t.Fatal("续页游标回读不一致")
	}

	// A crash after the atomic rename but before backup leaves no snapshot.
	// Re-running the same import must repair the backup instead of claiming a
	// clean idempotent success over an unresumable work.
	if err := os.RemoveAll(w.backups); err != nil {
		t.Fatal(err)
	}
	again, err := w.importLegacy(source)
	if err != nil {
		t.Fatalf("补备份失败：%v", err)
	}
	repaired := again.(map[string]any)
	if repaired["idempotent"] != true || repaired["workId"] != id || repaired["backup"] != "已补齐" {
		t.Fatalf("重复迁移未补齐备份：%v", repaired)
	}
	if !w.backupExists(id, 1) {
		t.Fatal("补备份后仍无快照")
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatalf("补备份后健康检查失败：%v", err)
	}
	third, err := w.importLegacy(source)
	if err != nil || third.(map[string]any)["backup"] != "正常" {
		t.Fatalf("再次迁移状态错误：%v %v", third, err)
	}
}

func TestLegacyImportRejectsCorruptVariants(t *testing.T) {
	w := testWorkspace(t)

	duplicate := buildLegacyScaleEnvelope(2, 10)
	chs := duplicate["data"].(map[string]any)["chapters"].([]any)
	dup := copyMap(chs[0].(map[string]any))
	dup["title"] = "重复 ID"
	duplicate["data"].(map[string]any)["chapters"] = append(chs, dup)
	path, _ := writeLegacyEnvelope(t, w, "dup.json", duplicate)
	if _, err := w.importLegacy(path); err == nil || !strings.Contains(err.Error(), "ID") {
		t.Fatalf("重复章节 ID 未被拒绝：%v", err)
	}

	badVersion := buildLegacyScaleEnvelope(1, 10)
	badVersion["schemaVersion"] = 2
	path, _ = writeLegacyEnvelope(t, w, "bad-version.json", badVersion)
	if _, err := w.importLegacy(path); err == nil || !strings.Contains(err.Error(), "不支持") {
		t.Fatalf("未知 schemaVersion 未被拒绝：%v", err)
	}

	tooMany := buildLegacyScaleEnvelope(1, 10)
	overflow := make([]any, 0, 10000)
	for n := 1; n <= 10000; n++ {
		overflow = append(overflow, map[string]any{"id": fmt.Sprintf("ch_%05d", n), "title": fmt.Sprintf("第%d章", n), "content": "纸"})
	}
	tooMany["data"].(map[string]any)["chapters"] = overflow
	path, _ = writeLegacyEnvelope(t, w, "too-many.json", tooMany)
	if _, err := w.importLegacy(path); err == nil || !strings.Contains(err.Error(), "安全上限") {
		t.Fatalf("超量章节未被拒绝：%v", err)
	}

	broken := filepath.Join(w.root, "moluo-v4-imports", "broken.json")
	if err := os.WriteFile(broken, []byte("{not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.importLegacy(broken); err == nil {
		t.Fatal("损坏 JSON 未被拒绝")
	}

	// Failed imports must not leave staging directories or visible works.
	leftovers, err := filepath.Glob(filepath.Join(w.works, ".legacy-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("失败的导入留下了暂存目录：%v", leftovers)
	}
	works, err := os.ReadDir(w.works)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	visible := 0
	for _, item := range works {
		if item.IsDir() && safeWorkID.MatchString(item.Name()) {
			visible++
		}
	}
	if visible != 0 {
		t.Fatalf("失败的导入产生了可见作品：%d", visible)
	}
}

func TestLegacyImportIdempotentRejectsCorruptedWork(t *testing.T) {
	w := testWorkspace(t)
	source, _ := writeLegacyEnvelope(t, w, "fictional-small-v1.json", buildLegacyScaleEnvelope(2, 10))
	result, err := w.importLegacy(source)
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]any)["workId"].(string)
	root, _, manifest, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := w.entries(root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	chapterPath := filepath.Join(root, filepath.FromSlash(entries[0].Path))
	data, err := os.ReadFile(chapterPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(chapterPath, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.importLegacy(source); err == nil || !strings.Contains(err.Error(), "完整性校验失败") {
		t.Fatalf("重复迁移未拒绝被篡改的已导入作品：%v", err)
	}
}
