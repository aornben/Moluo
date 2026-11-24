package main

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func lifecycleEntry(id string, number int) entry {
	return entry{ID: id, Number: number, Title: id, Path: "chapters/" + id + ".json", SHA256: ""}
}

func lifecycleFact(id, chapter string) map[string]any {
	return map[string]any{"id": id, "sourceChapterId": chapter, "certainty": "certain", "evidence": "证据" + chapter, "text": "事实" + id}
}

func TestArchiveAllActiveCategoriesKeepsOpenItems(t *testing.T) {
	w := testWorkspace(t)
	m := manifest{}
	memory := emptyMemory(1)
	memory["activeFacts"] = []any{lifecycleFact("fact_old", "ch_1"), lifecycleFact("fact_new", "ch_55")}
	memory["knowledge"] = []any{
		map[string]any{"id": "k_old", "characterId": "char_a", "factId": "fact_old", "learnedChapterId": "ch_2", "evidence": "证据ch_2"},
		map[string]any{"id": "k_new", "characterId": "char_a", "factId": "fact_new", "learnedChapterId": "ch_54", "evidence": "证据ch_54"},
	}
	memory["constraints"] = []any{map[string]any{"id": "c_old", "sourceChapterId": "ch_3", "evidence": "证据ch_3"}}
	memory["timeline"] = []any{map[string]any{"id": "t_new", "sourceChapterId": "ch_53", "evidence": "证据ch_53"}}
	memory["openHooks"] = []any{map[string]any{"id": "h_open", "openedChapterId": "ch_1", "status": "open", "evidence": "证据ch_1"}}
	memory["openQuestions"] = []any{map[string]any{"id": "q_open", "sourceChapterId": "ch_2", "evidence": "证据ch_2"}}
	memory["history"] = []any{map[string]any{"type": "activeFacts", "value": lifecycleFact("fact_superseded", "ch_3")}}

	chapters := make([]entry, 60)
	for i := range chapters {
		chapters[i] = lifecycleEntry(fmt.Sprintf("ch_%d", i+1), i+1)
	}
	if err := archiveHistory(w.root, &m, memory, chapters, 56, "lifecycle"); err != nil {
		t.Fatal(err)
	}
	if len(m.MemoryArchives) != 1 {
		t.Fatalf("归档描述数量错误：%v", m.MemoryArchives)
	}
	descriptor := m.MemoryArchives[0]
	if descriptor["fromChapter"] != 3 || descriptor["throughChapter"] != 3 {
		t.Fatalf("归档章节范围错误：%v", descriptor)
	}
	var archived []any
	if err := loadJSON(w.root, descriptor["path"].(string), &archived); err != nil {
		t.Fatal(err)
	}
	// ch_3 constraint plus the superseded ch_3 history record.
	if len(archived) != 2 {
		t.Fatalf("归档条目数量错误：%d %v", len(archived), archived)
	}
	types := map[string]int{}
	for _, raw := range archived {
		record := raw.(map[string]any)
		types[record["type"].(string)]++
	}
	if types["constraints"] != 1 || types["activeFacts"] != 1 {
		t.Fatalf("归档类别错误：%v", types)
	}

	activeFacts := memory["activeFacts"].([]any)
	if len(activeFacts) != 2 {
		t.Fatalf("当前事实不应被历史窗口归档：%v", activeFacts)
	}
	if len(memory["knowledge"].([]any)) != 2 {
		t.Fatal("当前认知不应被历史窗口归档")
	}
	if len(memory["constraints"].([]any)) != 0 || len(memory["timeline"].([]any)) != 1 {
		t.Fatal("历史事件类条目未按边界缩减")
	}
	if len(memory["history"].([]any)) != 0 {
		t.Fatal("已归档的历史条目仍留在活动历史层")
	}
	if len(memory["summaries"].([]any)) != 1 {
		t.Fatal("归档摘要未写入记忆")
	}
}

