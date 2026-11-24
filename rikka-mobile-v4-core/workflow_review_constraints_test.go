package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reviewConstraintBlueprint() map[string]any {
	plan := map[string]any{
		"theme":    "虚构审稿主题",
		"mainPlot": map[string]any{"id": "main_review", "summary": "验证篇幅与视角"},
		"subplots": []any{}, "volumes": []any{},
		"chapterOutline": []any{map[string]any{
			"id": "outline_plan", "title": "第一章", "chapterNumber": float64(1),
			"summary": "阿澜观察庭院", "pov": "阿澜限知视角",
		}},
		"chapterLengths": []any{map[string]any{
			"id": "length_plan", "chapterNumber": float64(1),
			"minWords": float64(4), "maxWords": float64(6), "targetWords": float64(5),
		}},
		"characterArcs": []any{}, "pacing": []any{}, "majorEvents": []any{},
	}
	canon := map[string]any{
		"chapterOutline": []any{map[string]any{
			"id": "outline_canon", "title": "第一章正史要求", "chapterNumber": float64(1),
			"summary": "保持阿澜所知边界", "pov": "阿澜限知视角",
		}},
		"chapterLengths": []any{map[string]any{
			"id": "length_canon", "chapterNumber": float64(1),
			"minWords": float64(4), "maxWords": float64(6), "targetWords": float64(5),
		}},
	}
	candidate := map[string]any{
		"chapterOutline": []any{map[string]any{
			"id": "outline_candidate", "title": "候选视角", "chapterNumber": float64(1),
			"summary": "尚未确认的薄舟视角", "pov": "薄舟视角",
		}},
		"chapterLengths": []any{map[string]any{
			"id": "length_candidate", "chapterNumber": float64(1),
			"minWords": float64(5), "maxWords": float64(5), "targetWords": float64(5),
		}},
	}
	return map[string]any{"schemaVersion": float64(2), "plan": plan, "candidate": candidate, "canon": canon}
}

type reviewConstraintFixture struct {
	w            *workspace
	id           string
	prepareID    string
	requirements []prepareRequirement
}

func newReviewConstraintFixture(t *testing.T) reviewConstraintFixture {
	t.Helper()
	w := testWorkspace(t)
	id := testInit(t, w)
	input := testRecordStage(t, w, id, strings.Repeat("1", 32), "blueprint-set", reviewConstraintBlueprint())
	if _, err := w.mutateFromFile(id, input, 1); err != nil {
		t.Fatal(err)
	}
	prepared, err := w.prepareChapter(id, "ch_1", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	value := prepared.(map[string]any)
	return reviewConstraintFixture{w: w, id: id, prepareID: value["prepareId"].(string),
		requirements: value["requirements"].([]prepareRequirement)}
}

func writeReviewDraft(t *testing.T, fixture reviewConstraintFixture, op, content string) string {
	t.Helper()
	path := filepath.Join(fixture.w.inbox, fixture.id, op+".json")
	data, err := json.Marshal(draftCandidate{OperationID: op, PrepareID: fixture.prepareID,
		Chapter: chapter{ID: "ch_1", Title: "虚构第一章", Content: content}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.w.draftWorkflow(fixture.id, path, 2); err != nil {
		t.Fatal(err)
	}
	return op
}

func writeConstraintReview(t *testing.T, fixture reviewConstraintFixture, op, draftID string, withPOVNote bool) (string, map[string]any) {
	t.Helper()
	items := make([]reviewItem, 0, len(fixture.requirements))
	for _, req := range fixture.requirements {
		item := reviewItem{ID: req.ID, Status: "satisfied"}
		if req.Kind != "length" {
			item.Quote = "甲乙"
			if req.SemanticReview && withPOVNote {
				item.Note = "已由模型按完整叙事语义核查；程序未使用人称代词规则冒充判断"
			}
		}
		if req.SourceType == "blueprint:candidate" && req.Kind == "outline" {
			item.Status = "not_applicable"
			item.Quote = ""
			item.Note = "候选视角尚未由作者确认"
		}
		items = append(items, item)
	}
	path := filepath.Join(fixture.w.inbox, fixture.id, op+".json")
	data, err := json.Marshal(reviewCandidate{OperationID: op, PrepareID: fixture.prepareID,
		DraftID: draftID, AuthorDecision: "作者确认候选视角本章不适用", Requirements: items})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.w.reviewWorkflow(fixture.id, path, 2)
	if err != nil {
		t.Fatal(err)
	}
	return op, result.(map[string]any)
}

func TestReviewLengthSourcesBoundariesAndHanziCounting(t *testing.T) {
	fixture := newReviewConstraintFixture(t)
	if got := countHanzi("甲，A乙!𠀀"); got != 3 {
		t.Fatalf("中文汉字、标点、中英文混排或扩展汉字计数错误：%d", got)
	}
	byID := map[string]prepareRequirement{}
	for _, req := range fixture.requirements {
		byID[req.ID] = req
	}
	for _, id := range []string{"length:length_plan", "length:length_canon", "length:length_candidate"} {
		req, ok := byID[id]
		if !ok || req.MinWords == 0 || req.MaxWords == 0 || req.TargetWords != 5 {
			t.Fatalf("篇幅来源或字段未完整传递：%s %v", id, req)
		}
	}
	if !byID["length:length_plan"].Hard || !byID["length:length_canon"].Hard || byID["length:length_candidate"].Hard {
		t.Fatal("plan/canon 硬约束与 candidate 软约束标记错误")
	}

	tests := []struct {
		name, content, draftOp, reviewOp string
		wantPass                         bool
	}{
		{"below-min", "甲乙丙，ABC!", strings.Repeat("2", 32), strings.Repeat("3", 32), false},
		{"at-min", "甲乙丙丁，ABC!", strings.Repeat("4", 32), strings.Repeat("5", 32), true},
		{"at-target", "甲乙丙丁戊，ABC!", strings.Repeat("6", 32), strings.Repeat("7", 32), true},
		{"at-max", "甲乙丙丁戊己，ABC!", strings.Repeat("8", 32), strings.Repeat("9", 32), true},
		{"above-max", "甲乙丙丁戊己庚，ABC!", strings.Repeat("a", 32), strings.Repeat("b", 32), false},
	}
	// Candidate has a soft exact target of 5. Its mismatch can be explicitly
	// waived by the author; hard plan/canon boundaries still gate finalization.
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			draftID := writeReviewDraft(t, fixture, tc.draftOp, tc.content)
			// For non-target candidate-length cases, explicitly waive only that
			// soft requirement while keeping hard requirements satisfied.
			items := make([]reviewItem, 0, len(fixture.requirements))
			for _, req := range fixture.requirements {
				item := reviewItem{ID: req.ID, Status: "satisfied"}
				if req.Kind != "length" {
					item.Quote = "甲乙"
					if req.SemanticReview {
						item.Note = "模型按叙事语义核查，程序不作代词推断"
					}
				}
				if req.SourceType == "blueprint:candidate" && ((req.Kind == "outline") || countHanzi(tc.content) != 5) {
					item.Status = "not_applicable"
					item.Quote = ""
				}
				items = append(items, item)
			}
			path := filepath.Join(fixture.w.inbox, fixture.id, tc.reviewOp+".json")
			data, _ := json.Marshal(reviewCandidate{OperationID: tc.reviewOp, PrepareID: fixture.prepareID,
				DraftID: draftID, AuthorDecision: "作者明确豁免未确认候选约束", Requirements: items})
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			result, err := fixture.w.reviewWorkflow(fixture.id, path, 2)
			if err != nil {
				t.Fatal(err)
			}
			passed := result.(map[string]any)["verdict"] == "pass"
			if passed != tc.wantPass {
				t.Fatalf("篇幅边界审稿结果错误：%v", result)
			}
		})
	}
}

