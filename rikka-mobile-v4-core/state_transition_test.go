package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

type stateTransitionFixture struct {
	w        *workspace
	id       string
	revision int
}

func newStateTransitionFixture(t *testing.T) stateTransitionFixture {
	t.Helper()
	w := testWorkspace(t)
	id := testInit(t, w)
	revision := 1
	chapters := []string{
		"阿澜把铜钥匙留在旧桥。",
		"薄舟把铜钥匙带到钟楼，欠款仍是一千元。",
		"薄舟清偿两百元，欠款变为八百元。",
	}
	for index, content := range chapters {
		input := testStage(t, w, id, fmt.Sprintf("%032x", 0xc100+index), fmt.Sprintf("ch_%d", index+1), content)
		if _, err := w.appendFromFile(id, input, revision); err != nil {
			t.Fatal(err)
		}
		revision++
	}
	item := testRecordStage(t, w, id, strings.Repeat("1", 32), "entity-add", map[string]any{
		"id": "item_key", "kind": "item", "name": "铜钥匙", "status": "canon",
		"sourceChapterId": "ch_1", "evidence": "铜钥匙留在旧桥",
	})
	if _, err := w.mutateFromFile(id, item, revision); err != nil {
		t.Fatal(err)
	}
	return stateTransitionFixture{w: w, id: id, revision: revision + 1}
}

