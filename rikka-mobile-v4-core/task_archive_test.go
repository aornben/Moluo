package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type taskArchiveFixture struct {
	w        *workspace
	id       string
	revision int
}

func stageConfirmedTaskOperation(t *testing.T, fixture taskArchiveFixture, operationID, kind string, record map[string]any, confirm bool) string {
	t.Helper()
	path := testRecordStage(t, fixture.w, fixture.id, operationID, kind, record)
	if !confirm {
		return path
	}
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

func newTaskArchiveFixture(t *testing.T) taskArchiveFixture {
	t.Helper()
	w := testWorkspace(t)
	id := testInit(t, w)
	contents := []string{
		"第一章虚构证据：钥匙已经交还。",
		"第二章虚构证据：密信已经烧毁。",
		"第三章虚构正文，没有任务核销。",
		"第四章虚构证据：旧桥已经封闭。",
	}
	for index, content := range contents {
		chapterID := "ch_" + string(rune('1'+index))
		operationID := strings.Repeat(string(rune('1'+index)), 32)
		input := testStage(t, w, id, operationID, chapterID, content)
		if _, err := w.appendFromFile(id, input, index+1); err != nil {
			t.Fatal(err)
		}
	}
	fixture := taskArchiveFixture{w: w, id: id, revision: 5}
	tasks := []struct {
		operationID string
		record      map[string]any
	}{
		{strings.Repeat("a", 32), map[string]any{
			"id": "task_todo", "title": "仍未完成的虚构任务", "plannedChapter": 2,
			"priority": "normal", "status": "todo", "compatibilityMarker": "保留扩展字段",
		}},
		{strings.Repeat("b", 32), map[string]any{
			"id": "task_done_1", "title": "第一章完成的虚构任务", "plannedChapter": 3,
			"priority": "high", "status": "done", "completedChapterId": "ch_1",
			"completionEvidence": "钥匙已经交还。",
		}},
		{strings.Repeat("c", 32), map[string]any{
			"id": "task_done_2", "title": "第二章完成的虚构任务", "plannedChapter": 4,
			"priority": "normal", "status": "done", "completedChapterId": "ch_2",
			"completionEvidence": "密信已经烧毁。",
		}},
		{strings.Repeat("d", 32), map[string]any{
			"id": "task_done_4", "title": "第四章完成的虚构任务", "plannedChapter": 1,
			"priority": "normal", "status": "done", "completedChapterId": "ch_4",
			"completionEvidence": "旧桥已经封闭。",
		}},
	}
	for _, item := range tasks {
		input := testRecordStage(t, w, id, item.operationID, "task-add", item.record)
		if _, err := w.mutateFromFile(id, input, fixture.revision); err != nil {
			t.Fatal(err)
		}
		fixture.revision++
	}
	return fixture
}

func taskByID(tasks []map[string]any, id string) map[string]any {
	for _, task := range tasks {
		if task["id"] == id {
			return task
		}
	}
	return nil
}

func taskIDs(tasks []map[string]any) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		id, _ := task["id"].(string)
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func currentTaskArchive(t *testing.T, fixture taskArchiveFixture, index int) (string, head, manifest, taskArchiveSnapshot) {
	t.Helper()
	root, h, m, err := fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	if index < 0 || index >= len(m.TaskArchives) {
		t.Fatalf("归档下标越界：%d/%d", index, len(m.TaskArchives))
	}
	archive, err := loadTaskArchive(root, m.TaskArchives[index])
	if err != nil {
		t.Fatal(err)
	}
	return root, h, m, archive
}

func resealCurrentManifest(t *testing.T, fixture taskArchiveFixture, mutate func(root string, m *manifest)) {
	t.Helper()
	root, h, m, err := fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	mutate(root, &m)
	if err := atomicJSON(root, h.Manifest, m); err != nil {
		t.Fatal(err)
	}
	hash, err := fileSHA(root, h.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	h.ManifestSHA256 = hash
	if err := atomicJSON(root, "HEAD.json", h); err != nil {
		t.Fatal(err)
	}
}

func TestTaskDeleteAuthorizationArchiveAndIdempotence(t *testing.T) {
	fixture := newTaskArchiveFixture(t)
	beforeTasks := persistedTasks(t, fixture.w, fixture.id)
	beforeTarget := copyMap(taskByID(beforeTasks, "task_todo"))
	_, beforeHead, beforeManifest, err := fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}

	rejections := []struct {
		name, operationID, want string
		confirm                 bool
		record                  map[string]any
	}{
		{"without-confirm", strings.Repeat("5", 32), "明确确认", false, map[string]any{"id": "task_todo", "authorDecision": "作者决定删除"}},
		{"without-decision", strings.Repeat("6", 32), "作者决定", true, map[string]any{"id": "task_todo"}},
		{"invalid-id", strings.Repeat("7", 32), "记录 ID 无效", true, map[string]any{"id": "bad id", "authorDecision": "作者决定删除"}},
		{"missing-task", strings.Repeat("8", 32), "待办不存在", true, map[string]any{"id": "task_missing", "authorDecision": "作者决定删除"}},
	}
	for _, tc := range rejections {
		t.Run(tc.name, func(t *testing.T) {
			input := stageConfirmedTaskOperation(t, fixture, tc.operationID, "task-delete", tc.record, tc.confirm)
			if _, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("非法删除未按契约拒绝：%v", err)
			}
			_, h, m, err := fixture.w.load(fixture.id)
			if err != nil {
				t.Fatal(err)
			}
			if h != beforeHead || m.TasksPath != beforeManifest.TasksPath || len(m.TaskArchives) != 0 ||
				!reflect.DeepEqual(persistedTasks(t, fixture.w, fixture.id), beforeTasks) {
				t.Fatal("失败的删除操作改变了 HEAD、任务或归档")
			}
		})
	}

	operationID := strings.Repeat("9", 32)
	decision := "作者确认该测试任务登记错误，保留归档后删除"
	input := stageConfirmedTaskOperation(t, fixture, operationID, "task-delete",
		map[string]any{"id": "task_todo", "authorDecision": decision}, true)
	result, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision)
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["archivedCount"] != 1 {
		t.Fatalf("删除未报告一条归档：%v", result)
	}
	committedRevision := fixture.revision + 1
	root, h, m, archive := currentTaskArchive(t, fixture, 0)
	if h.Revision != committedRevision || len(m.TaskArchives) != 1 || archive.Kind != "deleted" || archive.Count != 1 ||
		archive.Path != expectedTaskArchivePath("task-delete", committedRevision, operationID, 0) ||
		m.TaskArchives[0]["taskId"] != "task_todo" || m.TaskArchives[0]["authorDecision"] != decision ||
		m.TaskArchives[0]["operationId"] != operationID || !reflect.DeepEqual(archive.Tasks[0], beforeTarget) {
		t.Fatalf("删除归档元数据或完整原记录不一致：descriptor=%v archive=%v", m.TaskArchives[0], archive.Tasks)
	}
	actualHash, err := fileSHA(root, archive.Path)
	if err != nil || actualHash != archive.SHA256 {
		t.Fatal("删除归档 SHA-256 与磁盘不一致", err)
	}
	if taskByID(persistedTasks(t, fixture.w, fixture.id), "task_todo") != nil {
		t.Fatal("已删除任务仍在活动任务中")
	}
	page, err := fixture.w.query(fixture.id, "tasks", "", "", 0, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	archivedSeen := false
	for _, raw := range page.(map[string]any)["items"].([]any) {
		item := raw.(map[string]any)
		if item["id"] == "task_todo" {
			archivedSeen = item["archived"] == true && item["archivePath"] == archive.Path
		}
	}
	if !archivedSeen {
		t.Fatal("query 未回读带归档标记的完整删除记录")
	}
	if _, err := fixture.w.doctor(fixture.id); err != nil {
		t.Fatal(err)
	}

	retry, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision)
	if err != nil || retry.(map[string]any)["idempotent"] != true || retry.(map[string]any)["revision"] != committedRevision {
		t.Fatalf("删除重复请求未幂等返回：%v %v", retry, err)
	}
	_, retryHead, retryManifest, err := fixture.w.load(fixture.id)
	if err != nil || retryHead.Revision != committedRevision || len(retryManifest.TaskArchives) != 1 {
		t.Fatal("删除重复请求产生了新版本或重复归档")
	}
	if err := os.Remove(filepath.Join(root, "operations", operationID+".json")); err != nil {
		t.Fatal(err)
	}
	recovered, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision)
	if err != nil || recovered.(map[string]any)["idempotent"] != true {
		t.Fatalf("删除提交后的操作记录未安全补写：%v %v", recovered, err)
	}

	deleteAgain := stageConfirmedTaskOperation(t, fixture, strings.Repeat("e", 32), "task-delete",
		map[string]any{"id": "task_todo", "authorDecision": "再次删除"}, true)
	if _, err := fixture.w.mutateFromFile(fixture.id, deleteAgain, committedRevision); err == nil || !strings.Contains(err.Error(), "待办不存在") {
		t.Fatalf("不同操作重复删除同一任务未拒绝：%v", err)
	}
	reAdd := testRecordStage(t, fixture.w, fixture.id, strings.Repeat("f", 32), "task-add", beforeTarget)
	if _, err := fixture.w.mutateFromFile(fixture.id, reAdd, committedRevision); err == nil || !strings.Contains(err.Error(), "已存在于归档") {
		t.Fatalf("归档任务 ID 被重新用于活动任务：%v", err)
	}
}

