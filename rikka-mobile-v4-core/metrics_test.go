package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type metricsFixture struct {
	w            *workspace
	id           string
	contentBytes int
}

func writeConfirmedRecord(t *testing.T, w *workspace, id, operationID, kind string, record map[string]any) string {
	t.Helper()
	path := testRecordStage(t, w, id, operationID, kind, record)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var candidate recordDraft
	if err := json.Unmarshal(raw, &candidate); err != nil {
		t.Fatal(err)
	}
	candidate.Confirm = true
	raw, _ = json.Marshal(candidate)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendMetricChapter(t *testing.T, w *workspace, id, op, chapterID, content, factText string, revision int) {
	t.Helper()
	path := testStage(t, w, id, op, chapterID, content)
	raw, _ := os.ReadFile(path)
	var candidate draft
	_ = json.Unmarshal(raw, &candidate)
	candidate.Card = map[string]any{"chapterId": chapterID, "facts": []any{map[string]any{
		"id": "fact_shared", "sourceChapterId": chapterID, "certainty": "certain",
		"text": factText, "evidence": content,
	}}}
	raw, _ = json.Marshal(candidate)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.appendFromFile(id, path, revision); err != nil {
		t.Fatal(err)
	}
}

func newMetricsFixture(t *testing.T) metricsFixture {
	t.Helper()
	w := testWorkspace(t)
	id := testInit(t, w)
	first, second := "甲乙，ABC!", "丙𠀀D"
	appendMetricChapter(t, w, id, strings.Repeat("1", 32), "ch_1", first, "旧事实", 1)
	appendMetricChapter(t, w, id, strings.Repeat("2", 32), "ch_2", second, "新事实", 2)
	for index, record := range []map[string]any{
		{"id": "char_a", "name": "甲"}, {"id": "char_b", "name": "乙"},
	} {
		op := strings.Repeat(string(rune('3'+index)), 32)
		input := testRecordStage(t, w, id, op, "character-add", record)
		if _, err := w.mutateFromFile(id, input, 3+index); err != nil {
			t.Fatal(err)
		}
	}
	entity := testRecordStage(t, w, id, strings.Repeat("5", 32), "entity-add",
		map[string]any{"id": "item_a", "kind": "item", "name": "虚构物品", "status": "plan"})
	if _, err := w.mutateFromFile(id, entity, 5); err != nil {
		t.Fatal(err)
	}
	pending := testRecordStage(t, w, id, strings.Repeat("6", 32), "task-add", map[string]any{
		"id": "task_pending", "title": "待完成", "plannedChapter": float64(2), "priority": "normal", "status": "todo"})
	if _, err := w.mutateFromFile(id, pending, 6); err != nil {
		t.Fatal(err)
	}
	done := testRecordStage(t, w, id, strings.Repeat("7", 32), "task-add", map[string]any{
		"id": "task_done", "title": "已完成", "plannedChapter": float64(1), "priority": "high", "status": "done",
		"completedChapterId": "ch_1", "completionEvidence": first})
	if _, err := w.mutateFromFile(id, done, 7); err != nil {
		t.Fatal(err)
	}
	archive := writeConfirmedRecord(t, w, id, strings.Repeat("8", 32), "task-archive-done",
		map[string]any{"throughChapter": float64(1)})
	if _, err := w.mutateFromFile(id, archive, 8); err != nil {
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
	entries, _ := w.entries(root, m)
	if err := archiveHistory(root, &m, memory, entries, 51, "metrics"); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(root, m.MemoryPath, memory); err != nil {
		t.Fatal(err)
	}
	m.ContentHashes[m.MemoryPath], _ = fileSHA(root, m.MemoryPath)
	if err := atomicJSON(root, h.Manifest, m); err != nil {
		t.Fatal(err)
	}
	h.ManifestSHA256, _ = fileSHA(root, h.Manifest)
	if err := atomicJSON(root, "HEAD.json", h); err != nil {
		t.Fatal(err)
	}
	return metricsFixture{w: w, id: id, contentBytes: len([]byte(first)) + len([]byte(second))}
}

func snapshotWorkFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(relative)] = sumBytes(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func independentDiskSize(t *testing.T, root string) (int, int64) {
	t.Helper()
	files := 0
	var bytes int64
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files++
		bytes += info.Size()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files, bytes
}

func TestMetricsEmptyWorkAndReadOnlyReproducibility(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	root, h, m, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotWorkFiles(t, root)
	result, err := w.metrics(id)
	if err != nil {
		t.Fatal(err)
	}
	metrics := result.(map[string]any)
	for _, field := range []string{"chapters", "chapterHanzi", "chapterUTF8Bytes", "characters", "entities", "tasksActive", "tasksArchived", "memoryArchived"} {
		if intValue(metrics[field]) != 0 {
			t.Fatalf("空作品统计字段 %s 不为零：%v", field, metrics[field])
		}
	}
	if metrics["revision"] != h.Revision || metrics["manifestPath"] != h.Manifest ||
		metrics["manifestSHA256"] != h.ManifestSHA256 || metrics["workPath"] != root ||
		!reflect.DeepEqual(metrics["contentSHA256"], m.ContentHashes) {
		t.Fatalf("空作品 revision/path/SHA 统计错误：%v", metrics)
	}
	if !strings.Contains(metrics["units"].(string), "不是 Token") {
		t.Fatal("metrics 没有明确区分字节和 Token")
	}
	if after := snapshotWorkFiles(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("metrics 修改了空作品数据")
	}
}

func TestMetricsCountsPersistedDataArchivesAndCapacity(t *testing.T) {
	fixture := newMetricsFixture(t)
	root, h, m, err := fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotWorkFiles(t, root)
	files, diskBytes := independentDiskSize(t, root)
	result, err := fixture.w.metrics(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	got := result.(map[string]any)
	expected := map[string]int{
		"chapters": 2, "chapterHanzi": 4, "chapterUTF8Bytes": fixture.contentBytes,
		"characters": 2, "entities": 1, "tasksActive": 1, "tasksPending": 1,
		"tasksCompletedOrCancelled": 0, "tasksArchived": 1, "tasksArchivedDone": 1,
		"tasksCompletedTotal": 1, "taskArchives": 1, "memoryArchives": 1,
		"memoryArchived": 1, "memoryHistoryPending": 0,
	}
	for field, want := range expected {
		if value := intValue(got[field]); value != want {
			t.Fatalf("统计字段 %s 错误：got=%d want=%d all=%v", field, value, want, got)
		}
	}
	active := got["memoryActive"].(map[string]int)
	if active["activeFacts"] != 1 {
		t.Fatalf("活动记忆数量错误：%v", active)
	}
	if intValue(got["diskFiles"]) != files || got["diskBytes"].(int64) != diskBytes ||
		got["revision"] != h.Revision || got["manifestSHA256"] != h.ManifestSHA256 ||
		!reflect.DeepEqual(got["contentSHA256"], m.ContentHashes) {
		t.Fatalf("容量/revision/SHA 不可复核：%v", got)
	}
	delete(got, "generatedAt")
	second, err := fixture.w.metrics(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	secondMap := second.(map[string]any)
	delete(secondMap, "generatedAt")
	if !reflect.DeepEqual(got, secondMap) {
		t.Fatal("同一磁盘状态的 metrics 不可复现")
	}
	if after := snapshotWorkFiles(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("metrics 修改了作品数据")
	}
}

func TestMetricsRejectsMissingAndTamperedData(t *testing.T) {
	t.Run("missing-chapter", func(t *testing.T) {
		fixture := newMetricsFixture(t)
		root, _, m, _ := fixture.w.load(fixture.id)
		entries, _ := fixture.w.entries(root, m)
		path, _ := safePath(root, entries[0].Path)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.w.metrics(fixture.id); err == nil || !strings.Contains(err.Error(), "完整性检查") {
			t.Fatalf("章节缺失未被 metrics 拒绝：%v", err)
		}
	})

	t.Run("tampered-task-index", func(t *testing.T) {
		fixture := newMetricsFixture(t)
		root, _, m, _ := fixture.w.load(fixture.id)
		tasks, _ := currentRecords(root, m.TasksPath)
		tasks[0]["title"] = "篡改"
		if err := atomicJSON(root, m.TasksPath, tasks); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.w.metrics(fixture.id); err == nil || !strings.Contains(err.Error(), "完整性检查") {
			t.Fatalf("待办索引篡改未被 metrics 拒绝：%v", err)
		}
	})

	t.Run("tampered-archive", func(t *testing.T) {
		fixture := newMetricsFixture(t)
		root, _, m, _ := fixture.w.load(fixture.id)
		path := m.TaskArchives[0]["path"].(string)
		if err := atomicJSON(root, path, []any{}); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.w.metrics(fixture.id); err == nil || !strings.Contains(err.Error(), "完整性检查") {
			t.Fatalf("任务归档篡改未被 metrics 拒绝：%v", err)
		}
	})
}
