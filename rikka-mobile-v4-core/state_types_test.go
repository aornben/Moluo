package main

import (
	"fmt"
	"strings"
	"testing"
)

// TestStateTransitionCoversStructuredStateTypes exercises every structured
// stateType listed by the frozen scope through the real mutation path: first
// state, then a guarded transition, with the old state retained.
func TestStateTransitionCoversStructuredStateTypes(t *testing.T) {
	fixture := newStateTransitionFixture(t)
	types := []struct {
		name string
		v1   string
		v2   string
	}{
		{"travel", "在路上", "已抵达"},
		{"inventory", "三件", "两件"},
		{"debt", "一千元", "八百元"},
		{"promise", "已承诺", "已履行"},
		{"injury", "轻伤", "已痊愈"},
		{"condition", "完好", "有裂痕"},
	}
	for index, item := range types {
		op := fmt.Sprintf("%032x", 0xd200+index*2)
		initial := map[string]any{
			"id": "state_" + item.name + "_1", "name": "虚构" + item.name + "初始",
			"subjectId": "item_key", "stateType": item.name, "value": item.v1,
			"sourceChapterId": "ch_1", "evidence": "铜钥匙留在旧桥",
			"allowInitial": true, "authorDecision": "作者确认虚构初始状态",
		}
		input := stageStateTransition(t, fixture, op, initial, true)
		if _, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision); err != nil {
			t.Fatalf("%s 初始状态失败：%v", item.name, err)
		}
		fixture.revision++
		next := map[string]any{
			"id": "state_" + item.name + "_2", "name": "虚构" + item.name + "之后",
			"subjectId": "item_key", "stateType": item.name, "value": item.v2,
			"sourceChapterId": "ch_2", "evidence": "铜钥匙带到钟楼",
			"expectedPreviousStateId": "state_" + item.name + "_1",
			"expectedPreviousValue":   item.v1,
			"authorDecision":          "作者确认虚构状态变化",
		}
		input = stageStateTransition(t, fixture, fmt.Sprintf("%032x", 0xd201+index*2), next, true)
		if _, err := fixture.w.mutateFromFile(fixture.id, input, fixture.revision); err != nil {
			t.Fatalf("%s 转换失败：%v", item.name, err)
		}
		fixture.revision++
	}
	root, _, m, err := fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	entities := []map[string]any{}
	if err := loadJSON(root, m.EntitiesPath, &entities); err != nil {
		t.Fatal(err)
	}
	for _, item := range types {
		latest := map[string]any{}
		oldSeen := false
		for _, entity := range entities {
			if entity["subjectId"] != "item_key" || entity["stateType"] != item.name {
				continue
			}
			if entity["id"] == "state_"+item.name+"_1" {
				oldSeen = true
			}
			if entity["id"] == "state_"+item.name+"_2" {
				latest = entity
			}
		}
		if !oldSeen {
			t.Fatalf("%s 的旧状态没有保留为不可变历史", item.name)
		}
		if latest["value"] != item.v2 {
			t.Fatalf("%s 的最新状态值错误：%v", item.name, latest["value"])
		}
		if decision, _ := latest["authorDecision"].(string); strings.TrimSpace(decision) == "" {
			t.Fatalf("%s 的最新状态缺少作者决定记录", item.name)
		}
	}
	if _, err := fixture.w.doctor(fixture.id); err != nil {
		t.Fatalf("结构化状态完成后健康检查失败：%v", err)
	}
}