func TestTaskArchiveDoneSelectionManifestQueryAndIdempotence(t *testing.T) {
	fixture := newTaskArchiveFixture(t)
	beforeTasks := persistedTasks(t, fixture.w, fixture.id)
	_, beforeHead, beforeManifest, err := fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}

	withoutConfirm := stageConfirmedTaskOperation(t, fixture, "05050505050505050505050505050505", "task-archive-done",
		map[string]any{"throughChapter": 2}, false)
	if _, err := fixture.w.mutateFromFile(fixture.id, withoutConfirm, fixture.revision); err == nil || !strings.Contains(err.Error(), "明确确认") {
		t.Fatalf("无确认的批量归档未拒绝：%v", err)
	}
	invalid := []struct {
		op    string
		value any
	}{
		{"06060606060606060606060606060606", 0},
		{"07070707070707070707070707070707", 2.5},
		{"08080808080808080808080808080808", "2"},
		{"09090909090909090909090909090909", 10000},
	}
	for _, tc := range invalid {
		input := stageConfirmedTaskOperation(t, fixture, tc.op, "task-archive-done", map[string]any{"throughChapter": tc.value}, true)
		if _, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision); err == nil || !strings.Contains(err.Error(), "throughChapter") {
			t.Fatalf("非法 throughChapter %v 未拒绝：%v", tc.value, err)
		}
	}
	_, afterRejectHead, afterRejectManifest, err := fixture.w.load(fixture.id)
	if err != nil || afterRejectHead != beforeHead || afterRejectManifest.TasksPath != beforeManifest.TasksPath ||
		!reflect.DeepEqual(persistedTasks(t, fixture.w, fixture.id), beforeTasks) {
		t.Fatal("批量归档非法输入改变了活动状态")
	}

	operationID := "abababababababababababababababab"
	input := stageConfirmedTaskOperation(t, fixture, operationID, "task-archive-done", map[string]any{"throughChapter": 2}, true)
	result, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision)
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["archivedCount"] != 2 {
		t.Fatalf("已完成任务归档数量错误：%v", result)
	}
	committedRevision := fixture.revision + 1
	root, h, m, archive := currentTaskArchive(t, fixture, 0)
	if h.Revision != committedRevision || archive.Kind != "done" || archive.Count != 2 || archive.FromChapter != 1 ||
		archive.ThroughChapter != 2 || archive.LastCompletedChapter != 2 ||
		archive.Path != expectedTaskArchivePath("task-archive-done", committedRevision, operationID, 2) ||
		m.TaskArchives[0]["operationId"] != operationID {
		t.Fatalf("已完成任务归档描述不完整：%v", m.TaskArchives[0])
	}
	if got, want := taskIDs(archive.Tasks), []string{"task_done_1", "task_done_2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("完成状态/章节范围选择错误：got=%v want=%v", got, want)
	}
	if got, want := taskIDs(persistedTasks(t, fixture.w, fixture.id)), []string{"task_done_4", "task_todo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("未归档任务被遗漏或误删：got=%v want=%v", got, want)
	}
	actualHash, err := fileSHA(root, archive.Path)
	if err != nil || actualHash != archive.SHA256 {
		t.Fatal("批量归档 SHA-256 与磁盘不一致", err)
	}
	page, err := fixture.w.query(fixture.id, "tasks", "", "", 0, 20, 0)
	if err != nil || page.(map[string]any)["total"] != 4 {
		t.Fatalf("query 未合并活动与归档任务：%v %v", page, err)
	}
	if _, err := fixture.w.doctor(fixture.id); err != nil {
		t.Fatal(err)
	}
	retry, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision)
	if err != nil || retry.(map[string]any)["idempotent"] != true {
		t.Fatalf("批量归档重复请求未幂等返回：%v %v", retry, err)
	}
	_, retryHead, retryManifest, err := fixture.w.load(fixture.id)
	if err != nil || retryHead.Revision != committedRevision || len(retryManifest.TaskArchives) != 1 {
		t.Fatal("批量归档重复请求产生新版本或重复归档")
	}
	noEligible := stageConfirmedTaskOperation(t, fixture, "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd", "task-archive-done",
		map[string]any{"throughChapter": 2}, true)
	if _, err := fixture.w.mutateFromFile(fixture.id, noEligible, committedRevision); err == nil || !strings.Contains(err.Error(), "没有可归档") {
		t.Fatalf("没有符合条件的任务仍创建归档：%v", err)
	}
}

