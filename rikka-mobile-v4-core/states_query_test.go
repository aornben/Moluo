package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

type statesFixture struct {
	w        *workspace
	id       string
	revision int
}

func newStatesFixture(t *testing.T) statesFixture {
	t.Helper()
	w := testWorkspace(t)
	id := testInit(t, w)
	revision := 1
	chapters := []string{
		"阿澜负伤，并把铜钥匙交给薄舟。",
		"众人误传城门已经关闭。铜钥匙从旧桥转移到钟楼。阿澜倒地，被确认死亡。",
		"阿澜的伤势已经痊愈。铜钥匙被火烧坏。",
		"阿澜与薄舟反目。薄舟接过铜钥匙。",
		"阿澜苏醒，确认城门仍然开放。铜钥匙修复后转移到塔顶。",
	}
	for index, content := range chapters {
		op := fmt.Sprintf("%032x", index+1)
		path := testStage(t, w, id, op, fmt.Sprintf("ch_%d", index+1), content)
		if _, err := w.appendFromFile(id, path, revision); err != nil {
			t.Fatal(err)
		}
		revision++
	}
	mutate := func(operation, kind string, record map[string]any) {
		t.Helper()
		path := testRecordStage(t, w, id, operation, kind, record)
		if _, err := w.mutateFromFile(id, path, revision); err != nil {
			t.Fatal(err)
		}
		revision++
	}
	mutate(strings.Repeat("a", 32), "character-add", map[string]any{
		"id": "char_bo", "name": "薄舟",
		"death": map[string]any{"status": "dead", "sourceChapterId": "ch_2", "evidence": "被确认死亡"},
	})
	mutate(strings.Repeat("b", 32), "character-add", map[string]any{
		"id": "char_lan", "name": "阿澜",
		"death": []any{
			map[string]any{"status": "dead", "sourceChapterId": "ch_2", "evidence": "阿澜倒地，被确认死亡"},
			map[string]any{"status": "alive", "sourceChapterId": "ch_5", "evidence": "阿澜苏醒"},
		},
		"injuries": []any{
			map[string]any{"status": "injured", "chapterId": "ch_1", "evidence": "阿澜负伤"},
			map[string]any{"status": "healed", "chapterId": "ch_3", "evidence": "伤势已经痊愈"},
		},
		"relationships": []any{
			map[string]any{"targetId": "char_bo", "kind": "ally", "sinceChapterId": "ch_1", "evidence": "把铜钥匙交给薄舟"},
			map[string]any{"targetId": "char_bo", "kind": "enemy", "sinceChapterId": "ch_4", "evidence": "阿澜与薄舟反目"},
		},
		"cognition": []any{
			map[string]any{"factId": "fact_gate", "beliefType": "rumor", "learnedChapterId": "ch_2", "evidence": "误传城门已经关闭"},
			map[string]any{"factId": "fact_gate", "beliefType": "fact", "learnedChapterId": "ch_5", "evidence": "确认城门仍然开放"},
		},
		"knowledge": []any{
			map[string]any{"factId": "fact_key", "beliefType": "fact", "learnedChapterId": "ch_1", "evidence": "铜钥匙"},
		},
		"beliefs": []any{
			map[string]any{"factId": "fact_fire", "beliefType": "uncertain", "learnedChapterId": "ch_3", "evidence": "被火烧坏"},
		},
	})
	mutate(strings.Repeat("c", 32), "entity-add", map[string]any{
		"id": "item_key", "kind": "item", "name": "铜钥匙", "status": "canon",
		"sourceChapterId": "ch_1", "evidence": "铜钥匙",
		"relations": []any{
			map[string]any{"targetId": "char_lan", "kind": "carried_by", "fromChapterId": "ch_1", "evidence": "阿澜负伤"},
			map[string]any{"targetId": "char_lan", "kind": "rejected_by", "fromChapterId": "ch_3", "evidence": "铜钥匙被火烧坏"},
		},
		"ownershipHistory": []any{
			map[string]any{"ownerId": "char_lan", "fromChapterId": "ch_1", "evidence": "把铜钥匙交给薄舟"},
			map[string]any{"ownerId": "char_bo", "fromChapterId": "ch_4", "evidence": "薄舟接过铜钥匙"},
		},
		"transfers": []any{
			map[string]any{"subjectId": "item_key", "fromChapterId": "ch_2", "destination": "钟楼", "evidence": "转移到钟楼"},
			map[string]any{"subjectId": "item_key", "fromChapterId": "ch_5", "destination": "塔顶", "evidence": "转移到塔顶"},
		},
		"conditionHistory": []any{
			map[string]any{"status": "intact", "chapterId": "ch_1", "evidence": "铜钥匙"},
			map[string]any{"status": "damaged", "chapterId": "ch_3", "evidence": "铜钥匙被火烧坏"},
			map[string]any{"status": "repaired", "chapterId": "ch_5", "evidence": "铜钥匙修复"},
		},
	})
	bridge := stageStateTransition(t, stateTransitionFixture{w: w, id: id, revision: revision}, strings.Repeat("d", 32), map[string]any{
		"id": "state_key_bridge", "name": "钥匙位置（钟楼）", "subjectId": "item_key",
		"stateType": "location", "value": "钟楼", "sourceChapterId": "ch_2", "evidence": "转移到钟楼",
		"allowInitial": true, "authorDecision": "作者确认钥匙位于钟楼",
	}, true)
	if _, err := w.mutateFromFile(id, bridge, revision); err != nil {
		t.Fatal(err)
	}
	revision++
	tower := stageStateTransition(t, stateTransitionFixture{w: w, id: id, revision: revision}, strings.Repeat("e", 32), map[string]any{
		"id": "state_key_tower", "name": "钥匙位置（塔顶）", "subjectId": "item_key",
		"stateType": "location", "value": "塔顶", "sourceChapterId": "ch_5", "evidence": "转移到塔顶",
		"expectedPreviousStateId": "state_key_bridge", "expectedPreviousValue": "钟楼",
		"authorDecision": "作者确认钥匙转移到塔顶",
	}, true)
	if _, err := w.mutateFromFile(id, tower, revision); err != nil {
		t.Fatal(err)
	}
	revision++
	mutate(strings.Repeat("f", 32), "entity-add", map[string]any{
		"id": "state_key_debt_plan", "kind": "state", "name": "未确认债务", "status": "plan",
		"subjectId": "item_key", "stateType": "debt", "value": "未确认",
	})
	return statesFixture{w: w, id: id, revision: revision}
}

