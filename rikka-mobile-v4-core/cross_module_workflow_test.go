package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type crossModuleFixture struct {
	w        *workspace
	id       string
	revision int
	sequence int
}

func (f *crossModuleFixture) operationID() string {
	f.sequence++
	return fmt.Sprintf("%032x", 0xc000+f.sequence)
}

func (f *crossModuleFixture) mutate(t *testing.T, kind string, record map[string]any, confirm bool) (map[string]any, string, error) {
	t.Helper()
	op := f.operationID()
	path := testRecordStage(t, f.w, f.id, op, kind, record)
	if confirm {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var candidate recordDraft
		if err := json.Unmarshal(raw, &candidate); err != nil {
			t.Fatal(err)
		}
		candidate.Confirm = true
		raw, err = json.Marshal(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := f.w.mutateFromFile(f.id, path, f.revision)
	if err != nil {
		return nil, path, err
	}
	f.revision++
	return result.(map[string]any), path, nil
}

func crossModuleBlueprint() map[string]any {
	plan := map[string]any{
		"theme": "虚构跨模块闭环", "mainPlot": map[string]any{"id": "main_cross", "summary": "追查铜钥匙与账本"},
		"subplots": []any{}, "volumes": []any{},
		"chapterOutline": []any{
			map[string]any{"id": "outline_cross_1", "title": "旧桥纸盒", "chapterNumber": float64(1), "summary": "阿澜在旧桥打开纸盒"},
			map[string]any{"id": "outline_cross_2", "title": "钟楼确认", "chapterNumber": float64(2), "summary": "薄舟在钟楼确认钥匙状态"},
		},
		"chapterLengths": []any{}, "characterArcs": []any{}, "pacing": []any{}, "majorEvents": []any{},
	}
	candidate := map[string]any{
		"chapterOutline": []any{map[string]any{
			"id": "candidate_twist", "title": "未确认的背叛", "chapterNumber": float64(1), "summary": "薄舟可能背叛阿澜",
		}},
		"subplots": []any{}, "volumes": []any{}, "chapterLengths": []any{},
		"characterArcs": []any{}, "pacing": []any{}, "majorEvents": []any{},
	}
	return map[string]any{"schemaVersion": float64(2), "plan": plan, "candidate": candidate, "canon": map[string]any{}}
}

type crossWorkflowResult struct {
	prepareID    string
	draftID      string
	reviewID     string
	finalPath    string
	baseRevision int
	finalInputID string
}

func crossReviewItems(requirements []prepareRequirement, quotes map[string]string) []reviewItem {
	items := make([]reviewItem, 0, len(requirements))
	for _, requirement := range requirements {
		item := reviewItem{ID: requirement.ID, Status: "satisfied"}
		if requirement.SourceType == "blueprint:candidate" {
			item.Status = "not_applicable"
			item.Note = "候选设定尚未得到作者确认，不能冒充正史"
		} else if requirement.Kind != "length" {
			item.Quote = quotes[requirement.ID]
			if item.Quote == "" {
				item.Quote = quotes[requirement.Kind]
			}
			if requirement.SemanticReview {
				item.Note = "已按完整叙事语义核查；程序不以代词规则冒充 POV 判断"
			}
		}
		items = append(items, item)
	}
	return items
}

func writeCrossWorkflowChapter(t *testing.T, fixture *crossModuleFixture, chapterID string, number int,
	title, content string, card map[string]any, quotes map[string]string, verifyMissingAuthorization bool) crossWorkflowResult {
	t.Helper()
	baseRevision := fixture.revision
	prepared, err := fixture.w.prepareChapter(fixture.id, chapterID, number, baseRevision)
	if err != nil {
		t.Fatal(err)
	}
	prepare := prepared.(map[string]any)
	prepareID := prepare["prepareId"].(string)
	requirements := prepare["requirements"].([]prepareRequirement)
	if context, err := fixture.w.readPrepare(fixture.id, prepareID, 0, 20); err != nil || context.(map[string]any)["complete"] != true {
		t.Fatalf("写作准备不能持久化回读：%v %v", context, err)
	}
	if err := os.MkdirAll(filepath.Join(fixture.w.inbox, fixture.id), 0700); err != nil {
		t.Fatal(err)
	}

	draftID := fixture.operationID()
	draftPath := filepath.Join(fixture.w.inbox, fixture.id, draftID+".json")
	draftData, err := json.Marshal(draftCandidate{OperationID: draftID, PrepareID: prepareID,
		Chapter: chapter{ID: chapterID, Title: title, Content: content}, Card: card})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draftPath, draftData, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.w.draftWorkflow(fixture.id, draftPath, baseRevision); err != nil {
		t.Fatal(err)
	}
	items := crossReviewItems(requirements, quotes)

	if verifyMissingAuthorization {
		badReviewID := fixture.operationID()
		badReviewPath := filepath.Join(fixture.w.inbox, fixture.id, badReviewID+".json")
		badReviewData, _ := json.Marshal(reviewCandidate{OperationID: badReviewID, PrepareID: prepareID,
			DraftID: draftID, Requirements: items})
		if err := os.WriteFile(badReviewPath, badReviewData, 0600); err != nil {
			t.Fatal(err)
		}
		badReview, err := fixture.w.reviewWorkflow(fixture.id, badReviewPath, baseRevision)
		if err != nil || badReview.(map[string]any)["verdict"] != "fail" ||
			!strings.Contains(strings.Join(badReview.(map[string]any)["defects"].([]string), "|"), "作者决定") {
			t.Fatalf("未授权豁免候选设定没有审稿失败：%v %v", badReview, err)
		}
		badFinalID := fixture.operationID()
		badFinalPath := filepath.Join(fixture.w.inbox, fixture.id, badFinalID+".json")
		badFinalData, _ := json.Marshal(finalCandidate{OperationID: badFinalID, PrepareID: prepareID,
			DraftID: draftID, ReviewID: badReviewID, Chapter: chapter{ID: chapterID, Title: title}})
		if err := os.WriteFile(badFinalPath, badFinalData, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.w.finalizeWorkflow(fixture.id, badFinalPath, baseRevision); err == nil || !strings.Contains(err.Error(), "审稿未通过") {
			t.Fatalf("未授权审稿仍能定稿：%v", err)
		}
	}

	reviewID := fixture.operationID()
	reviewPath := filepath.Join(fixture.w.inbox, fixture.id, reviewID+".json")
	reviewData, _ := json.Marshal(reviewCandidate{OperationID: reviewID, PrepareID: prepareID,
		DraftID: draftID, AuthorDecision: "作者明确确认未晋升的候选设定本章不适用", Requirements: items})
	if err := os.WriteFile(reviewPath, reviewData, 0600); err != nil {
		t.Fatal(err)
	}
	reviewed, err := fixture.w.reviewWorkflow(fixture.id, reviewPath, baseRevision)
	if err != nil || reviewed.(map[string]any)["verdict"] != "pass" {
		t.Fatalf("跨模块正文审稿未通过：%v %v", reviewed, err)
	}
	finalInputID := fixture.operationID()
	finalPath := filepath.Join(fixture.w.inbox, fixture.id, finalInputID+".json")
	finalData, _ := json.Marshal(finalCandidate{OperationID: finalInputID, PrepareID: prepareID,
		DraftID: draftID, ReviewID: reviewID, Chapter: chapter{ID: chapterID, Title: title}})
	if err := os.WriteFile(finalPath, finalData, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.w.finalizeWorkflow(fixture.id, finalPath, baseRevision)
	if err != nil || result.(map[string]any)["readback"] != "正文一致" {
		t.Fatalf("跨模块正文定稿或回读失败：%v %v", result, err)
	}
	fixture.revision++
	return crossWorkflowResult{prepareID: prepareID, draftID: draftID, reviewID: reviewID,
		finalPath: finalPath, baseRevision: baseRevision, finalInputID: finalInputID}
}

func TestCrossModuleWritingLifecycleArchiveAndIndependentRestore(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	other := testInit(t, w)
	fixture := crossModuleFixture{w: w, id: id, revision: 1}

	for _, character := range []map[string]any{{"id": "char_lan", "name": "阿澜"}, {"id": "char_bo", "name": "薄舟"}} {
		if _, _, err := fixture.mutate(t, "character-add", character, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := fixture.mutate(t, "blueprint-set", crossModuleBlueprint(), false); err != nil {
		t.Fatal(err)
	}
	for _, task := range []map[string]any{
		{"id": "task_box", "title": "阿澜必须打开纸盒", "plannedChapter": float64(1), "priority": "high", "status": "todo", "characterIds": []any{"char_lan"}},
		{"id": "task_key", "title": "薄舟必须确认钥匙完好", "plannedChapter": float64(2), "priority": "normal", "status": "todo", "characterIds": []any{"char_bo"}},
	} {
		if _, _, err := fixture.mutate(t, "task-add", task, false); err != nil {
			t.Fatal(err)
		}
	}

	chapterOne := "阿澜在旧桥打开纸盒，把铜钥匙交给薄舟。钟楼账本记载十枚银币须在2026年12月1日偿还。"
	cardOne := map[string]any{"chapterId": "ch_1",
		"facts":            []any{map[string]any{"id": "fact_key", "subject": "铜钥匙由薄舟保管", "certainty": "certain", "sourceChapterId": "ch_1", "evidence": "把铜钥匙交给薄舟"}},
		"knowledgeChanges": []any{map[string]any{"id": "know_debt", "characterId": "char_lan", "factId": "fact_debt", "beliefType": "fact", "learnedChapterId": "ch_1", "evidence": "账本记载十枚银币"}},
		"hookChanges":      []any{map[string]any{"id": "hook_key", "text": "铜钥匙将打开何处", "status": "open", "openedChapterId": "ch_1", "evidence": "铜钥匙"}},
		"constraints":      []any{map[string]any{"id": "debt_due", "kind": "deadline", "subject": "十枚银币债务", "text": "十枚银币债务", "dueDate": "2026-12-01", "sourceChapterId": "ch_1", "evidence": "十枚银币"}},
		"timeline":         []any{map[string]any{"id": "time_bridge", "date": "2026-11-23", "sourceChapterId": "ch_1", "evidence": "旧桥"}},
	}
	one := writeCrossWorkflowChapter(t, &fixture, "ch_1", 1, "第一章 旧桥纸盒", chapterOne, cardOne,
		map[string]string{"task:task_box": "打开纸盒", "outline:outline_cross_1": "旧桥", "outline:candidate_twist": "薄舟"}, true)
	retry, err := fixture.w.finalizeWorkflow(id, one.finalPath, one.baseRevision)
	if err != nil || retry.(map[string]any)["idempotent"] != true || fixture.revision != 7 {
		t.Fatalf("同一最终操作重试未幂等：%v %v rev=%d", retry, err, fixture.revision)
	}

	stale := testRecordStage(t, w, id, fixture.operationID(), "task-add", map[string]any{
		"id": "task_stale", "title": "过期写入", "plannedChapter": float64(3), "priority": "normal", "status": "todo",
	})
	if _, err := w.mutateFromFile(id, stale, fixture.revision-1); err == nil || !strings.Contains(err.Error(), "版本冲突") {
		t.Fatalf("旧 revision 没有拒绝：%v", err)
	}

	if _, _, err := fixture.mutate(t, "task-update", map[string]any{"id": "task_box", "status": "done",
		"completedChapterId": "ch_1", "completionEvidence": "打开纸盒"}, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.mutate(t, "character-update", map[string]any{"id": "char_lan",
		"cognition":     []any{map[string]any{"factId": "fact_debt", "beliefType": "fact", "learnedChapterId": "ch_1", "evidence": "账本记载十枚银币"}},
		"relationships": []any{map[string]any{"targetId": "char_bo", "kind": "ally", "sinceChapterId": "ch_1", "evidence": "把铜钥匙交给薄舟"}},
	}, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.mutate(t, "entity-add", map[string]any{"id": "item_key", "kind": "item", "name": "铜钥匙", "status": "canon",
		"sourceChapterId": "ch_1", "evidence": "铜钥匙",
		"ownershipHistory": []any{map[string]any{"ownerId": "char_bo", "fromChapterId": "ch_1", "evidence": "把铜钥匙交给薄舟"}},
		"transfers":        []any{map[string]any{"subjectId": "item_key", "fromChapterId": "ch_1", "destination": "旧桥", "evidence": "旧桥"}},
		"conditionHistory": []any{map[string]any{"status": "intact", "chapterId": "ch_1", "evidence": "铜钥匙"}},
	}, false); err != nil {
		t.Fatal(err)
	}
	beforeInvalid := fixture.revision
	if _, _, err := fixture.mutate(t, "entity-add", map[string]any{"id": "item_fake", "kind": "item", "name": "伪证据实体", "status": "canon",
		"sourceChapterId": "ch_1", "evidence": "正文不存在的证据"}, false); err == nil || fixture.revision != beforeInvalid {
		t.Fatalf("伪证据实体未原子拒绝：%v rev=%d", err, fixture.revision)
	}
	if _, _, err := fixture.mutate(t, "entity-add", map[string]any{"id": "state_debt_plan", "kind": "state", "name": "未确认债务", "status": "plan",
		"subjectId": "item_key", "stateType": "debt", "value": "未知"}, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.mutate(t, "state-transition", map[string]any{"id": "state_key_bridge", "name": "钥匙位置",
		"subjectId": "item_key", "stateType": "location", "value": "旧桥", "sourceChapterId": "ch_1", "evidence": "旧桥",
		"allowInitial": true, "authorDecision": "作者确认钥匙初始位于旧桥"}, true); err != nil {
		t.Fatal(err)
	}

	promotionOp := fixture.operationID()
	promotionFixture := blueprintPromotionFixture{w: w, id: id, revision: fixture.revision}
	promotionItems := []any{map[string]any{"field": "chapterOutline", "id": "candidate_twist"}}
	withoutConfirm := stageBlueprintPromotion(t, promotionFixture, promotionOp, "作者确认候选背叛设定成为正史", promotionItems, false)
	if _, err := w.mutateFromFile(id, withoutConfirm, fixture.revision); err == nil || !strings.Contains(err.Error(), "明确确认") {
		t.Fatalf("蓝图候选未经授权被晋升：%v", err)
	}
	confirmed := stageBlueprintPromotion(t, promotionFixture, promotionOp, "作者确认候选背叛设定成为正史", promotionItems, true)
	if _, err := w.mutateFromFile(id, confirmed, fixture.revision); err != nil {
		t.Fatal(err)
	}
	fixture.revision++

	chapterTwo := "薄舟在钟楼确认铜钥匙完好，仍记得十枚银币债务，但没有归还钥匙。"
	cardTwo := map[string]any{"chapterId": "ch_2",
		"facts":            []any{map[string]any{"id": "fact_key", "subject": "铜钥匙仍完好", "certainty": "certain", "sourceChapterId": "ch_2", "evidence": "铜钥匙完好"}},
		"knowledgeChanges": []any{map[string]any{"id": "know_key", "characterId": "char_bo", "factId": "fact_key", "beliefType": "fact", "learnedChapterId": "ch_2", "evidence": "确认铜钥匙完好"}},
		"hookChanges":      []any{map[string]any{"id": "hook_key", "text": "铜钥匙将打开何处", "status": "open", "openedChapterId": "ch_2", "evidence": "铜钥匙完好"}},
		"timeline":         []any{map[string]any{"id": "time_tower", "date": "2026-11-24", "sourceChapterId": "ch_2", "evidence": "钟楼"}},
	}
	two := writeCrossWorkflowChapter(t, &fixture, "ch_2", 2, "第二章 钟楼确认", chapterTwo, cardTwo,
		map[string]string{"task:task_key": "确认铜钥匙完好", "outline:outline_cross_2": "钟楼", "hook": "铜钥匙", "constraint": "十枚银币"}, false)
	if two.prepareID == "" || two.reviewID == "" {
		t.Fatal("第二章写作闭环缺少持久化 ID")
	}
	if _, _, err := fixture.mutate(t, "task-update", map[string]any{"id": "task_key", "status": "done",
		"completedChapterId": "ch_2", "completionEvidence": "确认铜钥匙完好"}, false); err != nil {
		t.Fatal(err)
	}

	for number := 3; number <= 55; number++ {
		input := testStage(t, w, id, fixture.operationID(), fmt.Sprintf("ch_%d", number), fmt.Sprintf("第%d章虚构过渡正文。", number))
		if _, err := w.appendFromFile(id, input, fixture.revision); err != nil {
			t.Fatalf("第%d章写入失败：%v", number, err)
		}
		fixture.revision++
	}
	_, _, manifestBeforeOrder, err := w.load(id)
	if err != nil || len(manifestBeforeOrder.MemoryArchives) == 0 {
		t.Fatalf("约 50 章归档没有形成：%v archives=%v", err, manifestBeforeOrder.MemoryArchives)
	}
	if _, err := w.queryFiltered(id, "memory", "铜钥匙由薄舟保管", "", 0, 20, 0, 1, 1, "", "", ""); err != nil {
		t.Fatalf("归档旧事实无法按原章回查：%v", err)
	}

	order := []any{"ch_2", "ch_1"}
	for number := 3; number <= 55; number++ {
		order = append(order, fmt.Sprintf("ch_%d", number))
	}
	orderPath := stageChapterOrder(t, w, id, fixture.operationID(), order, true)
	if _, err := w.mutateFromFile(id, orderPath, fixture.revision); err != nil {
		t.Fatal(err)
	}
	fixture.revision++
	if got := plannedChapters(persistedTasks(t, w, id)); !reflect.DeepEqual(got, map[string]int{"task_box": 2, "task_key": 1}) {
		t.Fatalf("跨模块调序后任务映射错误：%v", got)
	}

	archiveRecord := map[string]any{"throughChapter": float64(2)}
	archiveOp := fixture.operationID()
	withoutArchiveConfirm := testRecordStage(t, w, id, archiveOp, "task-archive-done", archiveRecord)
	if _, err := w.mutateFromFile(id, withoutArchiveConfirm, fixture.revision); err == nil || !strings.Contains(err.Error(), "明确确认") {
		t.Fatalf("已完成任务未经确认被归档：%v", err)
	}
	archivePath := testRecordStage(t, w, id, archiveOp, "task-archive-done", archiveRecord)
	raw, _ := os.ReadFile(archivePath)
	var archiveCandidate recordDraft
	if err := json.Unmarshal(raw, &archiveCandidate); err != nil {
		t.Fatal(err)
	}
	archiveCandidate.Confirm = true
	raw, _ = json.Marshal(archiveCandidate)
	if err := os.WriteFile(archivePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.mutateFromFile(id, archivePath, fixture.revision); err != nil {
		t.Fatal(err)
	}
	fixture.revision++

	states := queryStateItems(t, statesFixture{w: w, id: id, revision: fixture.revision}, "", "", 0, 0)
	lan := findState(t, states, "character_state", "char_lan")
	encodedLan, _ := json.Marshal(lan)
	if !strings.Contains(string(encodedLan), "fact_debt") {
		t.Fatal("阿澜的已知事实没有从人物证据派生")
	}
	bo := findState(t, states, "character_state", "char_bo")
	encodedBo, _ := json.Marshal(bo)
	if strings.Contains(string(encodedBo), "fact_debt") {
		t.Fatal("薄舟被错误赋予了阿澜才知道的债务事实")
	}
	subject := findState(t, states, "subject_state", "item_key")
	if subject["location"].(map[string]any)["value"] != "旧桥" {
		t.Fatalf("正史位置状态错误：%v", subject)
	}
	if _, exists := subject["debt"]; exists {
		t.Fatal("plan 债务状态冒充了已确认正史")
	}
	dateResult, err := w.queryFiltered(id, "memory", "", "", 0, 20, 0, 0, 0, "", "2026-11-23", "2026-11-24")
	if err != nil || dateResult.(map[string]any)["total"].(int) != 2 {
		t.Fatalf("跨模块时间线日期检索错误：%v %v", dateResult, err)
	}
	archivedTasks, err := w.query(id, "tasks", "", "", 0, 20, 0)
	if err != nil || archivedTasks.(map[string]any)["total"].(int) != 2 {
		t.Fatalf("完成任务归档后查询丢失：%v %v", archivedTasks, err)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.metrics(id); err != nil {
		t.Fatal(err)
	}

	// A second work in the same workspace must remain completely isolated.
	if _, err := w.mutateFromFile(other, archivePath, 1); err == nil || !strings.Contains(err.Error(), "隔离 inbox") {
		t.Fatalf("跨作品候选没有拒绝：%v", err)
	}
	otherStatus, err := w.status(other)
	if err != nil || otherStatus.(map[string]any)["chapters"] != 0 || otherStatus.(map[string]any)["pendingTasks"] != 0 {
		t.Fatalf("第二作品被串写：%v %v", otherStatus, err)
	}

	workRoot, _ := w.workRoot(id)
	wanted := snapshotWorkFiles(t, workRoot)
	exported, err := w.exportWork(id)
	if err != nil {
		t.Fatal(err)
	}
	exportBytes, err := os.ReadFile(exported.(map[string]any)["path"].(string))
	if err != nil {
		t.Fatal(err)
	}
	restored := testWorkspace(t)
	importPath := copyExportIntoWorkspace(t, restored, exportBytes, "cross-module.zip")
	if err := os.RemoveAll(workRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctor(other); err != nil {
		t.Fatalf("删除虚构源作品后影响了另一作品：%v", err)
	}
	if _, err := restored.importWork(importPath); err != nil {
		t.Fatalf("跨模块作品在全新工作区恢复失败：%v", err)
	}
	assertRestoredWork(t, restored, id, fixture.revision, wanted)

	restoredFixture := crossModuleFixture{w: restored, id: id, revision: fixture.revision, sequence: 1000}
	continued := writeCrossWorkflowChapter(t, &restoredFixture, "ch_56", 56, "第五十六章 恢复后续写",
		"薄舟仍保管铜钥匙，十枚银币债务尚待处理。", nil,
		map[string]string{"hook": "铜钥匙", "constraint": "十枚银币"}, false)
	if continued.prepareID == "" || restoredFixture.revision != fixture.revision+1 {
		t.Fatal("恢复后不能继续完成写作闭环")
	}
	if _, err := restored.doctor(id); err != nil {
		t.Fatalf("恢复后续写导致 doctor 失败：%v", err)
	}
}

func newCrossCrashFixture(t *testing.T) (crossModuleFixture, crossWorkflowResult) {
	t.Helper()
	w := testWorkspace(t)
	id := testInit(t, w)
	fixture := crossModuleFixture{w: w, id: id, revision: 1, sequence: 2000}
	if _, _, err := fixture.mutate(t, "task-add", map[string]any{
		"id": "task_crash_workflow", "title": "必须打开纸盒", "plannedChapter": float64(1), "priority": "high", "status": "todo",
	}, false); err != nil {
		t.Fatal(err)
	}
	// Build a valid prepare/draft/review chain without committing the final.
	prepared, err := w.prepareChapter(id, "ch_1", 1, fixture.revision)
	if err != nil {
		t.Fatal(err)
	}
	prepareID := prepared.(map[string]any)["prepareId"].(string)
	draftID := fixture.operationID()
	draftPath := filepath.Join(w.inbox, id, draftID+".json")
	draftData, _ := json.Marshal(draftCandidate{OperationID: draftID, PrepareID: prepareID,
		Chapter: chapter{ID: "ch_1", Title: "中断测试", Content: "阿澜打开纸盒。"}})
	if err := os.WriteFile(draftPath, draftData, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.draftWorkflow(id, draftPath, fixture.revision); err != nil {
		t.Fatal(err)
	}
	reviewID := fixture.operationID()
	reviewPath := filepath.Join(w.inbox, id, reviewID+".json")
	reviewData, _ := json.Marshal(reviewCandidate{OperationID: reviewID, PrepareID: prepareID, DraftID: draftID,
		Requirements: []reviewItem{{ID: "task:task_crash_workflow", Status: "satisfied", Quote: "打开纸盒"}}})
	if err := os.WriteFile(reviewPath, reviewData, 0600); err != nil {
		t.Fatal(err)
	}
	if result, err := w.reviewWorkflow(id, reviewPath, fixture.revision); err != nil || result.(map[string]any)["verdict"] != "pass" {
		t.Fatalf("故障测试审稿准备失败：%v %v", result, err)
	}
	finalInputID := fixture.operationID()
	finalPath := filepath.Join(w.inbox, id, finalInputID+".json")
	finalData, _ := json.Marshal(finalCandidate{OperationID: finalInputID, PrepareID: prepareID,
		DraftID: draftID, ReviewID: reviewID, Chapter: chapter{ID: "ch_1", Title: "中断测试"}})
	if err := os.WriteFile(finalPath, finalData, 0600); err != nil {
		t.Fatal(err)
	}
	return fixture, crossWorkflowResult{prepareID: prepareID, draftID: draftID, reviewID: reviewID,
		finalPath: finalPath, baseRevision: fixture.revision, finalInputID: finalInputID}
}

func TestCrossModuleWorkflowCrashRecoveryAtHeadBoundaries(t *testing.T) {
	for _, point := range []string{"append:before-head-switch", "append:head-switched"} {
		t.Run(strings.ReplaceAll(point, ":", "-"), func(t *testing.T) {
			fixture, final := newCrossCrashFixture(t)
			originalFault := transactionFault
			transactionFault = func(actual string) error {
				if actual == point {
					return errors.New("cross-module injected interruption")
				}
				return nil
			}
			_, failure := fixture.w.finalizeWorkflow(fixture.id, final.finalPath, final.baseRevision)
			transactionFault = originalFault
			if failure == nil || !strings.Contains(failure.Error(), "injected interruption") {
				t.Fatalf("写作闭环故障点没有中断：%v", failure)
			}
			reopened, err := openWorkspace(fixture.w.root)
			if err != nil {
				t.Fatal(err)
			}
			finalOperation := sumBytes([]byte("finalize:" + final.finalInputID))[:32]
			if point == "append:before-head-switch" {
				assertCurrentHead(t, reopened, fixture.id, final.baseRevision, "")
				if _, err := reopened.doctor(fixture.id); err == nil || !strings.Contains(err.Error(), "未完成事务") {
					t.Fatalf("HEAD 前中断没有停写：%v", err)
				}
				if _, err := reopened.recoverPending(fixture.id, finalOperation, true); err != nil {
					t.Fatal(err)
				}
				// Aborted immutable candidates are retained. A new explicit final
				// request creates a different deterministic operation and may commit.
				newFinalID := fmt.Sprintf("%032x", 0xfeed)
				newFinalPath := filepath.Join(reopened.inbox, fixture.id, newFinalID+".json")
				newData, _ := json.Marshal(finalCandidate{OperationID: newFinalID, PrepareID: final.prepareID,
					DraftID: final.draftID, ReviewID: final.reviewID, Chapter: chapter{ID: "ch_1", Title: "中断测试"}})
				if err := os.WriteFile(newFinalPath, newData, 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := reopened.finalizeWorkflow(fixture.id, newFinalPath, final.baseRevision); err != nil {
					t.Fatalf("确认放弃中断事务后不能重新定稿：%v", err)
				}
			} else {
				assertCurrentHead(t, reopened, fixture.id, final.baseRevision+1, "")
				retry, err := reopened.finalizeWorkflow(fixture.id, final.finalPath, final.baseRevision)
				if err != nil || retry.(map[string]any)["idempotent"] != true {
					t.Fatalf("HEAD 后中断同操作没有幂等补记录/备份：%v %v", retry, err)
				}
			}
			if _, err := reopened.doctor(fixture.id); err != nil {
				t.Fatalf("写作闭环故障恢复后 doctor 失败：%v", err)
			}
			chapterPage, err := reopened.readChapter(fixture.id, "ch_1", 0, 100)
			if err != nil || chapterPage.(map[string]any)["text"] != "阿澜打开纸盒。" {
				t.Fatalf("故障恢复后正文不一致：%v %v", chapterPage, err)
			}
		})
	}
}