func addSecondDoneTaskAndArchive(t *testing.T, fixture taskArchiveFixture) (taskArchiveFixture, string, string, int) {
	t.Helper()
	firstOp := "abababababababababababababababab"
	first := stageConfirmedTaskOperation(t, fixture, firstOp, "task-archive-done", map[string]any{"throughChapter": 2}, true)
	if _, err := fixture.w.mutateFromFile(fixture.id, first, fixture.revision); err != nil {
		t.Fatal(err)
	}
	fixture.revision++
	addOp := "acacacacacacacacacacacacacacacac"
	add := testRecordStage(t, fixture.w, fixture.id, addOp, "task-add", map[string]any{
		"id": "task_done_later", "title": "稍后登记的第二章完成任务", "plannedChapter": 5,
		"priority": "normal", "status": "done", "completedChapterId": "ch_2", "completionEvidence": "密信已经烧毁。",
	})
	if _, err := fixture.w.mutateFromFile(fixture.id, add, fixture.revision); err != nil {
		t.Fatal(err)
	}
	fixture.revision++
	secondBase := fixture.revision
	secondOp := "adadadadadadadadadadadadadadadad"
	second := stageConfirmedTaskOperation(t, fixture, secondOp, "task-archive-done", map[string]any{"throughChapter": 2}, true)
	if _, err := fixture.w.mutateFromFile(fixture.id, second, secondBase); err != nil {
		t.Fatal(err)
	}
	fixture.revision++
	return fixture, second, secondOp, secondBase
}

