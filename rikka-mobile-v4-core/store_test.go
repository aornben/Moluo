package main

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
)

func testWorkspace(t *testing.T) *workspace {
	t.Helper()
	root := t.TempDir()
	w, err := openWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func testInit(t *testing.T, w *workspace) string {
	t.Helper()
	result, err := w.initWork("虚构：纸月城")
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]any)["workId"].(string)
	if !safeWorkID.MatchString(id) {
		t.Fatal("作品 ID 无效")
	}
	return id
}

func testStage(t *testing.T, w *workspace, id, op, chapterID, content string) string {
	t.Helper()
	dir := filepath.Join(w.inbox, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, op+".json")
	d := draft{OperationID: op, Chapter: chapter{ID: chapterID, Title: "虚构章节", Content: content}}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInitAppendBackupRestoreAndIdempotence(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
	op := "11111111111111111111111111111111"
	input := testStage(t, w, id, op, "ch_1", strings.Repeat("纸月城的风。", 2000))
	result, err := w.appendFromFile(id, input, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["revision"] != 2 {
		t.Fatal("提交版本不正确")
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
	listing, err := w.query(id, "chapters", "虚构", "", 0, 20, 0)
	if err != nil || listing.(map[string]any)["total"] != 1 {
		t.Fatalf("分页目录查询失败：%v %v", listing, err)
	}
	if _, err := w.query(id, "memory", "", "", 0, 1, 0); err != nil {
		t.Fatal(err)
	}
	first, err := w.readChapter(id, "ch_1", 0, 8000)
	if err != nil {
		t.Fatal(err)
	}
	page := first.(map[string]any)
	if page["complete"] != false || page["nextCursor"].(int) > 8000 {
		t.Fatal("正文分页失效")
	}
	second, err := w.readChapter(id, "ch_1", page["nextCursor"].(int), 8000)
	if err != nil {
		t.Fatal(err)
	}
	if second.(map[string]any)["cursor"] != page["nextCursor"] {
		t.Fatal("续页游标错误")
	}
	again, err := w.appendFromFile(id, input, 1)
	if err != nil {
		t.Fatal(err)
	}
	if again.(map[string]any)["idempotent"] != true {
		t.Fatal("重复操作没有返回幂等结果")
	}
	other := testStage(t, w, id, "22222222222222222222222222222222", "ch_2", "虚构第二章。")
	if _, err := w.appendFromFile(id, other, 1); err == nil || !strings.Contains(err.Error(), "版本冲突") {
		t.Fatal("旧 revision 未被拒绝")
	}
	root, err := w.workRoot(id)
	if err != nil {
		t.Fatal(err)
	}
	// 只删除 t.TempDir() 内的虚构作品目录；备份位于独立兄弟目录。
	rel, err := filepath.Rel(w.root, root)
	if err != nil || rel != filepath.Join("moluo-v4-works", id) {
		t.Fatal("测试删除路径不在隔离范围")
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if _, err := w.restore(id, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.restore(id, 2); err == nil {
		t.Fatal("恢复覆盖了现存作品")
	}
}

func TestLegacyImportIsCopyOnlyAndMarksUnverifiedCompletion(t *testing.T) {
	w := testWorkspace(t)
	if err := os.MkdirAll(filepath.Join(w.root, "moluo-v4-imports"), 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(w.root, "moluo-v4-imports", "fictional-v3.json")
	legacy := map[string]any{"app": "moluo-novel-manager", "format": "moluo-novel-backup", "schemaVersion": 1, "revision": 0, "savedAt": "fictional", "extraEnvelope": map[string]any{"keep": true}, "data": map[string]any{"schemaVersion": 1, "meta": map[string]any{"title": "虚构旧作"}, "characters": []any{map[string]any{"id": "char_a", "name": "阿甲", "extra": "保留"}}, "chapters": []any{map[string]any{"id": "ch_1", "title": "第一章", "content": "虚构旧章。", "customNote": "保留"}}, "plotTasks": []any{map[string]any{"id": "task_1", "title": "旧版已完成", "plannedChapter": float64(1), "priority": "high", "status": "done", "characterIds": []any{"char_a"}}}, "extraData": "保留"}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := w.importLegacy(source)
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]any)["workId"].(string)
	if result.(map[string]any)["legacyUnverifiedTasks"] != 1 {
		t.Fatal("旧版无证据核销未标记")
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(source)
	if err != nil || string(after) != string(data) {
		t.Fatal("旧版源副本被修改")
	}
	root, _, m, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := w.entries(root, m)
	if err != nil {
		t.Fatal(err)
	}
	var ch map[string]any
	if err := loadJSON(root, entries[0].Path, &ch); err != nil {
		t.Fatal(err)
	}
	if ch["customNote"] != "保留" || m.LegacyDataExtra["extraData"] != "保留" {
		t.Fatal("旧版自定义字段丢失")
	}
	exported, err := w.exportLegacy(id)
	if err != nil {
		t.Fatal(err)
	}
	exportPath := exported.(map[string]any)["path"].(string)
	exportBytes, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	var oldRoundTrip map[string]any
	if err := json.Unmarshal(exportBytes, &oldRoundTrip); err != nil {
		t.Fatal(err)
	}
	if oldRoundTrip["extraEnvelope"].(map[string]any)["keep"] != true || oldRoundTrip["data"].(map[string]any)["extraData"] != "保留" {
		t.Fatal("旧版导出丢失扩展字段")
	}
	again, err := w.importLegacy(source)
	if err != nil || again.(map[string]any)["workId"] != id || again.(map[string]any)["idempotent"] != true {
		t.Fatal("重复迁移未安全复用作品 ID", err)
	}
}

func TestConfirmedChapterReplacementKeepsHistoryAndRebuildsEvidence(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	first := testStage(t, w, id, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "ch_1", "阿甲在旧桥捡到钥匙。")
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	var d draft
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	d.Card = map[string]any{"chapterId": "ch_1", "facts": []any{map[string]any{"id": "fact_key", "sourceChapterId": "ch_1", "certainty": "certain", "evidence": "阿甲在旧桥捡到钥匙。"}}}
	updated, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, updated, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.appendFromFile(id, first, 1); err != nil {
		t.Fatal(err)
	}
	root, _, m, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	oldEntries, err := w.entries(root, m)
	if err != nil {
		t.Fatal(err)
	}
	oldPath := oldEntries[0].Path
	oldSHA := oldEntries[0].SHA256
	second := testStage(t, w, id, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "ch_1", "阿甲在新桥放下钥匙。")
	data, err = os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	d.Mode = "replace"
	d.Confirm = true
	d.ExpectedChapterSHA256 = oldSHA
	d.Card = map[string]any{"chapterId": "ch_1", "facts": []any{map[string]any{"id": "fact_key", "sourceChapterId": "ch_1", "certainty": "certain", "evidence": "阿甲在新桥放下钥匙。"}}}
	updated, err = json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, updated, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.appendFromFile(id, second, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
	_, _, m, err = w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := w.entries(root, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Path == oldPath {
		t.Fatal("替换后章节目录或历史错误")
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(oldPath))); err != nil {
		t.Fatal("旧正文丢失", err)
	}
	var memory map[string]any
	if err := loadJSON(root, m.MemoryPath, &memory); err != nil {
		t.Fatal(err)
	}
	fact := memory["activeFacts"].([]any)[0].(map[string]any)
	if fact["evidence"] != "阿甲在新桥放下钥匙。" {
		t.Fatal("替换后的记忆没有重建")
	}
	third := testStage(t, w, id, "cccccccccccccccccccccccccccccccc", "ch_1", "伪造替换。")
	data, err = os.ReadFile(third)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	d.Mode = "replace"
	d.Confirm = true
	d.ExpectedChapterSHA256 = oldSHA
	updated, err = json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(third, updated, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.appendFromFile(id, third, 3); err == nil {
		t.Fatal("过期原章哈希未阻止替换")
	}
}

func TestScale300ChaptersOneMillionHanzi(t *testing.T) {
	if os.Getenv("MOLUO_STRESS") != "1" {
		t.Skip("单独设置 MOLUO_STRESS=1 运行 300 章真实写入压力测试")
	}
	w := testWorkspace(t)
	// Keep the 600 active tasks in the same work as the 301 chapters. The first
	// twenty deliberately have long titles so a requested 20-item page exceeds
	// 32 KiB and the real query path must shrink it without loss or duplication.
	importDir := filepath.Join(w.root, "moluo-v4-imports")
	if err := os.MkdirAll(importDir, 0700); err != nil {
		t.Fatal(err)
	}
	tasks := make([]any, 0, 600)
	for n := 1; n <= 600; n++ {
		title := fmt.Sprintf("虚构活动待办%d", n)
		if n <= 20 {
			title += strings.Repeat("长", 700)
		}
		tasks = append(tasks, map[string]any{
			"id": fmt.Sprintf("scale_task_%03d", n), "title": title,
			"plannedChapter": float64(n%301 + 1), "priority": "normal",
			"status": "todo", "characterIds": []any{},
		})
	}
	legacy := map[string]any{
		"app": "moluo-novel-manager", "format": "moluo-novel-backup",
		"schemaVersion": 1, "revision": 1,
		"data": map[string]any{
			"schemaVersion": 1, "meta": map[string]any{"title": "虚构百万汉字压力作品"},
			"characters": []any{}, "chapters": []any{}, "plotTasks": tasks,
		},
	}
	legacyData, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(importDir, "scale-600-tasks.json")
	if err := os.WriteFile(legacyPath, legacyData, 0600); err != nil {
		t.Fatal(err)
	}
	imported, err := w.importLegacy(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	id := imported.(map[string]any)["workId"].(string)
	start := time.Now()
	body := strings.Repeat("山", 3339)
	markers := map[int]string{10: "远古铜钥匙", 120: "霜月银钥匙", 250: "北塔金钥匙", 301: "终章铁钥匙"}
	expectedHanzi, expectedRunes, expectedBytes := 0, 0, 0
	longChapter := ""
	for n := 1; n <= 301; n++ {
		op := fmt.Sprintf("%032x", n)
		chapterBody := body
		if n == 1 {
			chapterBody += " Alpha，！？"
		}
		if n == 150 {
			chapterBody = strings.Repeat("山", 50000) + " LONG-CHAPTER。"
			longChapter = chapterBody
		}
		if n == 200 {
			// U+20000 is an extension-B Han character encoded with four UTF-8
			// bytes. It catches implementations that assume all Han are 3 bytes.
			chapterBody = "𠀀" + strings.Repeat("山", 3338)
		}
		if marker := markers[n]; marker != "" {
			chapterBody = marker + strings.Repeat("山", 3339-len([]rune(marker)))
		}
		for _, value := range chapterBody {
			expectedRunes++
			if unicode.Is(unicode.Han, value) {
				expectedHanzi++
			}
		}
		expectedBytes += len([]byte(chapterBody))
		input := testStage(t, w, id, op, fmt.Sprintf("ch_%d", n), chapterBody)
		if markers[n] != "" {
			data, err := os.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}
			var d draft
			if err := json.Unmarshal(data, &d); err != nil {
				t.Fatal(err)
			}
			d.Card = map[string]any{"chapterId": d.Chapter.ID, "facts": []any{map[string]any{"id": "fact_key", "sourceChapterId": d.Chapter.ID, "certainty": "certain", "evidence": markers[n]}}}
			if n != 301 {
				d.Card["hookChanges"] = []any{map[string]any{"id": fmt.Sprintf("hook_%d", n), "status": "open", "openedChapterId": d.Chapter.ID, "evidence": markers[n]}}
			}
			updated, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(input, updated, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := w.appendFromFile(id, input, n); err != nil {
			t.Fatalf("第 %d 章写入失败：%v", n, err)
		}
		if n == 100 || n == 200 || n == 300 {
			t.Logf("chapters=%d hanzi=%d elapsed=%s", n, n*3339, time.Since(start).Round(time.Millisecond))
		}
	}
	status, err := w.status(id)
	if err != nil {
		t.Fatal(err)
	}
	if status.(map[string]any)["chapters"] != 301 {
		t.Fatal("章节数错误")
	}
	root, _, manifest, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := w.entries(root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	diskHanzi, diskRunes, diskBytes := 0, 0, 0
	for _, entry := range entries {
		actualSHA, err := fileSHA(root, entry.Path)
		if err != nil || actualSHA != entry.SHA256 {
			t.Fatalf("磁盘章节 SHA 不符：%s：%v", entry.ID, err)
		}
		chapter, err := loadChapter(root, entry.Path)
		if err != nil {
			t.Fatal(err)
		}
		for _, runeValue := range chapter.Content {
			diskRunes++
			if unicode.Is(unicode.Han, runeValue) {
				diskHanzi++
			}
		}
		diskBytes += len([]byte(chapter.Content))
	}
	if diskHanzi != expectedHanzi || diskRunes != expectedRunes || diskBytes != expectedBytes || diskHanzi < 1000000 {
		t.Fatalf("独立磁盘正文复核失败：汉字=%d Unicode码点=%d UTF-8字节=%d", diskHanzi, diskRunes, diskBytes)
	}
	t.Logf("diskRecheck chapters=%d hanzi=%d unicode=%d utf8Bytes=%d", len(entries), diskHanzi, diskRunes, diskBytes)
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
	listing, err := w.query(id, "chapters", "", "", 281, 20, 0)
	if err != nil || listing.(map[string]any)["total"] != 301 || listing.(map[string]any)["complete"] != true {
		t.Fatal("300章分页查询失败", err)
	}
	chapterCursor := 0
	var rebuilt strings.Builder
	chapterPages := 0
	for {
		page, err := w.readChapter(id, "ch_150", chapterCursor, 8192)
		if err != nil {
			t.Fatal(err)
		}
		result := page.(map[string]any)
		text := result["text"].(string)
		if len([]byte(text)) > 8192 {
			t.Fatal("超长章分页超过字节上限")
		}
		rebuilt.WriteString(text)
		chapterPages++
		if result["complete"] == true {
			break
		}
		next := result["nextCursor"].(int)
		if next <= chapterCursor {
			t.Fatal("超长章分页游标没有前进")
		}
		chapterCursor = next
	}
	if rebuilt.String() != longChapter || chapterPages < 2 {
		t.Fatal("超长章分页回读丢失、重复或没有真正分页")
	}
	hooks, err := w.query(id, "memory", "hook_", "", 0, 20, 0)
	if err != nil || hooks.(map[string]any)["total"] != 3 {
		t.Fatal("第10/120/250章的三个未解伏笔未同时检索到", err)
	}
	if len(manifest.MemoryArchives) != 3 {
		t.Fatalf("三条旧事实未分别归入三个历史归档：%d", len(manifest.MemoryArchives))
	}
	for _, number := range []int{10, 120, 250} {
		result, err := w.queryFiltered(id, "memory", markers[number], "", 0, 20, 0, number, number, "", "", "")
		if err != nil || result.(map[string]any)["total"].(int) < 1 || result.(map[string]any)["archivesRead"] != 1 || result.(map[string]any)["archivesSkipped"] != 2 {
			t.Fatalf("第301章未找回第%d章历史事实：%v %v", number, result, err)
		}
	}

	// Read all 600 active tasks through the bounded query path. At least one
	// page must be auto-shrunk from 20 records because its JSON exceeds 32 KiB.
	taskCursor, taskCalls, taskBytes := 0, 0, 0
	seenTasks := map[string]bool{}
	shrunk := false
	for {
		result, err := w.query(id, "tasks", "", "", taskCursor, 20, 0)
		if err != nil {
			t.Fatal(err)
		}
		page := result.(map[string]any)
		items := page["items"].([]any)
		if len(items) < 20 && page["complete"] != true {
			shrunk = true
		}
		if page["outputBytes"].(int) > 32768 {
			t.Fatal("待办查询输出超过 32 KiB")
		}
		for _, raw := range items {
			task := raw.(map[string]any)
			taskID := task["id"].(string)
			if seenTasks[taskID] {
				t.Fatalf("待办续页重复：%s", taskID)
			}
			seenTasks[taskID] = true
		}
		taskCalls++
		taskBytes += page["outputBytes"].(int)
		if page["complete"] == true {
			break
		}
		next := page["nextCursor"].(int)
		if next <= taskCursor {
			t.Fatal("待办续页游标没有前进")
		}
		taskCursor = next
	}
	if len(seenTasks) != 600 || !shrunk {
		t.Fatalf("600 待办分页不完整或未触发 32 KiB 自动缩页：count=%d shrunk=%v", len(seenTasks), shrunk)
	}

	metricsResult, err := w.metrics(id)
	if err != nil {
		t.Fatal(err)
	}
	metric := metricsResult.(map[string]any)
	diskMetricBytes, ok := metric["diskBytes"].(int64)
	if !ok {
		t.Fatalf("metrics diskBytes 类型错误：%T", metric["diskBytes"])
	}
	if intValue(metric["chapters"]) != 301 || intValue(metric["chapterHanzi"]) != diskHanzi ||
		intValue(metric["chapterUTF8Bytes"]) != diskBytes || intValue(metric["tasksActive"]) != 600 ||
		intValue(metric["tasksPending"]) != 600 || intValue(metric["memoryArchives"]) != 3 ||
		intValue(metric["diskFiles"]) < 301 || diskMetricBytes <= int64(diskBytes) {
		t.Fatalf("metrics 与独立复算不一致：%v", metric)
	}
	t.Logf("tasks=600 calls=%d outputBytes=%d diskFiles=%v diskBytes=%v elapsed=%s",
		taskCalls, taskBytes, metric["diskFiles"], metric["diskBytes"], time.Since(start).Round(time.Millisecond))

	// Export the complete large work, copy the archive into a distinct
	// workspace, remove the source temp workspace, then restore and re-check.
	sourceRoot, _ := w.workRoot(id)
	wanted := snapshotWorkFiles(t, sourceRoot)
	exported, err := w.exportWork(id)
	if err != nil {
		t.Fatal(err)
	}
	exportBytes, err := os.ReadFile(exported.(map[string]any)["path"].(string))
	if err != nil {
		t.Fatal(err)
	}
	restored := testWorkspace(t)
	importPath := copyExportIntoWorkspace(t, restored, exportBytes, "scale-independent.zip")
	if err := os.RemoveAll(w.root); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.importWork(importPath); err != nil {
		t.Fatalf("百万汉字作品在全新工作区恢复失败：%v", err)
	}
	assertRestoredWork(t, restored, id, 302, wanted)
	restoredMetric, err := restored.metrics(id)
	if err != nil {
		t.Fatal(err)
	}
	got := restoredMetric.(map[string]any)
	if intValue(got["chapterHanzi"]) != diskHanzi || intValue(got["chapterUTF8Bytes"]) != diskBytes || intValue(got["tasksPending"]) != 600 {
		t.Fatalf("全新工作区恢复后统计不一致：%v", got)
	}
}

func TestHistoricalMemoryArchiveKeepsCurrentFactsAndDetectsDamage(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	for n := 1; n <= 54; n++ {
		op := fmt.Sprintf("%032x", n+1000)
		body := fmt.Sprintf("第%d章虚构正文。", n)
		if n == 1 {
			body = "旧城门藏着铜钥匙。"
		}
		if n == 2 {
			body = "新城门藏着铜钥匙。"
		}
		if n == 3 {
			body = "北塔门藏着铜钥匙。"
		}
		if n == 4 {
			body = "东塔门藏着铜钥匙。"
		}
		input := testStage(t, w, id, op, fmt.Sprintf("ch_%d", n), body)
		if n <= 4 {
			data, err := os.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}
			var d draft
			if err := json.Unmarshal(data, &d); err != nil {
				t.Fatal(err)
			}
			d.Card = map[string]any{"chapterId": d.Chapter.ID, "facts": []any{map[string]any{"id": "fact_key", "sourceChapterId": d.Chapter.ID, "certainty": "certain", "evidence": body}}}
			if n == 1 {
				d.Card["hookChanges"] = []any{map[string]any{"id": "hook_1", "status": "open", "openedChapterId": "ch_1", "evidence": body}}
			}
			updated, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(input, updated, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := w.appendFromFile(id, input, n); err != nil {
			t.Fatalf("第%d章失败：%v", n, err)
		}
	}
	root, h, m, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.MemoryArchives) != 3 {
		t.Fatal("第51～53章没有把三个旧事实分别归档")
	}
	result, err := w.query(id, "memory", "旧城门", "", 0, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["total"].(int) < 1 {
		t.Fatal("跨归档旧事实无法检索")
	}
	oldRange, err := w.queryFiltered(id, "memory", "旧城门", "", 0, 20, 0, 1, 1, "", "", "")
	if err != nil || oldRange.(map[string]any)["total"] != 2 || oldRange.(map[string]any)["archivesRead"] != 1 || oldRange.(map[string]any)["archivesSkipped"] != 2 {
		t.Fatalf("归档内原文范围检索失败：%v %v", oldRange, err)
	}
	newRange, err := w.queryFiltered(id, "memory", "新城门", "", 0, 20, 0, 2, 2, "", "", "")
	if err != nil || newRange.(map[string]any)["total"] != 1 || newRange.(map[string]any)["archivesRead"] != 1 || newRange.(map[string]any)["archivesSkipped"] != 2 {
		t.Fatalf("当前记忆范围检索或归档跳过失败：%v %v", newRange, err)
	}
	wrongRange, err := w.queryFiltered(id, "memory", "旧城门", "", 0, 20, 0, 2, 2, "", "", "")
	if err != nil || wrongRange.(map[string]any)["total"] != 0 || wrongRange.(map[string]any)["archivesRead"] != 1 || wrongRange.(map[string]any)["archivesSkipped"] != 2 {
		t.Fatalf("记忆范围未排除旧章证据：%v %v", wrongRange, err)
	}
	for number, phrase := range []string{"旧城门", "新城门", "北塔门"} {
		scoped, err := w.queryFiltered(id, "memory", phrase, "", 0, 20, 0, number+1, number+1, "", "", "")
		if err != nil || scoped.(map[string]any)["total"].(int) < 1 || scoped.(map[string]any)["archivesRead"] != 1 || scoped.(map[string]any)["archivesSkipped"] != 2 {
			t.Fatalf("跨三个归档的第%d章证据遗漏：%v %v", number+1, scoped, err)
		}
	}
	hooks, err := w.query(id, "memory", "hook_1", "", 0, 20, 0)
	if err != nil || hooks.(map[string]any)["total"] != 1 {
		t.Fatal("未解伏笔因归档丢失", err)
	}
	openHooks, err := w.queryFiltered(id, "memory", "hook_1", "", 0, 20, 0, 1, 1, "open", "", "")
	if err != nil || openHooks.(map[string]any)["total"] != 1 {
		t.Fatalf("伏笔状态与章节组合检索失败：%v %v", openHooks, err)
	}
	prepared, err := w.prepareChapter(id, "ch_55", 55, h.Revision)
	if err != nil {
		t.Fatal(err)
	}
	coverage := prepared.(map[string]any)["coverage"].(prepareCoverage)
	if !coverage.Complete || len(coverage.ArchivesRead) != len(m.MemoryArchives) {
		t.Fatalf("prepare 未完整读取跨归档：%v", coverage)
	}
	current, err := w.query(id, "memory", "新城门", "", 0, 20, 0)
	if err != nil || current.(map[string]any)["total"] != 1 {
		t.Fatal("当前事实丢失", err)
	}
	originalManifest, err := readFile(root, h.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	originalHead, err := readFile(root, "HEAD.json")
	if err != nil {
		t.Fatal(err)
	}
	m.MemoryArchives[1]["fromChapter"] = 1
	m.MemoryArchives[1]["throughChapter"] = 1
	if err := atomicJSON(root, h.Manifest, m); err != nil {
		t.Fatal(err)
	}
	h.ManifestSHA256, err = fileSHA(root, h.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(root, "HEAD.json", h); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctorCore(id, false); err == nil || !strings.Contains(err.Error(), "归档章节范围") {
		t.Fatalf("错误归档索引没有被健康检查发现：%v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(h.Manifest)), originalManifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "HEAD.json"), originalHead, 0600); err != nil {
		t.Fatal(err)
	}
	path := m.MemoryArchives[0]["path"].(string)
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(path)), []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctor(id); err == nil || !strings.Contains(err.Error(), "归档") {
		t.Fatal("损坏历史归档未被检测")
	}
}

func TestWorkPathIsolationAndSymlinkRejection(t *testing.T) {
	w := testWorkspace(t)
	first := testInit(t, w)
	other := testInit(t, w)
	input := testStage(t, w, other, "dddddddddddddddddddddddddddddddd", "ch_1", "虚构正文。")
	if _, err := w.appendFromFile(first, input, 1); err == nil || !strings.Contains(err.Error(), "隔离 inbox") {
		t.Fatal("跨作品候选路径未拒绝")
	}
	if _, err := safePath(w.root, "../outside.json"); err == nil {
		t.Fatal("路径越界未拒绝")
	}
	link := filepath.Join(w.root, "shortcut")
	if err := os.Symlink(filepath.Join(w.root, "moluo-v4-works"), link); err == nil {
		if _, err := safePath(w.root, "shortcut/file.json"); err == nil {
			t.Fatal("符号链接未拒绝")
		}
	}
}

func TestManifestHashStopsTamperedIndex(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	root, h, _, err := w.load(id)
	if err != nil || h.ManifestSHA256 == "" {
		t.Fatalf("新作品 HEAD 缺少 manifest 哈希：%v", err)
	}
	path := filepath.Join(root, filepath.FromSlash(h.Manifest))
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(append([]byte{}, original...), ' '), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.status(id); err == nil || !strings.Contains(err.Error(), "manifest 哈希") {
		t.Fatalf("外部篡改 manifest 后没有停写：%v", err)
	}
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatalf("恢复原字节后健康检查失败：%v", err)
	}
	h.ManifestSHA256 = "" // 模拟旧版未封存的 HEAD；旧作品仍须可读。
	if err := atomicJSON(root, "HEAD.json", h); err != nil {
		t.Fatal(err)
	}
	status, err := w.status(id)
	if err != nil || status.(map[string]any)["manifestSealed"] != false {
		t.Fatalf("旧版 HEAD 兼容或未封存状态提示失败：%v %v", status, err)
	}
}

func TestStatusReportsScopeAndRejectsTamperedTasks(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	result, err := w.status(id)
	if err != nil {
		t.Fatal(err)
	}
	state := result.(map[string]any)
	if state["pendingTasks"] != 0 || state["memoryArchives"] != 0 || state["backupCurrent"] != true ||
		state["manifestSealed"] != true || state["lastBackupAt"] == "" || state["lastSavedAt"] == "" ||
		state["skillLoaded"] != "未检查" || state["toolPermissions"] != "未检查" {
		t.Fatalf("状态诊断缺少真实范围或时间：%v", state)
	}
	root, _, m, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(m.TasksPath)), []byte("[] "), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.status(id); err == nil || !strings.Contains(err.Error(), "待办索引哈希") {
		t.Fatalf("篡改待办后状态诊断仍报告正常：%v", err)
	}
}

func TestSixHundredPendingTasksArePaginated(t *testing.T) {
	w := testWorkspace(t)
	dir := filepath.Join(w.root, "moluo-v4-imports")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	tasks := make([]any, 0, 600)
	for n := 1; n <= 600; n++ {
		tasks = append(tasks, map[string]any{"id": fmt.Sprintf("task_%d", n), "title": fmt.Sprintf("虚构待办%d", n), "plannedChapter": float64(n%300 + 1), "priority": "normal", "status": "todo", "characterIds": []any{}})
	}
	legacy := map[string]any{"app": "moluo-novel-manager", "format": "moluo-novel-backup", "schemaVersion": 1, "revision": 1, "data": map[string]any{"schemaVersion": 1, "meta": map[string]any{"title": "虚构600任务"}, "characters": []any{}, "chapters": []any{}, "plotTasks": tasks}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "600-fictional.json")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := w.importLegacy(source)
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]any)["workId"].(string)
	start := time.Now()
	cursor := 0
	read := 0
	calls := 0
	bytes := 0
	for {
		page, err := w.query(id, "tasks", "", "", cursor, 20, 0)
		if err != nil {
			t.Fatal(err)
		}
		p := page.(map[string]any)
		items := p["items"].([]any)
		read += len(items)
		calls++
		bytes += p["outputBytes"].(int)
		if p["outputBytes"].(int) > 32768 {
			t.Fatal("分页输出过长")
		}
		if p["complete"] == true {
			break
		}
		cursor = p["nextCursor"].(int)
	}
	if read != 600 || calls != 30 {
		t.Fatal("600任务续页丢失", read, calls)
	}
	t.Logf("pending=600 calls=%d outputBytes=%d elapsed=%s", calls, bytes, time.Since(start).Round(time.Millisecond))
}

func TestWorldEntityCanonRequiresChapterEvidence(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	input := testStage(t, w, id, "81818181818181818181818181818181", "ch_1", "阿甲把铜钥匙留在旧城门。")
	if _, err := w.appendFromFile(id, input, 1); err != nil {
		t.Fatal(err)
	}
	bad := testRecordStage(t, w, id, "82828282828282828282828282828282", "entity-add", map[string]any{"id": "item_key", "kind": "item", "name": "铜钥匙", "status": "canon", "sourceChapterId": "ch_1", "evidence": "正文没有的句子"})
	if _, err := w.mutateFromFile(id, bad, 2); err == nil {
		t.Fatal("无正文证据的正史实体被写入")
	}
	good := testRecordStage(t, w, id, "83838383838383838383838383838383", "entity-add", map[string]any{"id": "item_key", "kind": "item", "name": "铜钥匙", "status": "canon", "owner": "阿甲", "location": "旧城门", "sourceChapterId": "ch_1", "evidence": "阿甲把铜钥匙留在旧城门。"})
	if _, err := w.mutateFromFile(id, good, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
	found, err := w.query(id, "entities", "铜钥匙", "", 0, 20, 0)
	if err != nil || found.(map[string]any)["total"] != 1 {
		t.Fatal("世界实体查询失败", err)
	}
	volume := testRecordStage(t, w, id, "84848484848484848484848484848484", "entity-add", map[string]any{"id": "vol_1", "kind": "volume", "name": "虚构第一卷", "status": "plan"})
	if _, err := w.mutateFromFile(id, volume, 3); err != nil {
		t.Fatal(err)
	}
	second := testStage(t, w, id, "85858585858585858585858585858585", "ch_2", "第二卷前的虚构正文。")
	data, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	var d draft
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	d.Chapter.VolumeID = "vol_1"
	encoded, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.appendFromFile(id, second, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
}

func TestSegmentedDraftResumeAndFinalCommit(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	op := "86868686868686868686868686868686"
	base := filepath.Join(w.inbox, id)
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	for n, part := range []string{"第一段虚构正文。", "第二段继续写作。"} {
		index := n + 1
		path := filepath.Join(base, fmt.Sprintf("%s-s%04d.txt", op, index))
		if err := os.WriteFile(path, []byte(part), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := w.stageSegment(id, op, path, 1, index); err != nil {
			t.Fatal(err)
		}
		again, err := w.stageSegment(id, op, path, 1, index)
		if err != nil || again.(map[string]any)["idempotent"] != true {
			t.Fatal("分段重试未幂等", err)
		}
	}
	if _, _, err := w.collectSegments(id, op, 3); err == nil {
		t.Fatal("缺失第三段未被识别")
	}
	changed := filepath.Join(base, fmt.Sprintf("%s-s%04d.txt", op, 2))
	if err := os.WriteFile(changed, []byte("被篡改的候选段。"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.stageSegment(id, op, changed, 1, 2); err == nil {
		t.Fatal("已保存分段被覆盖")
	}
	metadata := draft{OperationID: op, SegmentCount: 2, Chapter: chapter{ID: "ch_1", Title: "虚构分段章"}}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(base, op+".json")
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.appendFromFile(id, input, 1); err != nil {
		t.Fatal(err)
	}
	read, err := w.readChapter(id, "ch_1", 0, 8192)
	if err != nil {
		t.Fatal(err)
	}
	if read.(map[string]any)["text"] != "第一段虚构正文。第二段继续写作。" {
		t.Fatal("分段拼接丢字")
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
}

func TestContradictoryDeadlineStopsBeforeTransaction(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	for n, due := range []string{"2026-09-22", "2026-11-06"} {
		number := n + 1
		op := fmt.Sprintf("%032x", number+9000)
		input := testStage(t, w, id, op, fmt.Sprintf("ch_%d", number), "虚构账单日期。")
		data, err := os.ReadFile(input)
		if err != nil {
			t.Fatal(err)
		}
		var d draft
		if err := json.Unmarshal(data, &d); err != nil {
			t.Fatal(err)
		}
		d.Card = map[string]any{"chapterId": d.Chapter.ID, "constraints": []any{map[string]any{"id": fmt.Sprintf("deadline_%d", number), "kind": "deadline", "subject": "虚构医疗账单", "dueDate": due, "sourceChapterId": d.Chapter.ID, "evidence": "虚构账单日期。"}}}
		encoded, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(input, encoded, 0600); err != nil {
			t.Fatal(err)
		}
		_, err = w.appendFromFile(id, input, number)
		if number == 1 {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "矛盾") {
			t.Fatal("冲突截止日期没有提前拒绝", err)
		}
	}
	root, h, _, err := w.load(id)
	if err != nil || h.Revision != 2 {
		t.Fatal("错误日期改变了 HEAD", err)
	}
	if _, err := os.Stat(filepath.Join(root, "transactions", fmt.Sprintf("%032x.json", 9002))); !os.IsNotExist(err) {
		t.Fatal("错误日期留下阻塞事务", err)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
}

func TestOldV4SevenChapterReadOnlyCopyMigration(t *testing.T) {
	w := testWorkspace(t)
	oldID := testInit(t, w)
	for n := 1; n <= 7; n++ {
		input := testStage(t, w, oldID, fmt.Sprintf("%032x", n+11000), fmt.Sprintf("ch_%d", n), fmt.Sprintf("第%d章虚构旧正文。", n))
		if _, err := w.appendFromFile(oldID, input, n); err != nil {
			t.Fatal(err)
		}
	}
	oldRoot, _, _, err := w.load(oldID)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(w.root, "moluo-v4")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(oldRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(oldRoot, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return newFile(source, filepath.ToSlash(rel), data)
	})
	if err != nil {
		t.Fatal(err)
	}
	var h head
	if err := loadJSON(source, "HEAD.json", &h); err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := loadJSON(source, h.Manifest, &m); err != nil {
		t.Fatal(err)
	}
	h.WorkID = ""
	h.ManifestSHA256 = "" // 旧 v4 数据没有新版本的 manifest 绑定字段。
	m.WorkID = ""
	delete(m.ContentHashes, m.EntitiesPath)
	m.EntitiesPath = ""
	if err := atomicJSON(source, h.Manifest, m); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(source, "HEAD.json", h); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(source, "work.json")); err != nil {
		t.Fatal(err)
	}
	before, err := fileSHA(source, "HEAD.json")
	if err != nil {
		t.Fatal(err)
	}
	result, err := w.importOldV4()
	if err != nil {
		t.Fatal(err)
	}
	newID := result.(map[string]any)["workId"].(string)
	if newID == oldID || result.(map[string]any)["chapters"] != 7 {
		t.Fatal("旧v4迁移作品 ID 或章节数不正确")
	}
	if _, err := w.doctor(newID); err != nil {
		t.Fatal(err)
	}
	after, err := fileSHA(source, "HEAD.json")
	if err != nil || before != after {
		t.Fatal("旧v4源目录被修改")
	}
	again, err := w.importOldV4()
	if err != nil || again.(map[string]any)["workId"] != newID || again.(map[string]any)["idempotent"] != true {
		t.Fatal("旧v4重复导入未幂等", err)
	}
}

func TestOldV4ArchivedMemoryCopyPreservesHookAndEvidence(t *testing.T) {
	w := testWorkspace(t)
	oldID := testInit(t, w)
	first := testStage(t, w, oldID, "abababababababababababababababac", "ch_1", "旧门下埋着虚构铁盒。")
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	var d draft
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	fact := map[string]any{"id": "fact_old", "sourceChapterId": "ch_1", "certainty": "certain", "evidence": "旧门下埋着虚构铁盒。"}
	hook := map[string]any{"id": "hook_old", "status": "open", "openedChapterId": "ch_1", "evidence": "旧门下埋着虚构铁盒。"}
	d.Card = map[string]any{"chapterId": "ch_1", "facts": []any{fact}, "hookChanges": []any{hook}}
	data, err = json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.appendFromFile(oldID, first, 1); err != nil {
		t.Fatal(err)
	}
	second := testStage(t, w, oldID, "abababababababababababababababad", "ch_2", "虚构第二章。")
	if _, err := w.appendFromFile(oldID, second, 2); err != nil {
		t.Fatal(err)
	}
	oldRoot, h, m, err := w.load(oldID)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(w.root, "moluo-v4")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(oldRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(oldRoot, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return newFile(source, filepath.ToSlash(rel), data)
	})
	if err != nil {
		t.Fatal(err)
	}
	var memory map[string]any
	if err := loadJSON(source, m.MemoryPath, &memory); err != nil {
		t.Fatal(err)
	}
	cards := memory["chapterCards"].([]any)
	archivePath := "memory/archives/m0001-0001-r000003.json"
	archive := map[string]any{"start": 1, "end": 1, "chapterIds": []any{"ch_1"}, "facts": []any{fact}, "knowledge": []any{}, "constraints": []any{}, "timeline": []any{}, "hooks": []any{hook}, "questions": []any{}, "chapterCards": cards}
	if err := newJSON(source, archivePath, archive); err != nil {
		t.Fatal(err)
	}
	archiveHash, err := fileSHA(source, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := map[string]any{"start": 1, "end": 1, "path": archivePath, "cardCount": 1, "counts": map[string]any{"facts": 1, "knowledge": 0, "constraints": 0, "timeline": 0, "hooks": 1, "questions": 0, "cards": 1}}
	m.MemoryArchives = []map[string]any{descriptor}
	m.ContentHashes[archivePath] = archiveHash
	taskArchivePath := "tasks/archives/t0001-0001-r000003.json"
	oldTask := map[string]any{"id": "task_old", "title": "找回旧门铁盒", "plannedChapter": float64(1), "priority": "high", "status": "done", "completedChapterId": "ch_1", "completionEvidence": "旧门下埋着虚构铁盒。"}
	if err := newJSON(source, taskArchivePath, []any{oldTask}); err != nil {
		t.Fatal(err)
	}
	taskArchiveHash, err := fileSHA(source, taskArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	m.TaskArchives = []map[string]any{{"path": taskArchivePath, "count": 1}}
	m.ContentHashes[taskArchivePath] = taskArchiveHash
	memory["activeFacts"] = []any{}
	memory["openHooks"] = []any{}
	memory["chapterCards"] = []any{}
	memory["archives"] = []any{descriptor}
	memory["summaries"] = []any{map[string]any{"archivePath": archivePath, "summary": "旧门归档"}}
	if err := atomicJSON(source, m.MemoryPath, memory); err != nil {
		t.Fatal(err)
	}
	memoryHash, err := fileSHA(source, m.MemoryPath)
	if err != nil {
		t.Fatal(err)
	}
	m.ContentHashes[m.MemoryPath] = memoryHash
	delete(m.ContentHashes, m.EntitiesPath)
	m.EntitiesPath = ""
	m.WorkID = ""
	h.WorkID = ""
	h.ManifestSHA256 = "" // 构造旧版无哈希 HEAD 的迁移样本。
	if err := atomicJSON(source, h.Manifest, m); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(source, "HEAD.json", h); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(source, "work.json")); err != nil {
		t.Fatal(err)
	}
	before, err := fileSHA(source, "HEAD.json")
	if err != nil {
		t.Fatal(err)
	}
	result, err := w.importOldV4()
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]any)["workId"].(string)
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
	oldFact, err := w.query(id, "memory", "fact_old", "", 0, 20, 0)
	if err != nil || oldFact.(map[string]any)["total"].(int) < 1 {
		t.Fatal("旧归档事实丢失", err)
	}
	openHook, err := w.query(id, "memory", "hook_old", "", 0, 20, 0)
	if err != nil || openHook.(map[string]any)["total"].(int) < 1 {
		t.Fatal("旧未解伏笔丢失", err)
	}
	oldTasks, err := w.query(id, "tasks", "task_old", "", 0, 20, 0)
	if err != nil || oldTasks.(map[string]any)["total"].(int) != 1 {
		t.Fatal("旧已完成待办归档丢失", err)
	}
	after, err := fileSHA(source, "HEAD.json")
	if err != nil || before != after {
		t.Fatal("旧归档源被修改")
	}
	if err := os.WriteFile(filepath.Join(source, filepath.FromSlash(archivePath)), []byte(`{"tampered":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.importOldV4(); err == nil || !strings.Contains(err.Error(), "哈希") {
		t.Fatal("旧归档被篡改后仍被导入", err)
	}
}

func TestTaskDeferCancelAndReopenNeedConfirmation(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	stage := func(op, kind string, record map[string]any, confirm bool) string {
		path := testRecordStage(t, w, id, op, kind, record)
		if confirm {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var d recordDraft
			if err := json.Unmarshal(data, &d); err != nil {
				t.Fatal(err)
			}
			d.Confirm = true
			updated, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, updated, 0600); err != nil {
				t.Fatal(err)
			}
		}
		return path
	}
	add := stage("91919191919191919191919191919191", "task-add", map[string]any{"id": "task_a", "title": "虚构延期任务", "plannedChapter": float64(2), "priority": "normal", "status": "todo"}, false)
	if _, err := w.mutateFromFile(id, add, 1); err != nil {
		t.Fatal(err)
	}
	deferPath := stage("92929292929292929292929292929292", "task-update", map[string]any{"id": "task_a", "status": "deferred", "deferredUntilChapter": float64(5), "authorDecision": "作者明确延期到第五章"}, false)
	if _, err := w.mutateFromFile(id, deferPath, 2); err == nil {
		t.Fatal("未经确认延期成功")
	}
	deferOK := stage("93939393939393939393939393939393", "task-update", map[string]any{"id": "task_a", "status": "deferred", "deferredUntilChapter": float64(5), "authorDecision": "作者明确延期到第五章"}, true)
	if _, err := w.mutateFromFile(id, deferOK, 2); err != nil {
		t.Fatal(err)
	}
	cancel := stage("94949494949494949494949494949494", "task-update", map[string]any{"id": "task_a", "status": "cancelled", "authorDecision": "作者明确撤销"}, true)
	if _, err := w.mutateFromFile(id, cancel, 3); err != nil {
		t.Fatal(err)
	}
	ordinary := stage("95959595959595959595959595959595", "task-update", map[string]any{"id": "task_a", "status": "todo"}, false)
	if _, err := w.mutateFromFile(id, ordinary, 4); err == nil {
		t.Fatal("普通更新重开已撤销待办")
	}
	reopen := stage("96969696969696969696969696969696", "task-reopen", map[string]any{"id": "task_a", "status": "todo", "authorDecision": "作者明确重开"}, true)
	if _, err := w.mutateFromFile(id, reopen, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryCopyPreservesCorruptPrimary(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	input := testStage(t, w, id, "97979797979797979797979797979797", "ch_1", "虚构恢复正文。")
	if _, err := w.appendFromFile(id, input, 1); err != nil {
		t.Fatal(err)
	}
	root, err := w.workRoot(id)
	if err != nil {
		t.Fatal(err)
	}
	bad := []byte("{broken-head")
	if err := os.WriteFile(filepath.Join(root, "HEAD.json"), bad, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctor(id); err == nil {
		t.Fatal("损坏 HEAD 未被识别")
	}
	copy, err := w.restoreCopy(id, 2)
	if err != nil {
		t.Fatal(err)
	}
	path := copy.(map[string]any)["recoveryPath"].(string)
	var h head
	if err := loadJSON(path, "HEAD.json", &h); err != nil || h.Revision != 2 {
		t.Fatal("恢复副本 HEAD 无效", err)
	}
	unchanged, err := os.ReadFile(filepath.Join(root, "HEAD.json"))
	if err != nil || string(unchanged) != string(bad) {
		t.Fatal("恢复副本误改主作品")
	}
}

func TestCorruptionAndMissingBackupStopWrite(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	root, _, m, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(m.CharactersPath)), []byte("[] \n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctor(id); err == nil || !strings.Contains(err.Error(), "哈希") {
		t.Fatal("合法 JSON 篡改未被检出")
	}
	corruptInput := testStage(t, w, id, "90909090909090909090909090909090", "ch_1", "虚构正文。")
	if _, err := w.appendFromFile(id, corruptInput, 1); err == nil {
		t.Fatal("已有资料损坏后仍允许写入")
	}
	backupRoot1, err := w.backupRoot(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(backupRoot1, filepath.FromSlash(snapshotPath(1)))); err != nil {
		t.Fatal(err)
	}
	if _, err := w.backup(id); err == nil || !strings.Contains(err.Error(), "主数据不完整") {
		t.Fatal("损坏的主数据被补写进独立备份")
	}
	// 用另一份干净虚构作品检查独立备份缺失后的停写。
	id2 := testInit(t, w)
	backupRoot, err := w.backupRoot(id2)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(backupRoot, filepath.FromSlash(snapshotPath(1)))); err != nil {
		t.Fatal(err)
	}
	input := testStage(t, w, id2, "33333333333333333333333333333333", "ch_1", "虚构章节。")
	if _, err := w.appendFromFile(id2, input, 1); err == nil || !strings.Contains(err.Error(), "备份缺失") {
		t.Fatal("备份丢失后仍允许写入")
	}
	if _, err := w.backup(id2); err != nil {
		t.Fatal(err)
	}
	if _, err := w.appendFromFile(id2, input, 1); err != nil {
		t.Fatal(err)
	}
}

func TestBackupFailureAfterCommitReportedAndBlocksNext(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	backupRoot, err := w.backupRoot(id)
	if err != nil {
		t.Fatal(err)
	}
	// 隔离备份目录中预置一个同名目录，令新版本快照写入失败。
	blocked := filepath.Join(backupRoot, filepath.FromSlash(snapshotPath(2)))
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	first := testStage(t, w, id, "44444444444444444444444444444444", "ch_1", "虚构第一章。")
	_, err = w.appendFromFile(id, first, 1)
	var partial savedBackupError
	if !errors.As(err, &partial) || !strings.Contains(err.Error(), "备份失败") {
		t.Fatalf("未准确报告正文已保存、备份失败：%v", err)
	}
	_, h, _, err := w.load(id)
	if err != nil || h.Revision != 2 {
		t.Fatal("备份失败不应回滚已提交正文")
	}
	next := testStage(t, w, id, "55555555555555555555555555555555", "ch_2", "虚构第二章。")
	if _, err := w.appendFromFile(id, next, 2); err == nil {
		t.Fatal("未修复备份时允许继续写入")
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if _, err := w.backup(id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.appendFromFile(id, next, 2); err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceAndConcurrentConflict(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	invalid := testStage(t, w, id, "66666666666666666666666666666666", "ch_1", "虚构正文。")
	data, err := os.ReadFile(invalid)
	if err != nil {
		t.Fatal(err)
	}
	var d draft
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	d.Card = map[string]any{"chapterId": "ch_1", "facts": []any{map[string]any{"id": "fact_1", "sourceChapterId": "ch_1", "evidence": "正文中不存在的句子。"}}}
	modified, _ := json.Marshal(d)
	if err := os.WriteFile(invalid, modified, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.appendFromFile(id, invalid, 1); err == nil {
		t.Fatal("伪造原文证据被接受")
	}
	first := testStage(t, w, id, "77777777777777777777777777777777", "ch_1", "甲在纸月城。")
	second := testStage(t, w, id, "88888888888888888888888888888888", "ch_2", "乙在纸月城。")
	var wg sync.WaitGroup
	wg.Add(2)
	results := make(chan error, 2)
	for _, input := range []string{first, second} {
		go func(path string) { defer wg.Done(); _, err := w.appendFromFile(id, path, 1); results <- err }(input)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("并发应恰好一个成功，实际 %d", success)
	}
	_, h, m, err := w.load(id)
	if err != nil || h.Revision != 2 || m.ChapterCount != 1 {
		t.Fatal("并发写入后的 HEAD 或章节数错误")
	}
}

func testRecordStage(t *testing.T, w *workspace, id, op, kind string, record map[string]any) string {
	t.Helper()
	dir := filepath.Join(w.inbox, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, op+".json")
	data, err := json.Marshal(recordDraft{OperationID: op, Kind: kind, Record: record})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCharactersTasksBlueprintAndEvidence(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	char := testRecordStage(t, w, id, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "character-add", map[string]any{"id": "char_lan", "name": "阿岚", "age": "30"})
	if _, err := w.mutateFromFile(id, char, 1); err != nil {
		t.Fatal(err)
	}
	task := testRecordStage(t, w, id, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "task-add", map[string]any{"id": "task_1", "title": "桥上相遇", "plannedChapter": 1, "priority": "high", "status": "todo", "characterIds": []string{"char_lan"}})
	if _, err := w.mutateFromFile(id, task, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := w.mutateFromFile(id, task, 2); err != nil {
		t.Fatal("重复任务操作应幂等：", err)
	}
	bad := testRecordStage(t, w, id, "cccccccccccccccccccccccccccccccc", "task-add", map[string]any{"id": "task_bad", "title": "非法优先级", "plannedChapter": 1, "priority": "urgent", "status": "todo"})
	if _, err := w.mutateFromFile(id, bad, 3); err == nil {
		t.Fatal("非法优先级被接受")
	}
	chapterFile := testStage(t, w, id, "dddddddddddddddddddddddddddddddd", "ch_1", "阿岚在桥上遇见了月生。")
	if _, err := w.appendFromFile(id, chapterFile, 3); err != nil {
		t.Fatal(err)
	}
	falseDone := testRecordStage(t, w, id, "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "task-update", map[string]any{"id": "task_1", "status": "done", "completedChapterId": "ch_1", "completionEvidence": "根本没有发生。"})
	if _, err := w.mutateFromFile(id, falseDone, 4); err == nil {
		t.Fatal("错误核销被接受")
	}
	done := testRecordStage(t, w, id, "ffffffffffffffffffffffffffffffff", "task-update", map[string]any{"id": "task_1", "status": "done", "completedChapterId": "ch_1", "completionEvidence": "阿岚在桥上遇见了月生。"})
	if _, err := w.mutateFromFile(id, done, 4); err != nil {
		t.Fatal(err)
	}
	reopen := testRecordStage(t, w, id, "abababababababababababababababab", "task-update", map[string]any{"id": "task_1", "status": "todo"})
	if _, err := w.mutateFromFile(id, reopen, 5); err == nil {
		t.Fatal("已完成待办被普通更新重开")
	}
	blueprint := testRecordStage(t, w, id, "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd", "blueprint-set", map[string]any{"plan": map[string]any{"theme": "虚构主题"}, "candidate": map[string]any{}, "canon": map[string]any{}})
	if _, err := w.mutateFromFile(id, blueprint, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
	_, h, m, err := w.load(id)
	if err != nil || h.Revision != 6 || m.BlueprintPath == "" {
		t.Fatal("蓝图或版本未保存")
	}
	other := testInit(t, w)
	if _, err := w.mutateFromFile(other, blueprint, 1); err == nil {
		t.Fatal("跨作品候选文件被接受")
	}
}

func TestChapterStatusLifecycle(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	op := "93939393939393939393939393939393"
	input := testStage(t, w, id, op, "ch_1", "虚构草稿正文。")
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	var d draft
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	d.Chapter.Status = "draft"
	d.Chapter.Outline = "虚构提纲：阿岚在旧桥发现铜钥匙。"
	d.Chapter.ReviewNotes = "初稿待审。"
	modified, _ := json.Marshal(d)
	if err := os.WriteFile(input, modified, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.appendFromFile(id, input, 1); err != nil {
		t.Fatal(err)
	}
	review := testRecordStage(t, w, id, "94949494949494949494949494949494", "chapter-status",
		map[string]any{"chapterId": "ch_1", "status": "review",
			"reviewNotes": "审校通过：时间线无矛盾。"})
	if _, err := w.mutateFromFile(id, review, 2); err != nil {
		t.Fatal(err)
	}
	final := testRecordStage(t, w, id, "95959595959595959595959595959595", "chapter-status",
		map[string]any{"chapterId": "ch_1", "status": "final"})
	if _, err := w.mutateFromFile(id, final, 3); err != nil {
		t.Fatal(err)
	}
	page, err := w.readChapter(id, "ch_1", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if page.(map[string]any)["status"] != "final" ||
		page.(map[string]any)["outline"] == "" ||
		page.(map[string]any)["reviewNotes"] != "审校通过：时间线无矛盾。" {
		t.Fatalf("章节生命周期/提纲/审校记录未保存：%v", page)
	}
	regress := testRecordStage(t, w, id, "96969696969696969696969696969696", "chapter-status",
		map[string]any{"chapterId": "ch_1", "status": "draft"})
	if _, err := w.mutateFromFile(id, regress, 4); err == nil ||
		!strings.Contains(err.Error(), "不允许") {
		t.Fatal("非法章节状态转换未拒绝")
	}
}

func TestPrepareDraftReviewFinalizeGate(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	task := testRecordStage(t, w, id, "e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1", "task-add", map[string]any{
		"id": "task_open", "title": "阿岚必须打开纸盒", "plannedChapter": 1,
		"priority": "high", "status": "todo"})
	if _, err := w.mutateFromFile(id, task, 1); err != nil {
		t.Fatal(err)
	}
	prepared, err := w.prepareChapter(id, "ch_1", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	pm := prepared.(map[string]any)
	prepareID := pm["prepareId"].(string)
	requirements := pm["requirements"].([]prepareRequirement)
	if len(requirements) != 1 || requirements[0].ID != "task:task_open" {
		t.Fatalf("prepare 未生成待办硬要求：%v", requirements)
	}
	contextPage, err := w.readPrepare(id, prepareID, 0, 20)
	if err != nil || contextPage.(map[string]any)["complete"] != true {
		t.Fatalf("prepare 上下文不可分页回读：%v %v", contextPage, err)
	}
	writeDraft := func(op, content string) string {
		t.Helper()
		path := filepath.Join(w.inbox, id, op+".json")
		data, err := json.Marshal(draftCandidate{OperationID: op, PrepareID: prepareID,
			Chapter: chapter{ID: "ch_1", Title: "第1章", Content: content}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	draftPath := writeDraft("f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1", "阿岚走进房间，什么也没有发生。")
	if err := run([]string{"append", "--workspace", w.root, "--work", id,
		"--revision", "2", "--input", draftPath}); err == nil ||
		!strings.Contains(err.Error(), "finalize") {
		t.Fatal("直接 append 未被安全门禁拒绝")
	}
	if _, err := w.draftWorkflow(id, draftPath, 2); err != nil {
		t.Fatal(err)
	}
	writeReview := func(op, draftID, quote string) string {
		t.Helper()
		path := filepath.Join(w.inbox, id, op+".json")
		data, err := json.Marshal(reviewCandidate{OperationID: op, PrepareID: prepareID,
			DraftID: draftID, Requirements: []reviewItem{{ID: "task:task_open", Status: "satisfied", Quote: quote}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	missing := writeReview("f2f2f2f2f2f2f2f2f2f2f2f2f2f2f2f2", "f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1", "打开了纸盒")
	review, err := w.reviewWorkflow(id, missing, 2)
	if err != nil {
		t.Fatal(err)
	}
	if review.(map[string]any)["verdict"] != "fail" {
		t.Fatal("缺失/伪证据的审稿没有失败")
	}
	// A revised independent draft, then a genuinely matching review.
	draft2 := writeDraft("f3f3f3f3f3f3f3f3f3f3f3f3f3f3f3f3", "阿岚终于打开了纸盒。")
	if _, err := w.draftWorkflow(id, draft2, 2); err != nil {
		t.Fatal(err)
	}
	review2 := writeReview("f4f4f4f4f4f4f4f4f4f4f4f4f4f4f4f4", "f3f3f3f3f3f3f3f3f3f3f3f3f3f3f3f3", "打开了纸盒")
	okReview, err := w.reviewWorkflow(id, review2, 2)
	if err != nil || okReview.(map[string]any)["verdict"] != "pass" {
		t.Fatalf("真实正文证据审稿未通过：%v %v", okReview, err)
	}
	finalPath := filepath.Join(w.inbox, id, "f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5.json")
	finalData, _ := json.Marshal(finalCandidate{OperationID: "f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5",
		PrepareID: prepareID, DraftID: "f3f3f3f3f3f3f3f3f3f3f3f3f3f3f3f3",
		ReviewID: "f4f4f4f4f4f4f4f4f4f4f4f4f4f4f4f4",
		Chapter:  chapter{ID: "ch_1", Title: "第1章", Content: ""}})
	if err := os.WriteFile(finalPath, finalData, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := w.finalizeWorkflow(id, finalPath, 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["readback"] != "正文一致" {
		t.Fatal("定稿后未回读正文")
	}
	page, err := w.readChapter(id, "ch_1", 0, 100)
	if err != nil || page.(map[string]any)["status"] != "final" ||
		page.(map[string]any)["text"] != "阿岚终于打开了纸盒。" {
		t.Fatalf("定稿磁盘内容不一致：%v %v", page, err)
	}
	// No prepare/review must never be accepted as a safe final.
	noPrepare := filepath.Join(w.inbox, id, "f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6.json")
	noData, _ := json.Marshal(finalCandidate{OperationID: "f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6",
		Chapter: chapter{ID: "ch_2", Title: "第2章", Content: "没有准备。"}})
	if err := os.WriteFile(noPrepare, noData, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.finalizeWorkflow(id, noPrepare, 3); err == nil {
		t.Fatal("无 prepare 的正文被安全定稿")
	}
	directFinal := testRecordStage(t, w, id, "f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7", "chapter-status",
		map[string]any{"chapterId": "ch_1", "status": "final"})
	if err := run([]string{"mutate", "--workspace", w.root, "--work", id,
		"--revision", "3", "--input", directFinal}); err == nil ||
		!strings.Contains(err.Error(), "finalize") {
		t.Fatal("CLI chapter-status final 未被安全门禁拒绝")
	}
}

func TestWorkflowCLIEndToEnd(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	task := testRecordStage(t, w, id, "abababababababababababababababab", "task-add", map[string]any{
		"id": "task_cli", "title": "第一章必须抵达旧桥", "plannedChapter": 1,
		"priority": "normal", "status": "todo"})
	if _, err := w.mutateFromFile(id, task, 1); err != nil {
		t.Fatal(err)
	}
	base := []string{"--workspace", w.root, "--work", id, "--revision", "2"}
	if err := run(append([]string{"prepare"}, append(base, "--id", "ch_1", "--chapter", "1")...)); err != nil {
		t.Fatal(err)
	}
	root, _, _, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(root, "prepares", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("CLI prepare 未生成记录：%v %v", files, err)
	}
	var prepare prepareRecord
	if err := loadJSON(root, filepath.ToSlash(strings.TrimPrefix(files[0], root+string(filepath.Separator))), &prepare); err != nil {
		t.Fatal(err)
	}
	if len(prepare.Requirements) != 1 {
		t.Fatalf("CLI prepare 要求缺失：%v", prepare.Requirements)
	}
	write := func(op string, value any) string {
		t.Helper()
		path := filepath.Join(w.inbox, id, op+".json")
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	draft := write("acacacacacacacacacacacacacacacac", draftCandidate{OperationID: "acacacacacacacacacacacacacacacac",
		PrepareID: prepare.PrepareID, Chapter: chapter{ID: "ch_1", Title: "第1章", Content: "阿岚抵达旧桥。"}})
	if err := run(append([]string{"draft"}, append(base, "--input", draft)...)); err != nil {
		t.Fatal(err)
	}
	review := write("adadadadadadadadadadadadadadadad", reviewCandidate{
		OperationID: "adadadadadadadadadadadadadadadad", PrepareID: prepare.PrepareID,
		DraftID:      "acacacacacacacacacacacacacacacac",
		Requirements: []reviewItem{{ID: "task:task_cli", Status: "satisfied", Quote: "抵达旧桥"}}})
	if err := run(append([]string{"review"}, append(base, "--input", review)...)); err != nil {
		t.Fatal(err)
	}
	final := write("aeaeaeaeaeaeaeaeaeaeaeaeaeaeaeae", finalCandidate{
		OperationID: "aeaeaeaeaeaeaeaeaeaeaeaeaeaeaeae", PrepareID: prepare.PrepareID,
		DraftID: "acacacacacacacacacacacacacacacac", ReviewID: "adadadadadadadadadadadadadadadad",
		Chapter: chapter{ID: "ch_1", Title: "第1章", Content: ""}})
	if err := run(append([]string{"finalize"}, append(base, "--input", final)...)); err != nil {
		t.Fatal(err)
	}
	if err := run(append([]string{"finalize"}, append(base, "--input", final)...)); err != nil {
		t.Fatalf("同一定稿 CLI 重试失败：%v", err)
	}
	page, err := w.readChapter(id, "ch_1", 0, 100)
	if err != nil || page.(map[string]any)["text"] != "阿岚抵达旧桥。" {
		t.Fatalf("CLI 定稿正文不一致：%v %v", page, err)
	}
}

func TestChapterNarrativeOrderWithoutRewritingEvidence(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	for n, item := range []struct {
		op, chapterID, content string
	}{
		{"d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1", "ch_1", "第一章原文。"},
		{"d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2", "ch_2", "第二章原文。"},
		{"d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3", "ch_3", "第三章原文。"},
	} {
		input := testStage(t, w, id, item.op, item.chapterID, item.content)
		if _, err := w.appendFromFile(id, input, n+1); err != nil {
			t.Fatal(err)
		}
	}
	root, _, beforeManifest, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	beforeEntries, err := w.entries(root, beforeManifest)
	if err != nil {
		t.Fatal(err)
	}
	beforeHash := map[string]string{}
	for _, item := range beforeEntries {
		beforeHash[item.ID] = item.SHA256
	}
	order := testRecordStage(t, w, id, "d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4", "chapter-order",
		map[string]any{"order": []any{"ch_3", "ch_1", "ch_2"}})
	orderDraft, err := os.ReadFile(order)
	if err != nil {
		t.Fatal(err)
	}
	var od recordDraft
	if err := json.Unmarshal(orderDraft, &od); err != nil {
		t.Fatal(err)
	}
	od.Confirm = true
	encoded, _ := json.Marshal(od)
	if err := os.WriteFile(order, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.mutateFromFile(id, order, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
	_, _, afterManifest, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	afterEntries, err := w.entries(root, afterManifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range afterEntries {
		if beforeHash[item.ID] != item.SHA256 {
			t.Fatalf("调序改写了章节正文：%s", item.ID)
		}
	}
	listing, err := w.query(id, "chapters", "", "", 0, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	orders := map[string]int{}
	for _, raw := range listing.(map[string]any)["items"].([]any) {
		item := raw.(map[string]any)
		orders[item["id"].(string)] = item["order"].(int)
	}
	if orders["ch_3"] != 1 || orders["ch_1"] != 2 || orders["ch_2"] != 3 {
		t.Fatalf("叙事顺序未保存：%v", orders)
	}
	for _, format := range []string{"txt", "md"} {
		result, err := w.exportReading(id, format)
		if err != nil {
			t.Fatal(err)
		}
		info := result.(map[string]any)
		data, err := os.ReadFile(info["path"].(string))
		if err != nil || sumBytes(data) != info["sha256"] {
			t.Fatal("阅读版导出回读哈希错误", err)
		}
		text := string(data)
		if strings.Index(text, "第三章原文。") > strings.Index(text, "第一章原文。") || strings.Index(text, "第一章原文。") > strings.Index(text, "第二章原文。") {
			t.Fatal("阅读版未按叙事顺序导出")
		}
		if info["importable"] != false || !strings.Contains(text, "第三章原文。") {
			t.Fatal("阅读版内容或不可导入标记错误")
		}
	}
	if _, err := w.exportReading(id, "html"); err == nil {
		t.Fatal("非法阅读版格式被接受")
	}
	bad := testRecordStage(t, w, id, "d5d5d5d5d5d5d5d5d5d5d5d5d5d5d5d5", "chapter-order",
		map[string]any{"order": []any{"ch_1", "ch_2"}})
	badDraft, _ := os.ReadFile(bad)
	var bd recordDraft
	_ = json.Unmarshal(badDraft, &bd)
	bd.Confirm = true
	encoded, _ = json.Marshal(bd)
	_ = os.WriteFile(bad, encoded, 0600)
	if _, err := w.mutateFromFile(id, bad, 5); err == nil || !strings.Contains(err.Error(), "覆盖全部") {
		t.Fatal("缺章叙事顺序未拒绝")
	}
}

func TestQueryChapterRangeAndTaskStatusFilters(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	first := testStage(t, w, id, "c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1", "ch_1", "第一章虚构正文。")
	if _, err := w.appendFromFile(id, first, 1); err != nil {
		t.Fatal(err)
	}
	second := testStage(t, w, id, "c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2", "ch_2", "第二章虚构正文。")
	if _, err := w.appendFromFile(id, second, 2); err != nil {
		t.Fatal(err)
	}
	task := testRecordStage(t, w, id, "c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3", "task-add", map[string]any{
		"id": "task_2", "title": "第二章待办", "plannedChapter": 2,
		"priority": "normal", "status": "todo"})
	if _, err := w.mutateFromFile(id, task, 3); err != nil {
		t.Fatal(err)
	}
	chapters, err := w.queryFiltered(id, "chapters", "", "", 0, 20, 0, 2, 2, "final", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if chapters.(map[string]any)["total"] != 1 ||
		chapters.(map[string]any)["items"].([]any)[0].(map[string]any)["id"] != "ch_2" {
		t.Fatalf("章节范围/状态过滤失败：%v", chapters)
	}
	tasks, err := w.queryFiltered(id, "tasks", "", "", 0, 20, 0, 2, 2, "todo", "", "")
	if err != nil || tasks.(map[string]any)["total"] != 1 {
		t.Fatalf("待办范围/状态过滤失败：%v %v", tasks, err)
	}
	if _, err := w.queryFiltered(id, "chapters", "", "", 0, 20, 0, 3, 1, "", "", ""); err == nil {
		t.Fatal("反向章节范围未拒绝")
	}
}

func TestUnknownAndFlashbackTimelineEvidence(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	quote := "旧日记忆里没有日期。"
	input := testStage(t, w, id, "b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1", "ch_1", quote)
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	var d draft
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	d.Card = map[string]any{"chapterId": "ch_1", "timeline": []any{
		map[string]any{"id": "event_unknown", "sourceChapterId": "ch_1", "timeStatus": "unknown", "evidence": quote},
		map[string]any{"id": "event_flashback", "sourceChapterId": "ch_1", "timeStatus": "flashback", "startDate": "2020-01-01", "evidence": quote},
	}}
	encoded, _ := json.Marshal(d)
	if err := os.WriteFile(input, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.appendFromFile(id, input, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
	bad := testStage(t, w, id, "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2", "ch_2", quote)
	data, _ = os.ReadFile(bad)
	var d2 draft
	_ = json.Unmarshal(data, &d2)
	d2.Card = map[string]any{"chapterId": "ch_2", "timeline": []any{
		map[string]any{"id": "event_bad", "sourceChapterId": "ch_2", "timeStatus": "unknown", "startDate": "2020-01-01", "evidence": quote},
	}}
	encoded, _ = json.Marshal(d2)
	_ = os.WriteFile(bad, encoded, 0600)
	if _, err := w.appendFromFile(id, bad, 2); err == nil || !strings.Contains(err.Error(), "未知时间") {
		t.Fatal("未知时间事件伪造日期未被拒绝")
	}
}

func TestCharacterAndEntityCausalEvidence(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	quote := "阿岚在旧桥捡到铜钥匙。"
	input := testStage(t, w, id, "97979797979797979797979797979797", "ch_1", quote)
	if _, err := w.appendFromFile(id, input, 1); err != nil {
		t.Fatal(err)
	}
	character := testRecordStage(t, w, id, "98989898989898989898989898989898", "character-add", map[string]any{
		"id": "char_lan", "name": "阿岚",
		"cognition": []any{map[string]any{
			"factId": "fact_key", "beliefType": "fact",
			"learnedChapterId": "ch_1", "evidence": quote}},
		"relationships": []any{map[string]any{
			"targetId": "char_lan", "kind": "self", "sinceChapterId": "ch_1", "evidence": quote}},
		"injuries": []any{map[string]any{
			"chapterId": "ch_1", "status": "healed", "evidence": quote}},
	})
	if _, err := w.mutateFromFile(id, character, 2); err != nil {
		t.Fatal(err)
	}
	entity := testRecordStage(t, w, id, "99999999999999999999999999999999", "entity-add", map[string]any{
		"id": "item_key", "kind": "item", "name": "铜钥匙", "status": "canon",
		"sourceChapterId": "ch_1", "evidence": quote,
		"relations": []any{map[string]any{
			"targetId": "char_lan", "kind": "owner", "fromChapterId": "ch_1", "evidence": quote}},
		"ownershipHistory": []any{map[string]any{
			"ownerId": "char_lan", "fromChapterId": "ch_1", "evidence": quote}},
		"conditionHistory": []any{map[string]any{
			"chapterId": "ch_1", "status": "intact", "evidence": quote}},
	})
	if _, err := w.mutateFromFile(id, entity, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
	bad := testRecordStage(t, w, id, "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1", "character-update", map[string]any{
		"id": "char_lan", "name": "阿岚",
		"cognition": []any{map[string]any{
			"factId": "fact_key", "beliefType": "rumor",
			"learnedChapterId": "ch_1", "evidence": "正文不存在的认知证据。"}},
	})
	if _, err := w.mutateFromFile(id, bad, 4); err == nil || !strings.Contains(err.Error(), "证据") {
		t.Fatal("人物认知的伪证据未拒绝")
	}
	badEntity := testRecordStage(t, w, id, "a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2", "entity-add", map[string]any{
		"id": "item_bad", "kind": "item", "name": "伪物", "status": "plan",
		"relations": []any{map[string]any{
			"targetId": "char_lan", "kind": "owner", "fromChapterId": "ch_1", "evidence": "伪证据"}},
	})
	if _, err := w.mutateFromFile(id, badEntity, 4); err == nil || !strings.Contains(err.Error(), "证据") {
		t.Fatal("实体关系伪证据未拒绝")
	}
}

func TestStructuredBlueprintV2Validation(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	layer := func() map[string]any {
		return map[string]any{
			"theme":    "虚构主题",
			"mainPlot": map[string]any{"id": "main_1", "summary": "虚构主线"},
			"subplots": []any{map[string]any{"id": "sub_1", "summary": "虚构支线"}},
			"volumes":  []any{map[string]any{"id": "vol_1", "title": "第一卷", "summary": "虚构卷纲"}},
			"chapterOutline": []any{map[string]any{
				"id": "outline_1", "title": "第一章", "volumeId": "vol_1", "summary": "虚构提纲"}},
			"chapterLengths": []any{map[string]any{
				"id": "length_1", "chapterNumber": float64(1), "minWords": float64(1000), "maxWords": float64(3000), "targetWords": float64(2000)}},
			"characterArcs": []any{map[string]any{
				"id": "arc_1", "characterId": "char_lan", "summary": "虚构成长弧"}},
			"pacing":      []any{map[string]any{"chapterId": "ch_1", "tension": float64(5)}},
			"majorEvents": []any{map[string]any{"id": "event_1", "summary": "虚构事件"}},
		}
	}
	blueprint := testRecordStage(t, w, id, "91919191919191919191919191919191", "blueprint-set",
		map[string]any{"schemaVersion": float64(2), "plan": layer(),
			"candidate": map[string]any{}, "canon": map[string]any{}})
	if _, err := w.mutateFromFile(id, blueprint, 1); err != nil {
		t.Fatal(err)
	}
	broken := layer()
	delete(broken, "volumes")
	bad := testRecordStage(t, w, id, "92929292929292929292929292929292", "blueprint-set",
		map[string]any{"schemaVersion": float64(2), "plan": broken,
			"candidate": map[string]any{}, "canon": map[string]any{}})
	if _, err := w.mutateFromFile(id, bad, 2); err == nil ||
		!strings.Contains(err.Error(), "volumes") {
		t.Fatal("缺少卷纲的 v2 蓝图未拒绝")
	}
}

func TestInterruptedTransactionRecoveryKeepsOldHead(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	root, h, _, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	op := "12121212121212121212121212121212"
	tx := pendingTxn{OperationID: op, InputSHA256: strings.Repeat("a", 64), BaseRevision: 1, TargetRevision: 2, TargetManifest: "manifests/r000002-interrupted.json"}
	if err := newJSON(root, "transactions/"+op+".json", tx); err != nil {
		t.Fatal(err)
	}
	input := testStage(t, w, id, "34343434343434343434343434343434", "ch_1", "虚构正文。")
	if _, err := w.appendFromFile(id, input, 1); err == nil {
		t.Fatal("未完成事务没有阻止新写入")
	}
	if _, err := w.doctor(id); err == nil {
		t.Fatal("健康检查没有报告未完成事务")
	}
	if _, err := w.recoverPending(id, op, false); err == nil {
		t.Fatal("未经确认放弃事务")
	}
	if _, err := w.recoverPending(id, op, true); err != nil {
		t.Fatal(err)
	}
	_, after, _, err := w.load(id)
	if err != nil || after != h {
		t.Fatal("放弃未提交事务改变了旧 HEAD")
	}
	if _, err := w.appendFromFile(id, input, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
}

func TestStaleWriteLockNeedsConfirmation(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	root, err := w.workRoot(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".writer.lock"), []byte("pid=999999999 started=fictional\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.unlockStale(id, false); err == nil {
		t.Fatal("未经确认释放异常锁")
	}
	if _, err := w.unlockStale(id, true); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteExportAndSafeImport(t *testing.T) {
	source := testWorkspace(t)
	id := testInit(t, source)
	first := testStage(t, source, id, "56565656565656565656565656565656", "ch_1", "虚构的第一章。")
	if _, err := source.appendFromFile(id, first, 1); err != nil {
		t.Fatal(err)
	}
	exported, err := source.exportWork(id)
	if err != nil {
		t.Fatal(err)
	}
	zipPath := exported.(map[string]any)["path"].(string)
	if _, err := inspectExport(zipPath); err != nil {
		t.Fatal(err)
	}
	target := testWorkspace(t)
	importDir := filepath.Join(target.root, "moluo-v4-imports")
	if err := os.MkdirAll(importDir, 0700); err != nil {
		t.Fatal(err)
	}
	archiveData, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(importDir, "fictional.zip")
	if err := os.WriteFile(input, archiveData, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := target.importWork(input); err != nil {
		t.Fatal(err)
	}
	if _, err := target.doctor(id); err != nil {
		t.Fatal(err)
	}
	if _, err := target.importWork(input); err == nil {
		t.Fatal("重复导入覆盖已有作品")
	}
	srcRoot, err := source.workRoot(id)
	if err != nil {
		t.Fatal(err)
	}
	dstRoot, err := target.workRoot(id)
	if err != nil {
		t.Fatal(err)
	}
	srcHead, err := fileSHA(srcRoot, "HEAD.json")
	if err != nil {
		t.Fatal(err)
	}
	dstHead, err := fileSHA(dstRoot, "HEAD.json")
	if err != nil {
		t.Fatal(err)
	}
	if srcHead != dstHead {
		t.Fatal("导入后 HEAD 哈希不一致")
	}
}

func TestImportRejectsTraversalAndBrokenZip(t *testing.T) {
	w := testWorkspace(t)
	dir := filepath.Join(w.root, "moluo-v4-imports")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "evil.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(f)
	writer, err := archive.Create("../novel-data.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("不应落盘")); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.importWork(path); err == nil {
		t.Fatal("ZIP 路径穿越被接受")
	}
	if _, err := os.Stat(filepath.Join(w.root, "novel-data.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("ZIP 越界写入了文件")
	}
	if err := os.WriteFile(path, []byte("不是 ZIP"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.importWork(path); err == nil {
		t.Fatal("损坏 ZIP 被接受")
	}
}

func TestCommittedAppendRecoversMissingOperationAndBackup(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	op := "71717171717171717171717171717171"
	input := testStage(t, w, id, op, "ch_1", "虚构恢复章节。")
	if _, err := w.appendFromFile(id, input, 1); err != nil {
		t.Fatal(err)
	}
	root, _, _, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "operations", op+".json")); err != nil {
		t.Fatal(err)
	}
	backupRoot, err := w.backupRoot(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(backupRoot, filepath.FromSlash(snapshotPath(2)))); err != nil {
		t.Fatal(err)
	}
	result, err := w.appendFromFile(id, input, 1)
	if err != nil {
		t.Fatal(err)
	}
	m := result.(map[string]any)
	if m["idempotent"] != true || m["revision"] != 2 {
		t.Fatalf("已提交操作未按幂等恢复：%v", m)
	}
	if _, err := os.Stat(filepath.Join(root, "operations", op+".json")); err != nil {
		t.Fatal("操作记录未补写")
	}
	if !w.backupExists(id, 2) {
		t.Fatal("独立备份未补写")
	}
	// Same operation ID with different bytes must not be accepted as idempotent.
	changed := testStage(t, w, id, op, "ch_1", "不同的虚构正文。")
	if _, err := w.appendFromFile(id, changed, 1); err == nil ||
		!strings.Contains(err.Error(), "不同候选") {
		t.Fatal("不同候选被伪装成同一已提交操作")
	}
}

func TestCommittedMutationRecoversMissingOperationRecord(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	op := "81818181818181818181818181818181"
	input := testRecordStage(t, w, id, op, "character-add", map[string]any{"id": "char_lan", "name": "阿岚"})
	if _, err := w.mutateFromFile(id, input, 1); err != nil {
		t.Fatal(err)
	}
	root, _, _, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "operations", op+".json")); err != nil {
		t.Fatal(err)
	}
	result, err := w.mutateFromFile(id, input, 1)
	if err != nil {
		t.Fatal(err)
	}
	m := result.(map[string]any)
	if m["idempotent"] != true || m["revision"] != 2 {
		t.Fatalf("已提交记录操作未按幂等恢复：%v", m)
	}
	if err := os.Remove(filepath.Join(root, "operations", op+".json")); err != nil {
		t.Fatal(err)
	}
	different := testRecordStage(t, w, id, op, "character-add", map[string]any{"id": "char_lan", "name": "另一个名字"})
	if _, err := w.mutateFromFile(id, different, 1); err == nil ||
		!strings.Contains(err.Error(), "目标记录不存在") {
		t.Fatal("不同记录内容被伪装成同一已提交操作")
	}
}

func TestCommittedAppendRepairFailureStaysConservative(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	op := "74747474747474747474747474747474"
	content := "虚构保守恢复正文。"
	input := testStage(t, w, id, op, "ch_1", content)
	if _, err := w.appendFromFile(id, input, 1); err != nil {
		t.Fatal(err)
	}
	root, _, _, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "operations", op+".json")); err != nil {
		t.Fatal(err)
	}
	backupRoot, err := w.backupRoot(id)
	if err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(backupRoot, filepath.FromSlash(snapshotPath(2)))
	if err := os.RemoveAll(blocked); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := w.appendFromFile(id, input, 1); err == nil {
		t.Fatal("补备份失败却报告成功")
	} else {
		var partial savedBackupError
		if !errors.As(err, &partial) {
			t.Fatalf("补备份失败不是可修复状态：%v", err)
		}
	}
	if w.backupExists(id, 2) {
		t.Fatal("阻塞目录被误报为有效备份")
	}
	if err := os.RemoveAll(blocked); err != nil {
		t.Fatal(err)
	}
	recovered, err := w.appendFromFile(id, input, 1)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.(map[string]any)["idempotent"] != true || !w.backupExists(id, 2) {
		t.Fatal("解除故障后未恢复幂等和备份")
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
	page, err := w.readChapter(id, "ch_1", 0, 8000)
	if err != nil || page.(map[string]any)["text"] != content {
		t.Fatalf("恢复后的真实正文不一致：%v %v", page, err)
	}
}

func TestMemoryCardEvidenceAndTamperDetection(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	char := testRecordStage(t, w, id, "10101010101010101010101010101010", "character-add", map[string]any{"id": "char_lan", "name": "阿岚"})
	if _, err := w.mutateFromFile(id, char, 1); err != nil {
		t.Fatal(err)
	}
	path := testStage(t, w, id, "20202020202020202020202020202020", "ch_1", "阿岚在旧桥拾起一把铜钥匙。")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var d draft
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	d.Card = map[string]any{"chapterId": "ch_1", "facts": []any{map[string]any{"id": "fact_key", "sourceChapterId": "ch_1", "certainty": "certain", "evidence": "阿岚在旧桥拾起一把铜钥匙。"}}, "knowledgeChanges": []any{map[string]any{"characterId": "char_lan", "factId": "fact_key", "learnedChapterId": "ch_1", "evidence": "阿岚在旧桥拾起一把铜钥匙。"}}}
	modified, _ := json.Marshal(d)
	if err := os.WriteFile(path, modified, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.appendFromFile(id, path, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
	root, h, m, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	var memory map[string]any
	if err := loadJSON(root, m.MemoryPath, &memory); err != nil {
		t.Fatal(err)
	}
	facts := memory["activeFacts"].([]any)
	facts[0].(map[string]any)["evidence"] = "正文不存在的新句子。"
	if err := atomicJSON(root, m.MemoryPath, memory); err != nil {
		t.Fatal(err)
	}
	hash, err := fileSHA(root, m.MemoryPath)
	if err != nil {
		t.Fatal(err)
	}
	m.ContentHashes[m.MemoryPath] = hash
	if err := atomicJSON(root, h.Manifest, m); err != nil {
		t.Fatal(err)
	}
	h.ManifestSHA256, err = fileSHA(root, h.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(root, "HEAD.json", h); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctor(id); err == nil || !strings.Contains(err.Error(), "证据") {
		t.Fatal("同步改写哈希后，伪造记忆证据仍未被发现")
	}
}
