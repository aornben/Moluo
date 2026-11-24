package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type blueprintPromotionFixture struct {
	w         *workspace
	id        string
	revision  int
	blueprint map[string]any
}

func blueprintPlanFixture() map[string]any {
	return map[string]any{
		"theme":    "虚构主题保持不变",
		"mainPlot": map[string]any{"id": "main_plan", "summary": "虚构主线保持不变"},
		"subplots": []any{map[string]any{"id": "sub_plan", "summary": "计划支线"}},
		"volumes":  []any{map[string]any{"id": "vol_plan", "title": "计划卷", "summary": "计划卷纲"}},
		"chapterOutline": []any{map[string]any{
			"id": "outline_plan", "title": "计划章", "chapterNumber": float64(9), "summary": "计划提纲"}},
		"chapterLengths": []any{map[string]any{
			"id": "length_plan", "chapterNumber": float64(9), "minWords": float64(900), "maxWords": float64(1900), "targetWords": float64(1400)}},
		"characterArcs": []any{map[string]any{"id": "arc_plan", "characterId": "char_plan", "summary": "计划人物弧"}},
		"pacing":        []any{map[string]any{"chapterId": "ch_9", "tension": float64(5)}},
		"majorEvents":   []any{map[string]any{"id": "event_plan", "summary": "计划事件"}},
	}
}

func blueprintCandidateFixture() map[string]any {
	return map[string]any{
		"subplots": []any{
			map[string]any{"id": "sub_promote", "summary": "待晋升支线", "customMarker": "必须保留"},
			map[string]any{"id": "sub_keep", "summary": "保留候选支线"},
		},
		"chapterOutline": []any{
			map[string]any{"id": "outline_promote", "title": "第一章候选", "chapterNumber": float64(1), "summary": "待晋升提纲", "pov": "阿岚"},
			map[string]any{"id": "outline_keep", "title": "第三章候选", "chapterNumber": float64(3), "summary": "保留候选提纲"},
		},
		"chapterLengths": []any{
			map[string]any{"id": "length_promote", "chapterNumber": float64(1), "minWords": float64(1000), "maxWords": float64(3000), "targetWords": float64(2000), "customMarker": "篇幅字段保留"},
			map[string]any{"id": "length_keep", "chapterNumber": float64(3), "minWords": float64(800), "maxWords": float64(1800), "targetWords": float64(1300)},
		},
		"pacing": []any{
			map[string]any{"chapterId": "ch_1", "tension": float64(8), "note": "待晋升节奏"},
			map[string]any{"chapterId": "ch_3", "tension": float64(6), "note": "保留候选节奏"},
		},
		"majorEvents": []any{map[string]any{"id": "event_keep", "summary": "不参与本次晋升"}},
	}
}

func blueprintCanonFixture() map[string]any {
	return map[string]any{
		"subplots":       []any{map[string]any{"id": "sub_canon", "summary": "既有正史支线"}},
		"chapterOutline": []any{map[string]any{"id": "outline_canon", "title": "第二章正史", "chapterNumber": float64(2), "summary": "既有正史提纲"}},
		"chapterLengths": []any{map[string]any{"id": "length_canon", "chapterNumber": float64(2), "minWords": float64(1100), "maxWords": float64(2100), "targetWords": float64(1600)}},
		"pacing":         []any{map[string]any{"chapterId": "ch_2", "tension": float64(4)}},
		"majorEvents":    []any{map[string]any{"id": "event_canon", "summary": "既有正史事件"}},
	}
}