func TestReviewPOVRequiresSemanticReviewAndFailedReviewBlocksFinalize(t *testing.T) {
	fixture := newReviewConstraintFixture(t)
	for _, req := range fixture.requirements {
		if req.POV != "" && !req.SemanticReview {
			t.Fatalf("POV 没有携带语义审稿标记：%v", req)
		}
	}
	draftID := writeReviewDraft(t, fixture, strings.Repeat("c", 32), "他见甲乙丙，ABC!")
	failedReviewID, failed := writeConstraintReview(t, fixture, strings.Repeat("d", 32), draftID, false)
	if failed["verdict"] != "fail" || !strings.Contains(strings.Join(failed["defects"].([]string), "|"), "POV 需要模型语义审稿") {
		t.Fatalf("仅有代词和引文却没有语义核查说明时不应通过：%v", failed)
	}
	finalOp := strings.Repeat("e", 32)
	finalPath := filepath.Join(fixture.w.inbox, fixture.id, finalOp+".json")
	finalData, _ := json.Marshal(finalCandidate{OperationID: finalOp, PrepareID: fixture.prepareID,
		DraftID: draftID, ReviewID: failedReviewID,
		Chapter: chapter{ID: "ch_1", Title: "虚构第一章"}})
	if err := os.WriteFile(finalPath, finalData, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.w.finalizeWorkflow(fixture.id, finalPath, 2); err == nil || !strings.Contains(err.Error(), "审稿未通过") {
		t.Fatalf("POV 审稿失败后仍允许定稿：%v", err)
	}

	passedReviewID, passed := writeConstraintReview(t, fixture, strings.Repeat("f", 32), draftID, true)
	if passed["verdict"] != "pass" {
		t.Fatalf("有明确语义核查说明且篇幅合格时应通过：%v", passed)
	}
	if _, _, err := fixture.w.loadReview(fixture.id, passedReviewID); err != nil {
		t.Fatalf("审稿记录未持久化：%v", err)
	}
}

func TestBlueprintRejectsTargetOutsideLengthBoundsAtomically(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target float64
		want   string
	}{{"below", 3, "目标低于下限"}, {"above", 7, "目标高于上限"}} {
		t.Run(tc.name, func(t *testing.T) {
			w := testWorkspace(t)
			id := testInit(t, w)
			blueprint := reviewConstraintBlueprint()
			plan := blueprint["plan"].(map[string]any)
			plan["chapterLengths"].([]any)[0].(map[string]any)["targetWords"] = tc.target
			input := testRecordStage(t, w, id, strings.Repeat("1", 31)+string(tc.name[0]), "blueprint-set", blueprint)
			if _, err := w.mutateFromFile(id, input, 1); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("非法 targetWords 未拒绝：%v", err)
			}
			_, h, m, err := w.load(id)
			if err != nil || h.Revision != 1 || m.BlueprintPath != "" {
				t.Fatalf("非法蓝图破坏了原子性：head=%v manifest=%v err=%v", h, m, err)
			}
		})
	}
}
