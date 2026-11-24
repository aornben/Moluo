package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type rollbackFixture struct {
	w                    *workspace
	id                   string
	revision             int
	oldSHA, currentSHA   string
	oldPath, currentPath string
}

func newRollbackFixture(t *testing.T) rollbackFixture {
	t.Helper()
	w := testWorkspace(t)
	id := testInit(t, w)
	oldInput := testStage(t, w, id, strings.Repeat("1", 32), "ch_1", "阿甲在旧桥捡到钥匙。")
	raw, _ := os.ReadFile(oldInput)
	var oldDraft draft
	if err := json.Unmarshal(raw, &oldDraft); err != nil {
		t.Fatal(err)
	}
	oldDraft.Card = map[string]any{"chapterId": "ch_1", "facts": []any{map[string]any{
		"id": "fact_key", "subject": "钥匙在旧桥", "sourceChapterId": "ch_1", "certainty": "certain", "evidence": "在旧桥捡到钥匙",
	}}}
	raw, _ = json.Marshal(oldDraft)
	if err := os.WriteFile(oldInput, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.appendFromFile(id, oldInput, 1); err != nil {
		t.Fatal(err)
	}
	root, _, manifest, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	oldEntries, err := w.entries(root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	oldEntry := oldEntries[0]

	newInput := testStage(t, w, id, strings.Repeat("2", 32), "ch_1", "阿甲在新桥放下钥匙。")
	raw, _ = os.ReadFile(newInput)
	var newDraft draft
	if err := json.Unmarshal(raw, &newDraft); err != nil {
		t.Fatal(err)
	}
	newDraft.Mode = "replace"
	newDraft.Confirm = true
	newDraft.ExpectedChapterSHA256 = oldEntry.SHA256
	newDraft.Card = map[string]any{"chapterId": "ch_1", "facts": []any{map[string]any{
		"id": "fact_key", "subject": "钥匙在新桥", "sourceChapterId": "ch_1", "certainty": "certain", "evidence": "在新桥放下钥匙",
	}}}
	raw, _ = json.Marshal(newDraft)
	if err := os.WriteFile(newInput, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.appendFromFile(id, newInput, 2); err != nil {
		t.Fatal(err)
	}
	root, _, manifest, err = w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	currentEntries, err := w.entries(root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	return rollbackFixture{w: w, id: id, revision: 3,
		oldSHA: oldEntry.SHA256, currentSHA: currentEntries[0].SHA256,
		oldPath: oldEntry.Path, currentPath: currentEntries[0].Path}
}

func stageRollback(t *testing.T, fixture rollbackFixture, operationID string, targetRevision int,
	targetSHA, currentSHA, decision string, confirm bool) string {
	t.Helper()
	dir := filepath.Join(fixture.w.inbox, fixture.id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, operationID+".json")
	data, err := json.Marshal(chapterRollbackCandidate{OperationID: operationID, ChapterID: "ch_1",
		TargetRevision: targetRevision, TargetChapterSHA256: targetSHA,
		ExpectedChapterSHA256: currentSHA, Confirm: confirm, AuthorDecision: decision})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestChapterHistoryAndAuthorizedRollbackPersistAudit(t *testing.T) {
	fixture := newRollbackFixture(t)
	first, err := fixture.w.chapterHistory(fixture.id, "ch_1", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	firstPage := first.(map[string]any)
	if firstPage["total"] != 2 || firstPage["complete"] != false || firstPage["nextCursor"] != 1 {
		t.Fatalf("章节历史首分页错误：%v", firstPage)
	}
	firstItem := firstPage["items"].([]any)[0].(map[string]any)
	if intValue(firstItem["revision"]) != 2 || firstItem["sha256"] != fixture.oldSHA || firstItem["cardSha256"] == "" {
		t.Fatalf("旧修订历史来源不完整：%v", firstItem)
	}
	second, err := fixture.w.chapterHistory(fixture.id, "ch_1", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	secondItem := second.(map[string]any)["items"].([]any)[0].(map[string]any)
	if intValue(secondItem["revision"]) != 3 || secondItem["sha256"] != fixture.currentSHA {
		t.Fatalf("当前修订历史来源错误：%v", secondItem)
	}

	op := strings.Repeat("a", 32)
	input := stageRollback(t, fixture, op, 2, fixture.oldSHA, fixture.currentSHA,
		"作者确认将第一章恢复到 revision 2 的旧桥版本", true)
	result, err := fixture.w.rollbackChapterFromFile(fixture.id, input, fixture.revision)
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["revision"] != 4 || result.(map[string]any)["rollbackToRevision"] != 2 ||
		result.(map[string]any)["authorDecisionRecorded"] != true {
		t.Fatalf("章节回退结果缺少版本或授权信息：%v", result)
	}
	page, err := fixture.w.readChapter(fixture.id, "ch_1", 0, 100)
	if err != nil || page.(map[string]any)["text"] != "阿甲在旧桥捡到钥匙。" {
		t.Fatalf("回退后磁盘正文不一致：%v %v", page, err)
	}
	root, head, manifest, err := fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fixture.w.entries(root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Path == fixture.oldPath || entries[0].Path == fixture.currentPath {
		t.Fatal("回退覆盖了历史正文，而不是创建新不可变修订")
	}
	for _, path := range []string{fixture.oldPath, fixture.currentPath} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			t.Fatalf("回退后历史正文丢失：%s：%v", path, err)
		}
	}
	var memory map[string]any
	if err := loadJSON(root, manifest.MemoryPath, &memory); err != nil {
		t.Fatal(err)
	}
	fact := memory["activeFacts"].([]any)[0].(map[string]any)
	if fact["subject"] != "钥匙在旧桥" || fact["evidence"] != "在旧桥捡到钥匙" {
		t.Fatalf("回退后长期记忆未按历史证据卡重建：%v", fact)
	}
	var operation opRecord
	commitOperationID := rollbackCommitOperationID(op)
	if err := loadJSON(root, "operations/"+commitOperationID+".json", &operation); err != nil {
		t.Fatal(err)
	}
	if operation.Kind != "chapter-rollback" || operation.RequestOperationID != op || operation.RecordID != "ch_1" || operation.TargetRevision != 2 ||
		operation.TargetChapterSHA256 != fixture.oldSHA || !strings.Contains(operation.AuthorDecision, "作者确认") {
		t.Fatalf("回退操作授权审计不完整：%+v", operation)
	}
	var request chapterRollbackAudit
	if err := loadJSON(root, "rollback-requests/"+op+".json", &request); err != nil ||
		request.TargetManifestSHA256 == "" || request.TargetCardSHA256 == "" || request.CandidateInputSHA256 == "" {
		t.Fatalf("回退请求没有持久化信任来源：%+v %v", request, err)
	}
	if head.Revision != 4 || !fixture.w.backupExists(fixture.id, 4) {
		t.Fatal("回退提交未形成新 HEAD 或独立备份")
	}

	retry, err := fixture.w.rollbackChapterFromFile(fixture.id, input, fixture.revision)
	if err != nil || retry.(map[string]any)["idempotent"] != true || retry.(map[string]any)["revision"] != 4 {
		t.Fatalf("章节回退同 operationId 重试不幂等：%v %v", retry, err)
	}
	reopened, err := openWorkspace(fixture.w.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.doctor(fixture.id); err != nil {
		t.Fatalf("重启回读后 doctor 失败：%v", err)
	}
	history, err := reopened.chapterHistory(fixture.id, "ch_1", 0, 20)
	if err != nil || history.(map[string]any)["total"] != 3 {
		t.Fatalf("回退后的新修订没有进入历史：%v %v", history, err)
	}

	changed := stageRollback(t, fixture, op, 2, fixture.oldSHA, fixture.currentSHA,
		"冒用相同 operationId 的不同作者决定", true)
	if _, err := reopened.rollbackChapterFromFile(fixture.id, changed, fixture.revision); err == nil || !strings.Contains(err.Error(), "既有授权请求不一致") {
		t.Fatalf("不同授权冒用相同 operationId 未拒绝：%v", err)
	}
}

func TestChapterRollbackRejectsUnauthorizedConflictAndDamageAtomically(t *testing.T) {
	tests := []struct {
		name, decision, targetSHA, currentSHA, want string
		targetRevision                              int
		confirm                                     bool
	}{
		{"without-confirm", "作者同意", "old", "current", "confirm", 2, false},
		{"without-decision", "", "old", "current", "作者决定", 2, true},
		{"missing-target-chapter", "作者同意", "old", "current", "不存在", 1, true},
		{"bad-target-sha", "作者同意", strings.Repeat("f", 64), "current", "目标章节 SHA", 2, true},
		{"bad-current-sha", "作者同意", "old", strings.Repeat("e", 64), "当前章节 SHA", 2, true},
	}
	for index, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newRollbackFixture(t)
			targetSHA := tc.targetSHA
			if targetSHA == "old" {
				targetSHA = fixture.oldSHA
			}
			currentSHA := tc.currentSHA
			if currentSHA == "current" {
				currentSHA = fixture.currentSHA
			}
			op := fmt.Sprintf("%032x", 0xd000+index)
			input := stageRollback(t, fixture, op, tc.targetRevision, targetSHA, currentSHA, tc.decision, tc.confirm)
			if _, err := fixture.w.rollbackChapterFromFile(fixture.id, input, fixture.revision); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("非法回退没有按契约拒绝：%v", err)
			}
			_, head, _, err := fixture.w.load(fixture.id)
			if err != nil || head.Revision != fixture.revision {
				t.Fatalf("失败回退改变了 HEAD：%+v %v", head, err)
			}
			page, err := fixture.w.readChapter(fixture.id, "ch_1", 0, 100)
			if err != nil || page.(map[string]any)["text"] != "阿甲在新桥放下钥匙。" {
				t.Fatalf("失败回退改变了正文：%v %v", page, err)
			}
		})
	}

	t.Run("task-evidence", func(t *testing.T) {
		fixture := newRollbackFixture(t)
		task := testRecordStage(t, fixture.w, fixture.id, strings.Repeat("b", 32), "task-add", map[string]any{
			"id": "task_new_bridge", "title": "新桥放下钥匙", "plannedChapter": float64(1), "priority": "normal", "status": "done",
			"completedChapterId": "ch_1", "completionEvidence": "在新桥放下钥匙",
		})
		if _, err := fixture.w.mutateFromFile(fixture.id, task, fixture.revision); err != nil {
			t.Fatal(err)
		}
		fixture.revision++
		op := strings.Repeat("c", 32)
		input := stageRollback(t, fixture, op, 2, fixture.oldSHA, fixture.currentSHA, "作者同意回退", true)
		if _, err := fixture.w.rollbackChapterFromFile(fixture.id, input, fixture.revision); err == nil || !strings.Contains(err.Error(), "待办证据") {
			t.Fatalf("会破坏任务证据的回退未拒绝：%v", err)
		}
		_, head, _, _ := fixture.w.load(fixture.id)
		if head.Revision != fixture.revision {
			t.Fatal("任务证据失败后 HEAD 发生变化")
		}
	})

	t.Run("tampered-backup-object", func(t *testing.T) {
		fixture := newRollbackFixture(t)
		target, err := fixture.w.historicalChapterAtRevision(fixture.id, "ch_1", 2)
		if err != nil {
			t.Fatal(err)
		}
		backupRoot, snapshot, err := fixture.w.loadSnapshot(fixture.id, 2)
		if err != nil {
			t.Fatal(err)
		}
		hash := snapshot.Files[target.Entry.Path]
		if err := os.WriteFile(filepath.Join(backupRoot, filepath.FromSlash(objectPath(hash))), []byte("tampered"), 0600); err != nil {
			t.Fatal(err)
		}
		input := stageRollback(t, fixture, strings.Repeat("d", 32), 2, fixture.oldSHA, fixture.currentSHA, "作者同意回退", true)
		if _, err := fixture.w.rollbackChapterFromFile(fixture.id, input, fixture.revision); err == nil || !strings.Contains(err.Error(), "哈希不一致") {
			t.Fatalf("损坏历史备份仍被激活：%v", err)
		}
		_, head, _, _ := fixture.w.load(fixture.id)
		if head.Revision != fixture.revision {
			t.Fatal("损坏备份拒绝后 HEAD 发生变化")
		}
	})
}

func TestChapterRollbackRejectsExistingMemoryArchive(t *testing.T) {
	fixture := newRollbackFixture(t)
	update := testStage(t, fixture.w, fixture.id, strings.Repeat("3", 32), "ch_2", "薄舟把钥匙带到钟楼。")
	raw, _ := os.ReadFile(update)
	var candidate draft
	if err := json.Unmarshal(raw, &candidate); err != nil {
		t.Fatal(err)
	}
	candidate.Card = map[string]any{"chapterId": "ch_2", "facts": []any{map[string]any{
		"id": "fact_key", "subject": "钥匙在钟楼", "sourceChapterId": "ch_2", "certainty": "certain", "evidence": "钥匙带到钟楼",
	}}}
	raw, _ = json.Marshal(candidate)
	if err := os.WriteFile(update, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.w.appendFromFile(fixture.id, update, fixture.revision); err != nil {
		t.Fatal(err)
	}
	fixture.revision++
	for number := 3; number <= 52; number++ {
		input := testStage(t, fixture.w, fixture.id, fmt.Sprintf("%032x", 0xe000+number), fmt.Sprintf("ch_%d", number), "虚构归档推进正文。")
		if _, err := fixture.w.appendFromFile(fixture.id, input, fixture.revision); err != nil {
			t.Fatal(err)
		}
		fixture.revision++
	}
	_, _, manifest, err := fixture.w.load(fixture.id)
	if err != nil || len(manifest.MemoryArchives) == 0 {
		t.Fatalf("测试未形成真实记忆归档：%v %v", manifest.MemoryArchives, err)
	}
	input := stageRollback(t, fixture, strings.Repeat("e", 32), 2, fixture.oldSHA, fixture.currentSHA, "作者同意回退", true)
	if _, err := fixture.w.rollbackChapterFromFile(fixture.id, input, fixture.revision); err == nil || !strings.Contains(err.Error(), "归档") {
		t.Fatalf("会破坏归档证据的回退未被拒绝：%v", err)
	}
	if _, err := fixture.w.doctor(fixture.id); err != nil {
		t.Fatal(err)
	}
}

func TestChapterRollbackCrashRecoveryAndIdempotence(t *testing.T) {
	for _, point := range []string{"append:before-head-switch", "append:head-switched"} {
		t.Run(strings.ReplaceAll(point, ":", "-"), func(t *testing.T) {
			fixture := newRollbackFixture(t)
			op := fmt.Sprintf("%032x", 0xf100+len(point))
			input := stageRollback(t, fixture, op, 2, fixture.oldSHA, fixture.currentSHA, "作者确认故障恢复回退", true)
			originalFault := transactionFault
			transactionFault = func(actual string) error {
				if actual == point {
					return errors.New("rollback injected interruption")
				}
				return nil
			}
			_, failure := fixture.w.rollbackChapterFromFile(fixture.id, input, fixture.revision)
			transactionFault = originalFault
			if failure == nil || !strings.Contains(failure.Error(), "injected interruption") {
				t.Fatalf("章节回退故障点没有中断：%v", failure)
			}
			reopened, err := openWorkspace(fixture.w.root)
			if err != nil {
				t.Fatal(err)
			}
			if point == "append:before-head-switch" {
				assertCurrentHead(t, reopened, fixture.id, fixture.revision, "")
				if _, err := reopened.recoverPending(fixture.id, rollbackCommitOperationID(op), true); err != nil {
					t.Fatal(err)
				}
				newOp := strings.Repeat("9", 32)
				newInput := stageRollback(t, fixture, newOp, 2, fixture.oldSHA, fixture.currentSHA, "作者确认重新执行回退", true)
				if _, err := reopened.rollbackChapterFromFile(fixture.id, newInput, fixture.revision); err != nil {
					t.Fatalf("放弃 HEAD 前事务后新操作不能回退：%v", err)
				}
			} else {
				assertCurrentHead(t, reopened, fixture.id, fixture.revision+1, "")
				retry, err := reopened.rollbackChapterFromFile(fixture.id, input, fixture.revision)
				if err != nil || retry.(map[string]any)["idempotent"] != true {
					t.Fatalf("HEAD 后中断同操作不能幂等补写：%v %v", retry, err)
				}
			}
			if _, err := reopened.doctor(fixture.id); err != nil {
				t.Fatalf("章节回退故障恢复后 doctor 失败：%v", err)
			}
			page, err := reopened.readChapter(fixture.id, "ch_1", 0, 100)
			if err != nil || page.(map[string]any)["text"] != "阿甲在旧桥捡到钥匙。" {
				t.Fatalf("故障恢复后回退正文不一致：%v %v", page, err)
			}
		})
	}
}