func newBlueprintPromotionFixture(t *testing.T, mutate func(map[string]any)) blueprintPromotionFixture {
	t.Helper()
	w := testWorkspace(t)
	id := testInit(t, w)
	blueprint := map[string]any{
		"schemaVersion": float64(2), "plan": blueprintPlanFixture(),
		"candidate": blueprintCandidateFixture(), "canon": blueprintCanonFixture(),
		"metadata": map[string]any{"custom": "整份蓝图字段保持不变"},
	}
	if mutate != nil {
		mutate(blueprint)
	}
	input := testRecordStage(t, w, id, strings.Repeat("1", 32), "blueprint-set", blueprint)
	if _, err := w.mutateFromFile(id, input, 1); err != nil {
		t.Fatal(err)
	}
	return blueprintPromotionFixture{w: w, id: id, revision: 2, blueprint: blueprint}
}

func stageBlueprintPromotion(t *testing.T, fixture blueprintPromotionFixture, operationID, decision string, items any, confirm bool) string {
	t.Helper()
	path := testRecordStage(t, fixture.w, fixture.id, operationID, "blueprint-promote", map[string]any{
		"authorDecision": decision, "items": items,
	})
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

func promotionItems() []any {
	return []any{
		map[string]any{"field": "subplots", "id": "sub_promote"},
		map[string]any{"field": "chapterOutline", "id": "outline_promote"},
		map[string]any{"field": "chapterLengths", "id": "length_promote"},
		map[string]any{"field": "pacing", "id": "ch_1"},
	}
}

func loadCurrentBlueprint(t *testing.T, fixture blueprintPromotionFixture) (string, head, manifest, map[string]any) {
	t.Helper()
	root, h, m, err := fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	blueprint, err := currentBlueprint(root, m)
	if err != nil {
		t.Fatal(err)
	}
	return root, h, m, blueprint
}

func blueprintIDs(layer map[string]any, field string) []string {
	ids := []string{}
	for _, raw := range charSlice(layer, field) {
		entry, _ := raw.(map[string]any)
		ids = append(ids, blueprintEntryID(field, entry))
	}
	return ids
}

func resealBlueprint(t *testing.T, fixture blueprintPromotionFixture, mutate func(map[string]any)) {
	t.Helper()
	root, h, m, blueprint := loadCurrentBlueprint(t, fixture)
	mutate(blueprint)
	if err := atomicJSON(root, m.BlueprintPath, blueprint); err != nil {
		t.Fatal(err)
	}
	hash, err := fileSHA(root, m.BlueprintPath)
	if err != nil {
		t.Fatal(err)
	}
	m.BlueprintSHA256 = hash
	if err := atomicJSON(root, h.Manifest, m); err != nil {
		t.Fatal(err)
	}
	manifestHash, err := fileSHA(root, h.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	h.ManifestSHA256 = manifestHash
	if err := atomicJSON(root, "HEAD.json", h); err != nil {
		t.Fatal(err)
	}
}

func TestBlueprintPromoteAuthorizationAndInvalidSelectionsAreAtomic(t *testing.T) {
	fixture := newBlueprintPromotionFixture(t, nil)
	_, beforeHead, beforeManifest, beforeBlueprint := loadCurrentBlueprint(t, fixture)
	cases := []struct {
		name, operationID, decision, want string
		items                             any
		confirm                           bool
	}{
		{"without-confirm", strings.Repeat("2", 32), "作者同意", "明确确认", promotionItems(), false},
		{"without-decision", strings.Repeat("3", 32), "   ", "作者决定", promotionItems(), true},
		{"empty-items", strings.Repeat("4", 32), "作者同意", "非空 items", []any{}, true},
		{"non-object-item", strings.Repeat("5", 32), "作者同意", "必须是对象", []any{"bad"}, true},
		{"illegal-field", strings.Repeat("6", 32), "作者同意", "晋升条目无效", []any{map[string]any{"field": "theme", "id": "theme_1"}}, true},
		{"invalid-id", strings.Repeat("7", 32), "作者同意", "晋升条目无效", []any{map[string]any{"field": "subplots", "id": "bad id"}}, true},
		{"missing-candidate", strings.Repeat("8", 32), "作者同意", "数量异常", []any{map[string]any{"field": "subplots", "id": "sub_missing"}}, true},
		{"duplicate-selection", strings.Repeat("9", 32), "作者同意", "晋升条目重复", []any{
			map[string]any{"field": "subplots", "id": "sub_promote"}, map[string]any{"field": "subplots", "id": "sub_promote"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := stageBlueprintPromotion(t, fixture, tc.operationID, tc.decision, tc.items, tc.confirm)
			if _, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("非法晋升未按契约拒绝：%v", err)
			}
			_, h, m, blueprint := loadCurrentBlueprint(t, fixture)
			if h != beforeHead || m.BlueprintPath != beforeManifest.BlueprintPath ||
				m.BlueprintSHA256 != beforeManifest.BlueprintSHA256 || !reflect.DeepEqual(blueprint, beforeBlueprint) {
				t.Fatal("失败晋升改变了 HEAD、manifest 或蓝图分层")
			}
		})
	}
}

func TestBlueprintPromoteMovesLayersPersistsAuditAndSupportsPrepare(t *testing.T) {
	fixture := newBlueprintPromotionFixture(t, nil)
	_, _, beforeManifest, beforeBlueprint := loadCurrentBlueprint(t, fixture)
	beforePlan := beforeBlueprint["plan"]
	beforeMetadata := beforeBlueprint["metadata"]
	items := promotionItems()
	operationID := "abababababababababababababababab"
	decision := "作者确认这些候选设定成为正史"
	input := stageBlueprintPromotion(t, fixture, operationID, decision, items, true)
	result, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision)
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["revision"] != 3 || result.(map[string]any)["recordId"] != "blueprint" {
		t.Fatalf("晋升提交结果错误：%v", result)
	}
	root, h, m, blueprint := loadCurrentBlueprint(t, fixture)
	if h.Revision != 3 || m.BlueprintPath == beforeManifest.BlueprintPath || m.BlueprintSHA256 == "" {
		t.Fatalf("蓝图路径、版本或哈希未更新：HEAD=%v manifest=%v", h, m)
	}
	actualHash, err := fileSHA(root, m.BlueprintPath)
	if err != nil || actualHash != m.BlueprintSHA256 {
		t.Fatal("蓝图 manifest SHA-256 与磁盘不一致", err)
	}
	if !reflect.DeepEqual(blueprint["plan"], beforePlan) || !reflect.DeepEqual(blueprint["metadata"], beforeMetadata) {
		t.Fatal("晋升误改了 plan 或无关顶层数据")
	}
	candidate := blueprint["candidate"].(map[string]any)
	canon := blueprint["canon"].(map[string]any)
	for field, promotedID := range map[string]string{
		"subplots": "sub_promote", "chapterOutline": "outline_promote",
		"chapterLengths": "length_promote", "pacing": "ch_1",
	} {
		if containsString(blueprintIDs(candidate, field), promotedID) || !containsString(blueprintIDs(canon, field), promotedID) {
			t.Fatalf("条目未从 candidate 正确移动到 canon：%s/%s", field, promotedID)
		}
	}
	for field, keepID := range map[string]string{
		"subplots": "sub_keep", "chapterOutline": "outline_keep", "chapterLengths": "length_keep",
		"pacing": "ch_3", "majorEvents": "event_keep",
	} {
		if !containsString(blueprintIDs(candidate, field), keepID) {
			t.Fatalf("无关候选被误改：%s/%s", field, keepID)
		}
	}
	for field, existingID := range map[string]string{
		"subplots": "sub_canon", "chapterOutline": "outline_canon", "chapterLengths": "length_canon",
		"pacing": "ch_2", "majorEvents": "event_canon",
	} {
		if !containsString(blueprintIDs(canon, field), existingID) {
			t.Fatalf("既有正史被误改：%s/%s", field, existingID)
		}
	}
	promotedSubplot := charSlice(canon, "subplots")[1].(map[string]any)
	promotedLength := charSlice(canon, "chapterLengths")[1].(map[string]any)
	if promotedSubplot["customMarker"] != "必须保留" || promotedLength["customMarker"] != "篇幅字段保留" ||
		intValue(promotedLength["minWords"]) != 1000 || intValue(promotedLength["maxWords"]) != 3000 {
		t.Fatal("晋升丢失了候选原有合法字段或 chapterLengths 约束")
	}
	history := blueprint["promotionHistory"].([]any)
	if len(history) != 1 {
		t.Fatalf("晋升审计数量错误：%v", history)
	}
	audit := history[0].(map[string]any)
	if audit["operationId"] != operationID || audit["authorDecision"] != decision || intValue(audit["revision"]) != 3 ||
		!reflect.DeepEqual(audit["items"], items) {
		t.Fatalf("晋升作者授权审计未完整持久化：%v", audit)
	}
	if err := validateBlueprint(blueprint); err != nil {
		t.Fatal(err)
	}
	var operation opRecord
	if err := loadJSON(root, "operations/"+operationID+".json", &operation); err != nil ||
		operation.Revision != 3 || operation.RecordID != "blueprint" || operation.Kind != "blueprint-promote" {
		t.Fatalf("晋升操作审计记录不一致：%v %v", operation, err)
	}
	page, err := fixture.w.query(fixture.id, "blueprint", "", "", 0, 10, 0)
	if err != nil || page.(map[string]any)["total"] != 1 {
		t.Fatalf("晋升后蓝图无法按哈希回读：%v %v", page, err)
	}
	prepared, err := fixture.w.prepareChapter(fixture.id, "ch_1", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	requirements := prepared.(map[string]any)["requirements"].([]prepareRequirement)
	outlineHard, lengthHard := false, false
	for _, requirement := range requirements {
		if requirement.ID == "outline:outline_promote" {
			outlineHard = requirement.Hard && requirement.SourceType == "blueprint:canon"
		}
		if requirement.ID == "length:length_promote" {
			lengthHard = requirement.Hard && requirement.SourceType == "blueprint:canon" &&
				requirement.MinWords == 1000 && requirement.MaxWords == 3000
		}
	}
	if !outlineHard || !lengthHard {
		t.Fatalf("写作准备未把晋升后的提纲/篇幅作为正史硬约束：%v", requirements)
	}
	if _, err := fixture.w.doctor(fixture.id); err != nil {
		t.Fatal(err)
	}
}

func TestBlueprintPromoteRejectsCanonConflicts(t *testing.T) {
	cases := []struct {
		name  string
		field string
		id    string
		entry map[string]any
	}{
		{"stable-id-conflict", "subplots", "sub_promote", map[string]any{"id": "sub_promote", "summary": "冲突正史支线"}},
		{"pacing-chapter-conflict", "pacing", "ch_1", map[string]any{"chapterId": "ch_1", "tension": float64(2)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newBlueprintPromotionFixture(t, func(blueprint map[string]any) {
				canon := blueprint["canon"].(map[string]any)
				canon[tc.field] = append(charSlice(canon, tc.field), tc.entry)
			})
			_, beforeHead, beforeManifest, beforeBlueprint := loadCurrentBlueprint(t, fixture)
			input := stageBlueprintPromotion(t, fixture, strings.Repeat("2", 32), "作者请求晋升",
				[]any{map[string]any{"field": tc.field, "id": tc.id}}, true)
			if _, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision); err == nil ||
				(!strings.Contains(err.Error(), "冲突") && !strings.Contains(err.Error(), "重复")) {
				t.Fatalf("正史冲突未拒绝：%v", err)
			}
			_, h, m, blueprint := loadCurrentBlueprint(t, fixture)
			if h != beforeHead || m.BlueprintPath != beforeManifest.BlueprintPath || !reflect.DeepEqual(blueprint, beforeBlueprint) {
				t.Fatal("冲突晋升留下候选删除或正史半成品")
			}
		})
	}
}

func TestBlueprintPromoteIdempotenceAndRecoveryAudit(t *testing.T) {
	fixture := newBlueprintPromotionFixture(t, nil)
	items := promotionItems()
	operationID := "abababababababababababababababab"
	input := stageBlueprintPromotion(t, fixture, operationID, "作者确认晋升", items, true)
	if _, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision); err != nil {
		t.Fatal(err)
	}
	retry, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision)
	if err != nil || retry.(map[string]any)["idempotent"] != true || retry.(map[string]any)["revision"] != 3 {
		t.Fatalf("相同 operationId 重试未幂等返回：%v %v", retry, err)
	}
	root, h, _, blueprint := loadCurrentBlueprint(t, fixture)
	if h.Revision != 3 || len(blueprint["promotionHistory"].([]any)) != 1 {
		t.Fatal("幂等重试重复增加 revision 或审计记录")
	}
	if err := os.Remove(filepath.Join(root, "operations", operationID+".json")); err != nil {
		t.Fatal(err)
	}
	recovered, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision)
	if err != nil || recovered.(map[string]any)["idempotent"] != true {
		t.Fatalf("缺失操作记录后未按蓝图与作者审计恢复：%v %v", recovered, err)
	}
	different := stageBlueprintPromotion(t, fixture, operationID, "不同作者决定", items, true)
	if _, err := fixture.w.mutateFromFile(fixture.id, different, fixture.revision); err == nil || !strings.Contains(err.Error(), "不同候选") {
		t.Fatalf("不同内容冒充同一 operationId：%v", err)
	}
	other := stageBlueprintPromotion(t, fixture, strings.Repeat("c", 32), "另一次晋升", items, true)
	if _, err := fixture.w.mutateFromFile(fixture.id, other, 3); err == nil || !strings.Contains(err.Error(), "数量异常") {
		t.Fatalf("不同操作重复晋升已移出候选的条目：%v", err)
	}

	if err := os.Remove(filepath.Join(root, "operations", operationID+".json")); err != nil {
		t.Fatal(err)
	}
	resealBlueprint(t, fixture, func(blueprint map[string]any) { delete(blueprint, "promotionHistory") })
	if _, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision); err == nil || !strings.Contains(err.Error(), "审计") {
		t.Fatalf("作者授权审计缺失时错误伪装幂等恢复：%v", err)
	}
}