func stageStateTransition(t *testing.T, fixture stateTransitionFixture, operationID string,
	record map[string]any, confirm bool) string {
	t.Helper()
	path := testRecordStage(t, fixture.w, fixture.id, operationID, "state-transition", record)
	if !confirm {
		return path
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var draft recordDraft
	if err := json.Unmarshal(data, &draft); err != nil {
		t.Fatal(err)
	}
	draft.Confirm = true
	data, err = json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func initialLocationTransition() map[string]any {
	return map[string]any{
		"id": "state_key_bridge", "name": "钥匙位置：旧桥", "subjectId": "item_key",
		"stateType": "location", "value": "旧桥", "sourceChapterId": "ch_1",
		"evidence": "铜钥匙留在旧桥", "allowInitial": true,
		"authorDecision": "作者确认钥匙初始位于旧桥",
	}
}

func nextLocationTransition() map[string]any {
	return map[string]any{
		"id": "state_key_tower", "name": "钥匙位置：钟楼", "subjectId": "item_key",
		"stateType": "location", "value": "钟楼", "sourceChapterId": "ch_2",
		"evidence": "铜钥匙带到钟楼", "expectedPreviousStateId": "state_key_bridge",
		"expectedPreviousValue": "旧桥", "authorDecision": "作者确认钥匙已转移到钟楼",
	}
}

func commitInitialLocation(t *testing.T, fixture *stateTransitionFixture) string {
	t.Helper()
	op := strings.Repeat("2", 32)
	input := stageStateTransition(t, *fixture, op, initialLocationTransition(), true)
	if _, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision); err != nil {
		t.Fatal(err)
	}
	fixture.revision++
	return input
}

func subjectState(t *testing.T, fixture stateTransitionFixture) map[string]any {
	t.Helper()
	result, err := fixture.w.queryFiltered(fixture.id, "states", "", "item_key", 0, 20, 0, 0, 0, "subject_state", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range result.(map[string]any)["items"].([]any) {
		record := raw.(map[string]any)
		if record["type"] == "subject_state" {
			return record["value"].(map[string]any)
		}
	}
	t.Fatal("未查询到 subject_state")
	return nil
}

func TestStateTransitionEnforcesCausalPreconditionAndPersists(t *testing.T) {
	fixture := newStateTransitionFixture(t)
	commitInitialLocation(t, &fixture)
	op := strings.Repeat("3", 32)
	input := stageStateTransition(t, fixture, op, nextLocationTransition(), true)
	result, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision)
	if err != nil {
		t.Fatal(err)
	}
	committedRevision := fixture.revision + 1
	if intValue(result.(map[string]any)["revision"]) != committedRevision || result.(map[string]any)["recordId"] != "state_key_tower" {
		t.Fatalf("状态转换结果错误：%v", result)
	}
	fixture.revision = committedRevision

	current := subjectState(t, fixture)["location"].(map[string]any)
	if current["id"] != "state_key_tower" || current["value"] != "钟楼" || current["evidence"] != "铜钥匙带到钟楼" {
		t.Fatalf("状态查询未返回最新有证据状态：%v", current)
	}
	root, h, m, err := fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	if h.Revision != committedRevision || !fixture.w.backupExists(fixture.id, committedRevision) {
		t.Fatalf("revision 或独立备份错误：%v", h)
	}
	var operation opRecord
	if err := loadJSON(root, "operations/"+op+".json", &operation); err != nil {
		t.Fatal(err)
	}
	if operation.Kind != "state-transition" || operation.RecordID != "state_key_tower" ||
		operation.AuthorDecision != "作者确认钥匙已转移到钟楼" {
		t.Fatalf("操作审计缺失：%+v", operation)
	}
	entities, err := currentRecords(root, m.EntitiesPath)
	if err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, entity := range entities {
		if entity["id"] == "state_key_tower" {
			matched = entity["kind"] == "state" && entity["status"] == "canon" &&
				entity["expectedPreviousStateId"] == "state_key_bridge"
		}
	}
	if !matched {
		t.Fatal("正史状态实体或前置审计未持久化")
	}

	retry, err := fixture.w.mutateFromFile(fixture.id, input, committedRevision-1)
	if err != nil || retry.(map[string]any)["idempotent"] != true {
		t.Fatalf("相同 operationId 幂等重试失败：%v %v", retry, err)
	}
	reopened, err := openWorkspace(fixture.w.root)
	if err != nil {
		t.Fatal(err)
	}
	fixture.w = reopened
	if _, err := reopened.doctor(fixture.id); err != nil {
		t.Fatalf("重启回读 doctor 失败：%v", err)
	}
	if subjectState(t, fixture)["location"].(map[string]any)["value"] != "钟楼" {
		t.Fatal("重启回读丢失最新状态")
	}

	changed := nextLocationTransition()
	changed["value"] = "塔顶"
	changedInput := stageStateTransition(t, fixture, op, changed, true)
	if _, err := reopened.mutateFromFile(fixture.id, changedInput, committedRevision); err == nil || !strings.Contains(err.Error(), "不同候选") {
		t.Fatalf("不同请求冒用 operationId 未被拒绝：%v", err)
	}
}

func TestStateTransitionRejectsInvalidCausalityAtomically(t *testing.T) {
	fixture := newStateTransitionFixture(t)
	commitInitialLocation(t, &fixture)
	root, beforeHead, beforeManifest, err := fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	beforeEntities, err := fileSHA(root, beforeManifest.EntitiesPath)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		confirm bool
		want    string
		record  map[string]any
	}{
		{"no-confirm", false, "明确确认", nextLocationTransition()},
		{"no-author-decision", true, "作者决定", func() map[string]any { v := nextLocationTransition(); delete(v, "authorDecision"); return v }()},
		{"unknown-subject", true, "引用缺失", func() map[string]any {
			v := nextLocationTransition()
			v["id"] = "state_unknown"
			v["subjectId"] = "item_missing"
			return v
		}()},
		{"bad-evidence", true, "逐字证据", func() map[string]any {
			v := nextLocationTransition()
			v["id"] = "state_bad_evidence"
			v["evidence"] = "正文中不存在"
			return v
		}()},
		{"wrong-previous-id", true, "前置状态冲突", func() map[string]any {
			v := nextLocationTransition()
			v["id"] = "state_wrong_id"
			v["expectedPreviousStateId"] = "state_other"
			return v
		}()},
		{"wrong-previous-value", true, "前置状态值冲突", func() map[string]any {
			v := nextLocationTransition()
			v["id"] = "state_wrong_value"
			v["expectedPreviousValue"] = "河岸"
			return v
		}()},
		{"same-value", true, "没有发生变化", func() map[string]any {
			v := nextLocationTransition()
			v["id"] = "state_same"
			v["value"] = "旧桥"
			return v
		}()},
		{"invalid-state-type", true, "stateType 无效", func() map[string]any {
			v := nextLocationTransition()
			v["id"] = "state_invalid_type"
			v["stateType"] = "mood"
			v["allowInitial"] = true
			delete(v, "expectedPreviousStateId")
			delete(v, "expectedPreviousValue")
			return v
		}()},
		{"existing-id", true, "ID 已存在", func() map[string]any { v := nextLocationTransition(); v["id"] = "state_key_bridge"; return v }()},
		{"missing-initial-authority", true, "首次状态", map[string]any{
			"id": "state_debt", "name": "债务", "subjectId": "item_key", "stateType": "debt", "value": "一千元",
			"sourceChapterId": "ch_2", "evidence": "欠款仍是一千元", "authorDecision": "作者确认债务",
		}},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			op := fmt.Sprintf("%032x", 0xd100+index)
			input := stageStateTransition(t, fixture, op, tc.record, tc.confirm)
			if _, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误未按契约拒绝，want %q: %v", tc.want, err)
			}
			_, afterHead, afterManifest, err := fixture.w.load(fixture.id)
			if err != nil {
				t.Fatal(err)
			}
			afterEntities, err := fileSHA(root, afterManifest.EntitiesPath)
			if err != nil {
				t.Fatal(err)
			}
			if afterHead.Revision != beforeHead.Revision || afterEntities != beforeEntities {
				t.Fatalf("失败请求改变了 HEAD 或实体索引：%v %s", afterHead, afterEntities)
			}
		})
	}

	for _, bypass := range []struct {
		name   string
		kind   string
		record map[string]any
		want   string
	}{
		{"direct-canon-add", "entity-add", map[string]any{
			"id": "state_bypass", "kind": "state", "name": "绕过状态", "status": "canon",
			"subjectId": "item_key", "stateType": "location", "value": "塔顶",
			"sourceChapterId": "ch_3", "evidence": "欠款变为八百元",
		}, "state-transition"},
		{"in-place-state-update", "entity-update", map[string]any{
			"id": "state_key_bridge", "value": "塔顶",
		}, "不可原地修改"},
	} {
		t.Run(bypass.name, func(t *testing.T) {
			op := strings.Repeat(map[string]string{"direct-canon-add": "a", "in-place-state-update": "b"}[bypass.name], 32)
			input := testRecordStage(t, fixture.w, fixture.id, op, bypass.kind, bypass.record)
			if _, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision); err == nil || !strings.Contains(err.Error(), bypass.want) {
				t.Fatalf("通用实体写入绕过状态门禁：%v", err)
			}
			_, afterHead, afterManifest, err := fixture.w.load(fixture.id)
			if err != nil {
				t.Fatal(err)
			}
			afterEntities, err := fileSHA(root, afterManifest.EntitiesPath)
			if err != nil {
				t.Fatal(err)
			}
			if afterHead.Revision != beforeHead.Revision || afterEntities != beforeEntities {
				t.Fatal("绕过请求失败后改变了作品状态")
			}
		})
	}

	// A chronological regression needs an existing state later than the
	// proposed evidence chapter. Commit one valid transition first, then prove
	// the older evidence cannot become the new current state.
	valid := stageStateTransition(t, fixture, strings.Repeat("4", 32), nextLocationTransition(), true)
	if _, err := fixture.w.mutateFromFile(fixture.id, valid, fixture.revision); err != nil {
		t.Fatal(err)
	}
	fixture.revision++
	root, beforeBackwardHead, beforeBackwardManifest, err := fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	beforeBackwardEntities, err := fileSHA(root, beforeBackwardManifest.EntitiesPath)
	if err != nil {
		t.Fatal(err)
	}
	backward := nextLocationTransition()
	backward["id"] = "state_backward"
	backward["sourceChapterId"] = "ch_1"
	backward["evidence"] = "铜钥匙留在旧桥"
	backward["value"] = "桥下"
	backward["expectedPreviousStateId"] = "state_key_tower"
	backward["expectedPreviousValue"] = "钟楼"
	input := stageStateTransition(t, fixture, strings.Repeat("5", 32), backward, true)
	if _, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision); err == nil || !strings.Contains(err.Error(), "早于当前状态") {
		t.Fatalf("较旧证据错误成为当前状态：%v", err)
	}
	_, afterBackwardHead, afterBackwardManifest, err := fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	afterBackwardEntities, err := fileSHA(root, afterBackwardManifest.EntitiesPath)
	if err != nil {
		t.Fatal(err)
	}
	if afterBackwardHead.Revision != beforeBackwardHead.Revision || afterBackwardEntities != beforeBackwardEntities {
		t.Fatal("时间倒退拒绝后改变了 HEAD 或实体索引")
	}
}