func TestTaskArchiveDoneRecoveryUsesExactOperationArchive(t *testing.T) {
	fixture, secondInput, secondOp, secondBase := addSecondDoneTaskAndArchive(t, newTaskArchiveFixture(t))
	root, _, m, err := fixture.w.load(fixture.id)
	if err != nil || len(m.TaskArchives) != 2 || intValue(m.TaskArchives[0]["throughChapter"]) != 2 || intValue(m.TaskArchives[1]["throughChapter"]) != 2 {
		t.Fatalf("未建立两个同截止章归档：%v %v", m.TaskArchives, err)
	}
	if err := os.Remove(filepath.Join(root, "operations", secondOp+".json")); err != nil {
		t.Fatal(err)
	}
	recovered, err := fixture.w.mutateFromFile(fixture.id, secondInput, secondBase)
	if err != nil || recovered.(map[string]any)["idempotent"] != true {
		t.Fatalf("多个同截止章归档时未恢复本次精确操作：%v %v", recovered, err)
	}
	if err := os.Remove(filepath.Join(root, "operations", secondOp+".json")); err != nil {
		t.Fatal(err)
	}
	resealCurrentManifest(t, fixture, func(_ string, m *manifest) {
		m.TaskArchives = append([]map[string]any(nil), m.TaskArchives[:1]...)
	})
	if _, err := fixture.w.mutateFromFile(fixture.id, secondInput, secondBase); err == nil || !strings.Contains(err.Error(), "找不到本次归档") {
		t.Fatalf("本次归档描述缺失时错误套用了同截止章旧归档：%v", err)
	}
}