func TestBlueprintPromoteWriteFailureKeepsOldHeadAndLayers(t *testing.T) {
	fixture := newBlueprintPromotionFixture(t, nil)
	root, beforeHead, beforeManifest, beforeBlueprint := loadCurrentBlueprint(t, fixture)
	operationID := "abababababababababababababababab"
	targetPath := "blueprints/" + revTag(fixture.revision+1) + "-" + operationID + ".json"
	if err := newJSON(root, targetPath, map[string]any{"conflict": true}); err != nil {
		t.Fatal(err)
	}
	input := stageBlueprintPromotion(t, fixture, operationID, "作者确认晋升", promotionItems(), true)
	if _, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision); err == nil {
		t.Fatal("蓝图目标文件冲突时晋升错误成功")
	}
	_, afterHead, afterManifest, afterBlueprint := loadCurrentBlueprint(t, fixture)
	if afterHead != beforeHead || afterManifest.BlueprintPath != beforeManifest.BlueprintPath ||
		afterManifest.BlueprintSHA256 != beforeManifest.BlueprintSHA256 || !reflect.DeepEqual(afterBlueprint, beforeBlueprint) {
		t.Fatal("写入失败后留下候选已删除或正史已写入的半成品")
	}
	if err := fixture.w.pending(root, beforeHead.Revision); err == nil || !strings.Contains(err.Error(), "未完成事务") {
		t.Fatalf("写入失败未留下保守的待恢复事务：%v", err)
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