func queryStateItems(t *testing.T, fixture statesFixture, character, status string, fromChapter, toChapter int) []any {
	t.Helper()
	result, err := fixture.w.queryFiltered(fixture.id, "states", "", character, 0, 20, 0,
		fromChapter, toChapter, status, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return result.(map[string]any)["items"].([]any)
}

func findState(t *testing.T, items []any, kind, id string) map[string]any {
	t.Helper()
	for _, raw := range items {
		record := raw.(map[string]any)
		if record["type"] != kind {
			continue
		}
		value := record["value"].(map[string]any)
		if value["id"] == id || value["subjectId"] == id {
			return value
		}
	}
	t.Fatalf("未找到 %s/%s", kind, id)
	return nil
}

func TestStatesQueryDerivesCurrentStateAndFilters(t *testing.T) {
	fixture := newStatesFixture(t)
	items := queryStateItems(t, fixture, "", "", 0, 0)
	lan := findState(t, items, "character_state", "char_lan")
	death := lan["death"].(map[string]any)
	if death["status"] != "alive" || intValue(death["chapter"]) != 5 || death["evidence"] != "阿澜苏醒" {
		t.Fatalf("兼容死亡历史没有按最新有效证据派生：%v", death)
	}
	injury := lan["injuryLatest"].(map[string]any)
	if injury["status"] != "healed" || intValue(injury["chapter"]) != 3 {
		t.Fatalf("伤病当前状态错误：%v", injury)
	}
	relationships := lan["relationships"].([]map[string]any)
	if len(relationships) != 1 || relationships[0]["kind"] != "enemy" || intValue(relationships[0]["chapter"]) != 4 {
		t.Fatalf("人物关系没有按目标人物取最新状态：%v", relationships)
	}
	cognition := lan["cognition"].([]map[string]any)
	if len(cognition) != 3 {
		t.Fatalf("人物认知没有按事实 ID 取最新状态：%v", cognition)
	}
	byFact := map[string]map[string]any{}
	for _, fact := range cognition {
		byFact[fact["factId"].(string)] = fact
	}
	if byFact["fact_gate"]["beliefType"] != "fact" || intValue(byFact["fact_gate"]["chapter"]) != 5 ||
		byFact["fact_key"]["beliefType"] != "fact" || byFact["fact_fire"]["beliefType"] != "uncertain" {
		t.Fatalf("当前与兼容认知字段合并错误：%v", cognition)
	}
	encoded, _ := json.Marshal(lan)
	if strings.Contains(string(encoded), "reader_only") {
		t.Fatal("状态查询创造了人物没有记录的读者事实")
	}

	bo := findState(t, items, "character_state", "char_bo")
	if death := bo["death"].(map[string]any); death["status"] != "dead" || intValue(death["chapter"]) != 2 || death["evidence"] != "被确认死亡" {
		t.Fatalf("对象格式死亡状态兼容错误：%v", death)
	}
	key := findState(t, items, "entity_state", "item_key")
	if owner := key["ownerLatest"].(map[string]any); owner["ownerId"] != "char_bo" || intValue(owner["chapter"]) != 4 || owner["evidence"] != "薄舟接过铜钥匙" {
		t.Fatalf("所有权当前状态或证据错误：%v", owner)
	}
	if transfer := key["transferLatest"].(map[string]any); transfer["destination"] != "塔顶" || intValue(transfer["chapter"]) != 5 {
		t.Fatalf("fromChapterId 转移历史没有正确派生：%v", transfer)
	}
	if condition := key["conditionLatest"].(map[string]any); condition["status"] != "repaired" || intValue(condition["chapter"]) != 5 {
		t.Fatalf("chapterId 状态历史没有正确派生：%v", condition)
	}
	if relations := key["relations"].([]map[string]any); len(relations) != 1 || relations[0]["kind"] != "rejected_by" || intValue(relations[0]["chapter"]) != 3 {
		t.Fatalf("实体关系没有区分历史与当前状态：%v", relations)
	}
	subject := findState(t, items, "subject_state", "item_key")
	location := subject["location"].(map[string]any)
	if location["id"] != "state_key_tower" || location["value"] != "塔顶" || intValue(location["chapter"]) != 5 || location["evidence"] != "转移到塔顶" {
		t.Fatalf("主体最新状态错误或不可追溯：%v", location)
	}
	if _, exists := subject["debt"]; exists {
		t.Fatal("未确认 plan 状态被当成主体当前事实")
	}

	if got := len(queryStateItems(t, fixture, "", "character_state", 0, 0)); got != 2 {
		t.Fatalf("character_state 类型过滤错误：%d", got)
	}
	if got := len(queryStateItems(t, fixture, "", "location", 0, 0)); got != 3 {
		t.Fatalf("location 状态类型过滤错误：%d", got)
	}
	if got := len(queryStateItems(t, fixture, "char_lan", "", 0, 0)); got != 2 {
		t.Fatalf("人物 ID 过滤没有准确包含人物及当前关联实体：%d", got)
	}
	if got := len(queryStateItems(t, fixture, "item_key", "", 0, 0)); got != 5 {
		t.Fatalf("实体/主体 ID 过滤错误：%d", got)
	}
	if got := len(queryStateItems(t, fixture, "char_missing", "", 0, 0)); got != 0 {
		t.Fatalf("不存在的合法 ID 应返回空结果：%d", got)
	}
	if _, err := fixture.w.queryFiltered(fixture.id, "states", "", "bad id", 0, 20, 0, 0, 0, "", "", ""); err == nil || !strings.Contains(err.Error(), "人物 ID 无效") {
		t.Fatalf("非法 ID 没有明确拒绝：%v", err)
	}
	if got := len(queryStateItems(t, fixture, "", "", 3, 4)); got != 2 {
		t.Fatalf("章节证据边界过滤错误：%d", got)
	}

	reopened, err := openWorkspace(fixture.w.root)
	if err != nil {
		t.Fatal(err)
	}
	fixture.w = reopened
	afterReload := queryStateItems(t, fixture, "", "", 0, 0)
	if !reflect.DeepEqual(items, afterReload) {
		t.Fatal("重新打开工作区后状态派生结果变化")
	}

	doctor, err := fixture.w.doctor(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	warnings := doctor.(map[string]any)["warnings"].([]string)
	if len(warnings) != 0 {
		t.Fatalf("人物已有后续存活证据，不应继续按旧死亡记录告警：%v", warnings)
	}
}

func resealStateIndex(t *testing.T, fixture statesFixture, entityIndex bool, mutate func([]map[string]any)) {
	t.Helper()
	root, h, m, err := fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	path := m.CharactersPath
	if entityIndex {
		path = m.EntitiesPath
	}
	records, err := currentRecords(root, path)
	if err != nil {
		t.Fatal(err)
	}
	mutate(records)
	if err := atomicJSON(root, path, records); err != nil {
		t.Fatal(err)
	}
	hash, err := fileSHA(root, path)
	if err != nil {
		t.Fatal(err)
	}
	m.ContentHashes[path] = hash
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
}

func TestStatesQueryRejectsTamperingAndInvalidEvidence(t *testing.T) {
	t.Run("index-hash", func(t *testing.T) {
		fixture := newStatesFixture(t)
		root, _, m, err := fixture.w.load(fixture.id)
		if err != nil {
			t.Fatal(err)
		}
		characters, _ := currentRecords(root, m.CharactersPath)
		characters[0]["name"] = "被篡改"
		if err := atomicJSON(root, m.CharactersPath, characters); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.w.query(fixture.id, "states", "", "", 0, 20, 0); err == nil || !strings.Contains(err.Error(), "人物索引哈希") {
			t.Fatalf("篡改索引未被哈希门禁拒绝：%v", err)
		}
	})

	t.Run("resealed-false-evidence", func(t *testing.T) {
		fixture := newStatesFixture(t)
		resealStateIndex(t, fixture, false, func(records []map[string]any) {
			for _, record := range records {
				if record["id"] == "char_lan" {
					record["cognition"].([]any)[1].(map[string]any)["evidence"] = "正文不存在的事实"
				}
			}
		})
		if _, err := fixture.w.query(fixture.id, "states", "", "", 0, 20, 0); err == nil || !strings.Contains(err.Error(), "证据") {
			t.Fatalf("重封哈希后的伪证据未被 states 拒绝：%v", err)
		}
		if _, err := fixture.w.doctor(fixture.id); err == nil || !strings.Contains(err.Error(), "证据") {
			t.Fatalf("doctor 与 states 对伪证据判断不一致：%v", err)
		}
	})

	t.Run("dangling-subject", func(t *testing.T) {
		fixture := newStatesFixture(t)
		resealStateIndex(t, fixture, true, func(records []map[string]any) {
			for _, record := range records {
				if record["id"] == "state_key_tower" {
					record["subjectId"] = "item_missing"
				}
			}
		})
		if _, err := fixture.w.query(fixture.id, "states", "", "", 0, 20, 0); err == nil || !strings.Contains(err.Error(), "引用缺失") {
			t.Fatalf("不存在主体未被 states 拒绝：%v", err)
		}
		if _, err := fixture.w.doctor(fixture.id); err == nil || !strings.Contains(err.Error(), "引用缺失") {
			t.Fatalf("doctor 与 states 对悬空主体判断不一致：%v", err)
		}
	})

	t.Run("chapter-content-hash", func(t *testing.T) {
		fixture := newStatesFixture(t)
		root, _, m, err := fixture.w.load(fixture.id)
		if err != nil {
			t.Fatal(err)
		}
		entries, err := fixture.w.entries(root, m)
		if err != nil {
			t.Fatal(err)
		}
		chapter, err := loadChapter(root, entries[0].Path)
		if err != nil {
			t.Fatal(err)
		}
		chapter.Content = "正文被篡改"
		if err := atomicJSON(root, entries[0].Path, chapter); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.w.query(fixture.id, "states", "", "", 0, 20, 0); err == nil || !strings.Contains(err.Error(), "章节哈希") {
			t.Fatalf("章节正文篡改未被 states 拒绝：%v", err)
		}
	})

	t.Run("invalid-death-status", func(t *testing.T) {
		fixture := newStatesFixture(t)
		resealStateIndex(t, fixture, false, func(records []map[string]any) {
			for _, record := range records {
				if record["id"] == "char_lan" {
					record["death"].([]any)[1].(map[string]any)["status"] = "maybe"
				}
			}
		})
		if _, err := fixture.w.query(fixture.id, "states", "", "", 0, 20, 0); err == nil || !strings.Contains(err.Error(), "死亡状态") {
			t.Fatalf("非法兼容死亡状态未拒绝：%v", err)
		}
	})
}

func TestStatesQueryDoesNotMutatePersistedIndexes(t *testing.T) {
	fixture := newStatesFixture(t)
	root, _, m, err := fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	characterBefore, err := os.ReadFile(mustSafePath(t, root, m.CharactersPath))
	if err != nil {
		t.Fatal(err)
	}
	entityBefore, err := os.ReadFile(mustSafePath(t, root, m.EntitiesPath))
	if err != nil {
		t.Fatal(err)
	}
	_ = queryStateItems(t, fixture, "", "", 0, 0)
	characterAfter, _ := os.ReadFile(mustSafePath(t, root, m.CharactersPath))
	entityAfter, _ := os.ReadFile(mustSafePath(t, root, m.EntitiesPath))
	if !reflect.DeepEqual(characterBefore, characterAfter) || !reflect.DeepEqual(entityBefore, entityAfter) {
		t.Fatal("只读 states 查询修改了持久化索引")
	}
}

func mustSafePath(t *testing.T, root, relative string) string {
	t.Helper()
	path, err := safePath(root, relative)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