func TestArchivedActiveMemoryIsQueryableByChapter(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	first := testStage(t, w, id, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "ch_1", "阿甲在旧桥捡到钥匙。")
	writeCard(t, w, first, map[string]any{"chapterId": "ch_1", "facts": []any{map[string]any{"id": "fact_key", "sourceChapterId": "ch_1", "certainty": "certain", "evidence": "阿甲在旧桥捡到钥匙。"}}})
	if _, err := w.appendFromFile(id, first, 1); err != nil {
		t.Fatal(err)
	}
	root, h, m, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	var memory map[string]any
	if err := loadJSON(root, m.MemoryPath, &memory); err != nil {
		t.Fatal(err)
	}
	memory["constraints"] = []any{map[string]any{"id": "constraint_key", "sourceChapterId": "ch_1", "evidence": "阿甲在旧桥捡到钥匙。"}}
	chapters, err := w.entries(root, m)
	if err != nil {
		t.Fatal(err)
	}
	if err := archiveHistory(root, &m, memory, chapters, 56, "query"); err != nil {
		t.Fatal(err)
	}
	if len(m.MemoryArchives) != 1 {
		t.Fatalf("未生成归档：%v", m.MemoryArchives)
	}
	if err := atomicJSON(root, m.MemoryPath, memory); err != nil {
		t.Fatal(err)
	}
	m.ContentHashes[m.MemoryPath], _ = fileSHA(root, m.MemoryPath)
	if err := atomicJSON(root, h.Manifest, m); err != nil {
		t.Fatal(err)
	}
	h.ManifestSHA256, _ = fileSHA(root, h.Manifest)
	if err := atomicJSON(root, "HEAD.json", h); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctorCore(id, false); err != nil {
		t.Fatalf("归档后完整性检查失败：%v", err)
	}
	result, err := w.queryFiltered(id, "memory", "constraint_key", "", 0, 50, 0, 1, 1, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	page := result.(map[string]any)
	items := page["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("按章节检索归档约束失败：%v", items)
	}
	record := items[0].(map[string]any)
	if record["type"] != "archivedHistory" {
		t.Fatalf("归档事实来源标记错误：%v", record["type"])
	}
	inner := record["value"].(map[string]any)
	if inner["type"] != "constraints" {
		t.Fatalf("归档事实类别错误：%v", inner["type"])
	}
}

func writeCard(t *testing.T, w *workspace, path string, card map[string]any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var d draft
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	d.Card = card
	updated, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, updated, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestReplacementRejectsStaleEvidenceAndRebuildsMemory(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	op := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	first := testStage(t, w, id, op, "ch_1", "阿甲在旧桥捡到钥匙。")
	writeCard(t, w, first, map[string]any{"chapterId": "ch_1", "facts": []any{map[string]any{"id": "fact_key", "sourceChapterId": "ch_1", "certainty": "certain", "evidence": "阿甲在旧桥捡到钥匙。"}}})
	if _, err := w.appendFromFile(id, first, 1); err != nil {
		t.Fatal(err)
	}
	root, _, m, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	chapters, err := w.entries(root, m)
	if err != nil || len(chapters) != 1 {
		t.Fatalf("章节目录错误：%v", err)
	}
	oldSHA := chapters[0].SHA256

	replace := func(op, content, evidence string) (string, map[string]any) {
		path := testStage(t, w, id, op, "ch_1", content)
		writeCard(t, w, path, map[string]any{"chapterId": "ch_1", "facts": []any{map[string]any{"id": "fact_key", "sourceChapterId": "ch_1", "certainty": "certain", "evidence": evidence}}})
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var d draft
		if err := json.Unmarshal(data, &d); err != nil {
			t.Fatal(err)
		}
		d.Mode = "replace"
		d.Confirm = true
		d.ExpectedChapterSHA256 = oldSHA
		updated, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, updated, 0600); err != nil {
			t.Fatal(err)
		}
		return path, nil
	}

	// The quote no longer exists in the new body: the replacement must be
	// rejected instead of leaving memory pointing at vanished prose.
	stale, _ := replace("cccccccccccccccccccccccccccccccc", "阿甲把钥匙交给了阿乙。", "阿甲在旧桥捡到钥匙。")
	if _, err := w.appendFromFile(id, stale, 2); err == nil {
		t.Fatal("失效证据的章节替换未被拒绝")
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatalf("被拒绝的替换破坏了作品：%v", err)
	}

	// A replacement with matching evidence rebuilds memory without stale data.
	fresh, _ := replace("dddddddddddddddddddddddddddddddd", "阿甲在新桥埋下钥匙。", "阿甲在新桥埋下钥匙。")
	if _, err := w.appendFromFile(id, fresh, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatalf("替换后健康检查失败：%v", err)
	}
	root, _, m, err = w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	var memory map[string]any
	if err := loadJSON(root, m.MemoryPath, &memory); err != nil {
		t.Fatal(err)
	}
	facts := memory["activeFacts"].([]any)
	if len(facts) != 1 {
		t.Fatalf("替换后活动事实数量错误：%d", len(facts))
	}
	if facts[0].(map[string]any)["evidence"] != "阿甲在新桥埋下钥匙。" {
		t.Fatalf("替换后事实证据未更新：%v", facts[0])
	}
}
