package main

import (
	"math"
	"testing"
)

func TestThirtyDateMoneyAgeAndTimelineCases(t *testing.T) {
	deadline := func(id, subject, due, source string) map[string]any {
		return map[string]any{"id": id, "kind": "deadline", "subject": subject, "dueDate": due, "sourceChapterId": source, "evidence": "证据"}
	}
	amount := func(id, subject string, value any) map[string]any {
		return map[string]any{"id": id, "kind": "amount", "subject": subject, "value": value, "currency": "CNY", "sourceChapterId": "ch_1", "evidence": "证据"}
	}
	event := func(date string) map[string]any {
		return map[string]any{"id": "event_1", "date": date, "sourceChapterId": "ch_1", "evidence": "证据"}
	}
	base := deadline("d1", "医药费", "2026-09-22", "ch_1")
	caseList := []struct {
		name                  string
		constraints, timeline []any
		characters            []map[string]any
		valid                 bool
	}{
		{"绝对期限", []any{base}, nil, nil, true},
		{"格式错误", []any{deadline("d1", "医药费", "2026/09/22", "ch_1")}, nil, nil, false},
		{"非闰年二月", []any{deadline("d1", "医药费", "2025-02-29", "ch_1")}, nil, nil, false},
		{"闰年二月", []any{deadline("d1", "医药费", "2024-02-29", "ch_1")}, nil, nil, true},
		{"二十一天吻合", []any{map[string]any{"id": "d1", "kind": "deadline", "subject": "医药费", "dueDate": "2026-09-22", "anchorDate": "2026-09-01", "relativeDays": float64(21), "sourceChapterId": "ch_1", "evidence": "证据"}}, nil, nil, true},
		{"二十一天冲突", []any{map[string]any{"id": "d1", "kind": "deadline", "subject": "医药费", "dueDate": "2026-11-06", "anchorDate": "2026-09-01", "relativeDays": float64(21), "sourceChapterId": "ch_1", "evidence": "证据"}}, nil, nil, false},
		{"相对期限缺锚", []any{map[string]any{"id": "d1", "kind": "deadline", "subject": "医药费", "relativeDays": float64(21), "sourceChapterId": "ch_1", "evidence": "证据"}}, nil, nil, false},
		{"相对期限零天", []any{map[string]any{"id": "d1", "kind": "deadline", "subject": "医药费", "anchorDate": "2026-09-01", "relativeDays": float64(0), "sourceChapterId": "ch_1", "evidence": "证据"}}, nil, nil, false},
		{"相对期限负天", []any{map[string]any{"id": "d1", "kind": "deadline", "subject": "医药费", "anchorDate": "2026-09-01", "relativeDays": float64(-1), "sourceChapterId": "ch_1", "evidence": "证据"}}, nil, nil, false},
		{"相对期限小数", []any{map[string]any{"id": "d1", "kind": "deadline", "subject": "医药费", "anchorDate": "2026-09-01", "relativeDays": float64(1.5), "sourceChapterId": "ch_1", "evidence": "证据"}}, nil, nil, false},
		{"相对期限过长", []any{map[string]any{"id": "d1", "kind": "deadline", "subject": "医药费", "anchorDate": "2026-09-01", "relativeDays": float64(36501), "sourceChapterId": "ch_1", "evidence": "证据"}}, nil, nil, false},
		{"有效金额", []any{amount("a1", "医药费", float64(100))}, nil, nil, true},
		{"零金额", []any{amount("a1", "医药费", float64(0))}, nil, nil, false},
		{"负金额", []any{amount("a1", "医药费", float64(-1))}, nil, nil, false},
		{"缺少币种", []any{map[string]any{"id": "a1", "kind": "amount", "subject": "医药费", "value": float64(10), "sourceChapterId": "ch_1", "evidence": "证据"}}, nil, nil, false},
		{"金额字符串", []any{amount("a1", "医药费", "100")}, nil, nil, false},
		{"金额无穷", []any{amount("a1", "医药费", math.Inf(1))}, nil, nil, false},
		{"相同截止日期", []any{base, deadline("d2", "医药费", "2026-09-22", "ch_2")}, nil, nil, true},
		{"不同截止日期冲突", []any{base, deadline("d2", "医药费", "2026-11-06", "ch_2")}, nil, nil, false},
		{"明确延期覆盖", []any{base, map[string]any{"id": "d2", "kind": "deadline", "subject": "医药费", "dueDate": "2026-11-06", "sourceChapterId": "ch_2", "evidence": "证据", "supersedes": "d1"}}, nil, nil, true},
		{"延期引用缺失", []any{base, map[string]any{"id": "d2", "kind": "deadline", "subject": "医药费", "dueDate": "2026-11-06", "sourceChapterId": "ch_2", "evidence": "证据", "supersedes": "unknown"}}, nil, nil, false},
		{"相同金额", []any{amount("a1", "医药费", float64(100)), map[string]any{"id": "a2", "kind": "amount", "subject": "医药费", "value": float64(100), "currency": "CNY", "sourceChapterId": "ch_2", "evidence": "证据"}}, nil, nil, true},
		{"不同金额冲突", []any{amount("a1", "医药费", float64(100)), map[string]any{"id": "a2", "kind": "amount", "subject": "医药费", "value": float64(200), "currency": "CNY", "sourceChapterId": "ch_2", "evidence": "证据"}}, nil, nil, false},
		{"不同主题金额", []any{amount("a1", "医药费", float64(100)), amount("a2", "房租", float64(200))}, nil, nil, true},
		{"时间线有效", nil, []any{event("2026-09-01")}, nil, true},
		{"时间线日期无效", nil, []any{event("2026-13-01")}, nil, false},
		{"结束早于开始", nil, []any{map[string]any{"id": "event_1", "startDate": "2026-09-02", "endDate": "2026-09-01", "sourceChapterId": "ch_1", "evidence": "证据"}}, nil, false},
		{"跨年事件", nil, []any{map[string]any{"id": "event_1", "startDate": "2026-12-31", "endDate": "2027-01-01", "sourceChapterId": "ch_1", "evidence": "证据"}}, nil, true},
		{"生日前年龄", nil, []any{map[string]any{"id": "event_1", "date": "2026-12-30", "characterId": "char_a", "ageYears": float64(25), "sourceChapterId": "ch_1", "evidence": "证据"}}, []map[string]any{{"id": "char_a", "birthDate": "2000-12-31"}}, true},
		{"生日前年龄冲突", nil, []any{map[string]any{"id": "event_1", "date": "2026-12-30", "characterId": "char_a", "ageYears": float64(26), "sourceChapterId": "ch_1", "evidence": "证据"}}, []map[string]any{{"id": "char_a", "birthDate": "2000-12-31"}}, false},
	}
	if len(caseList) != 30 {
		t.Fatalf("样例数量=%d，需30", len(caseList))
	}
	entries := []entry{{ID: "ch_1", Number: 1}, {ID: "ch_2", Number: 2}}
	contents := map[string]string{"ch_1": "证据：虚构章节一。", "ch_2": "证据：虚构章节二。"}
	for _, test := range caseList {
		t.Run(test.name, func(t *testing.T) {
			memory := map[string]any{"constraints": test.constraints, "timeline": test.timeline}
			if memory["constraints"] == nil {
				memory["constraints"] = []any{}
			}
			if memory["timeline"] == nil {
				memory["timeline"] = []any{}
			}
			err := validateContinuity(memory, entries, contents, test.characters)
			if (err == nil) != test.valid {
				t.Fatalf("期望 valid=%t，得到 %v", test.valid, err)
			}
		})
	}
}