func TestTaskArchiveTamperAndManifestMismatchDetection(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, fixture taskArchiveFixture)
	}{
		{"hash-mismatch", func(t *testing.T, fixture taskArchiveFixture) {
			root, _, _, archive := currentTaskArchive(t, fixture, 0)
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(archive.Path)), []byte("[]"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"count-mismatch", func(t *testing.T, fixture taskArchiveFixture) {
			resealCurrentManifest(t, fixture, func(_ string, m *manifest) { m.TaskArchives[0]["count"] = 3 })
		}},
		{"range-mismatch", func(t *testing.T, fixture taskArchiveFixture) {
			resealCurrentManifest(t, fixture, func(_ string, m *manifest) { m.TaskArchives[0]["throughChapter"] = 1 })
		}},
		{"invalid-path", func(t *testing.T, fixture taskArchiveFixture) {
			resealCurrentManifest(t, fixture, func(_ string, m *manifest) { m.TaskArchives[0]["path"] = "tasks/not-an-archive.json" })
		}},
		{"invalid-evidence-with-resealed-hash", func(t *testing.T, fixture taskArchiveFixture) {
			resealCurrentManifest(t, fixture, func(root string, m *manifest) {
				path, _ := m.TaskArchives[0]["path"].(string)
				var tasks []map[string]any
				if err := loadJSON(root, path, &tasks); err != nil {
					t.Fatal(err)
				}
				tasks[0]["completionEvidence"] = "正文中不存在的证据"
				if err := atomicJSON(root, path, tasks); err != nil {
					t.Fatal(err)
				}
				hash, err := fileSHA(root, path)
				if err != nil {
					t.Fatal(err)
				}
				m.TaskArchives[0]["sha256"] = hash
			})
		}},
		{"duplicate-descriptor", func(t *testing.T, fixture taskArchiveFixture) {
			resealCurrentManifest(t, fixture, func(_ string, m *manifest) {
				m.TaskArchives = append(m.TaskArchives, copyMap(m.TaskArchives[0]))
			})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newTaskArchiveFixture(t)
			input := stageConfirmedTaskOperation(t, fixture, "abababababababababababababababab", "task-archive-done",
				map[string]any{"throughChapter": 2}, true)
			if _, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision); err != nil {
				t.Fatal(err)
			}
			fixture.revision++
			tc.mutate(t, fixture)
			if _, err := fixture.w.query(fixture.id, "tasks", "", "", 0, 20, 0); err == nil {
				t.Fatal("query 未拒绝损坏的待办归档")
			}
			if _, err := fixture.w.doctor(fixture.id); err == nil {
				t.Fatal("doctor 未拒绝损坏的待办归档")
			}
		})
	}
}

func TestTaskArchiveFailureBeforeHeadSwitchIsAtomic(t *testing.T) {
	fixture := newTaskArchiveFixture(t)
	root, beforeHead, beforeManifest, err := fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	beforeTasks := persistedTasks(t, fixture.w, fixture.id)
	operationID := "abababababababababababababababab"
	archivePath := expectedTaskArchivePath("task-archive-done", fixture.revision+1, operationID, 2)
	if err := newJSON(root, archivePath, []map[string]any{{"id": "conflict"}}); err != nil {
		t.Fatal(err)
	}
	input := stageConfirmedTaskOperation(t, fixture, operationID, "task-archive-done", map[string]any{"throughChapter": 2}, true)
	if _, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision); err == nil {
		t.Fatal("归档目标文件冲突时操作错误成功")
	}
	_, afterHead, afterManifest, err := fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	if afterHead != beforeHead || afterManifest.TasksPath != beforeManifest.TasksPath ||
		!reflect.DeepEqual(afterManifest.TaskArchives, beforeManifest.TaskArchives) ||
		!reflect.DeepEqual(persistedTasks(t, fixture.w, fixture.id), beforeTasks) {
		t.Fatal("失败中断后出现任务部分归档、部分删除或 manifest 切换")
	}
	if err := fixture.w.pending(root, beforeHead.Revision); err == nil || !strings.Contains(err.Error(), "未完成事务") {
		t.Fatalf("失败中断未留下保守的待恢复事务：%v", err)
	}
}