func TestStateTransitionIntegrityAndCrashRecovery(t *testing.T) {
	t.Run("tampered-index", func(t *testing.T) {
		fixture := newStateTransitionFixture(t)
		commitInitialLocation(t, &fixture)
		root, _, m, _ := fixture.w.load(fixture.id)
		path := mustSafePath(t, root, m.EntitiesPath)
		if err := os.WriteFile(path, []byte("[]"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.w.query(fixture.id, "states", "", "", 0, 20, 0); err == nil || !strings.Contains(err.Error(), "哈希") {
			t.Fatalf("states 未拒绝篡改索引：%v", err)
		}
		if _, err := fixture.w.doctor(fixture.id); err == nil {
			t.Fatal("doctor 未拒绝篡改索引")
		}
	})

	for _, point := range []string{"mutation:before-head-switch", "mutation:head-switched"} {
		t.Run(strings.ReplaceAll(point, ":", "-"), func(t *testing.T) {
			fixture := newStateTransitionFixture(t)
			commitInitialLocation(t, &fixture)
			op := strings.Repeat(map[string]string{"mutation:before-head-switch": "7", "mutation:head-switched": "8"}[point], 32)
			input := stageStateTransition(t, fixture, op, nextLocationTransition(), true)
			oldRevision := fixture.revision
			withTransactionFault(t, point)
			if _, err := fixture.w.mutateFromFile(fixture.id, input, oldRevision); err == nil || !strings.Contains(err.Error(), "injected") {
				t.Fatalf("故障注入未中断提交：%v", err)
			}
			transactionFault = func(string) error { return nil }
			reopened, err := openWorkspace(fixture.w.root)
			if err != nil {
				t.Fatal(err)
			}
			fixture.w = reopened
			if point == "mutation:before-head-switch" {
				_, h, _, _ := reopened.load(fixture.id)
				if h.Revision != oldRevision {
					t.Fatalf("HEAD 前中断却改变 revision：%d", h.Revision)
				}
				if _, err := reopened.recoverPending(fixture.id, op, true); err != nil {
					t.Fatal(err)
				}
				if subjectState(t, fixture)["location"].(map[string]any)["value"] != "旧桥" {
					t.Fatal("HEAD 前中断暴露了未提交状态")
				}
			} else {
				result, err := reopened.mutateFromFile(fixture.id, input, oldRevision)
				if err != nil || result.(map[string]any)["idempotent"] != true {
					t.Fatalf("HEAD 后中断未能幂等补写：%v %v", result, err)
				}
				fixture.revision = oldRevision + 1
				if subjectState(t, fixture)["location"].(map[string]any)["value"] != "钟楼" {
					t.Fatal("HEAD 后恢复丢失已提交状态")
				}
			}
			if _, err := reopened.doctor(fixture.id); err != nil {
				t.Fatalf("故障恢复后 doctor 失败：%v", err)
			}
		})
	}
}
