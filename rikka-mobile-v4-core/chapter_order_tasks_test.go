package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func stageChapterOrder(t *testing.T, w *workspace, id, op string, order []any, remap bool) string {
	t.Helper()
	path := testRecordStage(t, w, id, op, "chapter-order", map[string]any{
		"order": order, "remapTasks": remap,
	})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var draft recordDraft
	if err := json.Unmarshal(raw, &draft); err != nil {
		t.Fatal(err)
	}
	draft.Confirm = true
	raw, err = json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func persistedTasks(t *testing.T, w *workspace, id string) []map[string]any {
	t.Helper()
	root, _, manifest, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := currentRecords(root, manifest.TasksPath)
	if err != nil {
		t.Fatal(err)
	}
	return tasks
}

func plannedChapters(tasks []map[string]any) map[string]int {
	result := make(map[string]int, len(tasks))
	for _, task := range tasks {
		id, _ := task["id"].(string)
		result[id] = intValue(task["plannedChapter"])
	}
	return result
}

func TestChapterOrderRemapTasksComplexPersistentAndIdempotent(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	for index, chapterID := range []string{"ch_1", "ch_2", "ch_3", "ch_4"} {
		op := strings.Repeat(string(rune('1'+index)), 32)
		input := testStage(t, w, id, op, chapterID, "虚构正文 "+chapterID)
		if _, err := w.appendFromFile(id, input, index+1); err != nil {
			t.Fatal(err)
		}
	}

	taskSpecs := []struct {
		op, id  string
		planned int
	}{
		{strings.Repeat("a", 32), "task_ch1", 1},
		{strings.Repeat("b", 32), "task_ch2", 2},
		{strings.Repeat("c", 32), "task_ch3", 3},
		{strings.Repeat("d", 32), "task_ch4", 4},
		{strings.Repeat("e", 32), "task_future", 8},
	}
	revision := 5
	for _, spec := range taskSpecs {
		input := testRecordStage(t, w, id, spec.op, "task-add", map[string]any{
			"id": spec.id, "title": "虚构任务 " + spec.id,
			"plannedChapter": spec.planned, "priority": "normal", "status": "todo",
			"compatibilityMarker": "应原样保留",
		})
		if _, err := w.mutateFromFile(id, input, revision); err != nil {
			t.Fatal(err)
		}
		revision++
	}

	first := stageChapterOrder(t, w, id, strings.Repeat("f", 32),
		[]any{"ch_4", "ch_2", "ch_3", "ch_1"}, true)
	result, err := w.mutateFromFile(id, first, revision)
	if err != nil {
		t.Fatal(err)
	}
	revision++
	afterFirst := persistedTasks(t, w, id)
	if got, want := plannedChapters(afterFirst), map[string]int{
		"task_ch1": 4, "task_ch2": 2, "task_ch3": 3, "task_ch4": 1, "task_future": 8,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("首次复杂调序映射错误：got=%v want=%v", got, want)
	}
	if len(result.(map[string]any)["remappedTasks"].([]map[string]any)) != 2 {
		t.Fatalf("未受影响任务被错误计入映射：%v", result)
	}
	for _, task := range persistedTasks(t, w, id) {
		if task["compatibilityMarker"] != "应原样保留" {
			t.Fatalf("旧任务扩展字段被破坏：%v", task)
		}
	}

	retry, err := w.mutateFromFile(id, first, revision-1)
	if err != nil {
		t.Fatal(err)
	}
	if retry.(map[string]any)["idempotent"] != true || retry.(map[string]any)["revision"] != revision {
		t.Fatalf("同一 operationId 重试未幂等返回原提交：%v", retry)
	}
	if afterRetry := persistedTasks(t, w, id); !reflect.DeepEqual(afterRetry, afterFirst) {
		t.Fatalf("同一 operationId 重试后任务被再次映射：before=%v after=%v", afterFirst, afterRetry)
	}

	second := stageChapterOrder(t, w, id, "06060606060606060606060606060606",
		[]any{"ch_2", "ch_1", "ch_4", "ch_3"}, true)
	if _, err := w.mutateFromFile(id, second, revision); err != nil {
		t.Fatal(err)
	}
	revision++
	if got, want := plannedChapters(persistedTasks(t, w, id)), map[string]int{
		"task_ch1": 2, "task_ch2": 1, "task_ch3": 4, "task_ch4": 3, "task_future": 8,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("连续调序未按上一次叙事位置映射：got=%v want=%v", got, want)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
	_, head, manifest, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	if head.Revision != revision || !reflect.DeepEqual(manifest.ChapterOrder, []string{"ch_2", "ch_1", "ch_4", "ch_3"}) {
		t.Fatalf("重新读取后的版本或章节顺序错误：revision=%d order=%v", head.Revision, manifest.ChapterOrder)
	}
}

func TestChapterOrderRemapTasksRejectsInvalidOrderAtomically(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	for index, chapterID := range []string{"ch_1", "ch_2", "ch_3"} {
		op := strings.Repeat(string(rune('7'+index)), 32)
		input := testStage(t, w, id, op, chapterID, "虚构正文 "+chapterID)
		if _, err := w.appendFromFile(id, input, index+1); err != nil {
			t.Fatal(err)
		}
	}
	task := testRecordStage(t, w, id, strings.Repeat("a", 32), "task-add", map[string]any{
		"id": "task_atomic", "title": "原子性任务", "plannedChapter": 1,
		"priority": "high", "status": "todo",
	})
	if _, err := w.mutateFromFile(id, task, 4); err != nil {
		t.Fatal(err)
	}

	_, beforeHead, beforeManifest, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	beforeTasks := persistedTasks(t, w, id)
	cases := []struct {
		name, op, wantError string
		order               []any
	}{
		{"duplicate", strings.Repeat("b", 32), "未知或重复", []any{"ch_1", "ch_1", "ch_3"}},
		{"missing", strings.Repeat("c", 32), "覆盖全部", []any{"ch_1", "ch_2"}},
		{"unknown", strings.Repeat("d", 32), "未知或重复", []any{"ch_1", "ch_2", "ch_missing"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := stageChapterOrder(t, w, id, tc.op, tc.order, true)
			if _, err := w.mutateFromFile(id, input, beforeHead.Revision); err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("非法调序未按契约拒绝：%v", err)
			}
			_, afterHead, afterManifest, err := w.load(id)
			if err != nil {
				t.Fatal(err)
			}
			if afterHead != beforeHead || afterManifest.TasksPath != beforeManifest.TasksPath ||
				!reflect.DeepEqual(afterManifest.ChapterOrder, beforeManifest.ChapterOrder) ||
				!reflect.DeepEqual(persistedTasks(t, w, id), beforeTasks) {
				t.Fatalf("失败操作留下部分修改：HEAD=%v tasks=%s order=%v", afterHead, afterManifest.TasksPath, afterManifest.ChapterOrder)
			}
		})
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
}
