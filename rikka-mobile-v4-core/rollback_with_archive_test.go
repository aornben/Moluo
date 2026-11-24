package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func chapterCardFact(chapterID, body string) map[string]any {
	return map[string]any{"chapterId": chapterID, "facts": []any{
		map[string]any{"id": "fact_key", "sourceChapterId": chapterID, "certainty": "certain", "evidence": body},
	}}
}

func addArchivedLifecycleRecords(card map[string]any, chapterID, evidence string) {
	card["constraints"] = []any{map[string]any{
		"id": "constraint_ch1", "kind": "amount", "subject": "虚构费用", "value": 100.0,
		"currency": "CNY", "sourceChapterId": chapterID, "evidence": evidence,
	}}
	card["timeline"] = []any{map[string]any{
		"id": "timeline_ch1", "sourceChapterId": chapterID, "evidence": evidence, "timeStatus": "unknown",
	}}
}

// TestReplaceAndRollbackWithMemoryArchive checks that a chapter replacement or
// rollback is allowed only when the immutable archive evidence still exists in
// the restored body, and that archived records survive the memory rebuild.
func TestReplaceAndRollbackWithMemoryArchive(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	revision := 1
	for n := 1; n <= 51; n++ {
		cid := fmt.Sprintf("ch_%d", n)
		body := fmt.Sprintf("第%d章虚构正文。", n)
		input := testStage(t, w, id, fmt.Sprintf("%032x", 0xf000+n), cid, body)
		card := chapterCardFact(cid, body)
		if n == 1 {
			addArchivedLifecycleRecords(card, cid, body)
		}
		writeCard(t, w, input, card)
		if _, err := w.appendFromFile(id, input, revision); err != nil {
			t.Fatalf("第%d章写入失败：%v", n, err)
		}
		revision++
	}
	root, head, manifest, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.MemoryArchives) == 0 {
		t.Fatalf("未形成真实记忆归档")
	}
	var archived []any
	if err := loadJSON(root, manifest.MemoryArchives[0]["path"].(string), &archived); err != nil {
		t.Fatal(err)
	}
	if len(archived) == 0 {
		t.Fatal("归档为空")
	}
	var memoryBefore map[string]any
	if err := loadJSON(root, manifest.MemoryPath, &memoryBefore); err != nil {
		t.Fatal(err)
	}
	archivesBefore := memoryBefore["archives"]
	summariesBefore := memoryBefore["summaries"]
	manifestArchivesBefore := manifest.MemoryArchives
	archiveHashesBefore := map[string]string{}
	for _, descriptor := range manifest.MemoryArchives {
		path := descriptor["path"].(string)
		archiveHashesBefore[path] = descriptor["sha256"].(string)
	}
	assertArchivedLifecycleState(t, w, id, archivesBefore, summariesBefore, manifestArchivesBefore, archiveHashesBefore)

	// A replacement that keeps every archived quote must succeed.
	replaceBody := "第一章改写，但仍保留第1章虚构正文。"
	replacement := testStage(t, w, id, fmt.Sprintf("%032x", 0xf100), "ch_1", replaceBody)
	replacementCard := chapterCardFact("ch_1", replaceBody)
	addArchivedLifecycleRecords(replacementCard, "ch_1", "第1章虚构正文。")
	writeCard(t, w, replacement, replacementCard)
	markReplace(t, w, replacement, chapterSHAOf(t, w, id, "ch_1"))
	if _, err := w.appendFromFile(id, replacement, head.Revision); err != nil {
		t.Fatalf("归档后兼容替换被拒绝：%v", err)
	}
	revision++
	assertArchivedLifecycleState(t, w, id, archivesBefore, summariesBefore, manifestArchivesBefore, archiveHashesBefore)

	// A replacement that removes an archived quote must be refused.
	badBody := "第一章改写，旧引文已被删除。"
	bad := testStage(t, w, id, fmt.Sprintf("%032x", 0xf101), "ch_1", badBody)
	badCard := chapterCardFact("ch_1", badBody)
	addArchivedLifecycleRecords(badCard, "ch_1", badBody)
	writeCard(t, w, bad, badCard)
	markReplace(t, w, bad, chapterSHAOf(t, w, id, "ch_1"))
	if _, err := w.appendFromFile(id, bad, revision); err == nil ||
		!strings.Contains(err.Error(), "归档") {
		t.Fatalf("删除归档证据的替换未被拒绝：%v", err)
	}
	_, afterRejected, _, err := w.load(id)
	if err != nil || afterRejected.Revision != revision {
		t.Fatalf("不兼容替换改变了 HEAD：%+v %v", afterRejected, err)
	}
	assertArchivedLifecycleState(t, w, id, archivesBefore, summariesBefore, manifestArchivesBefore, archiveHashesBefore)

	// Rolling the chapter back to its original revision is safe because the
	// archived quote exists in that original body.
	root, head, manifest, err = w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	target, err := w.historicalChapterAtRevision(id, "ch_1", 2)
	if err != nil {
		t.Fatalf("历史修订不可用：%v", err)
	}
	entries, err := w.entries(root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	currentSHA := ""
	for _, e := range entries {
		if e.ID == "ch_1" {
			currentSHA = e.SHA256
		}
	}
	if currentSHA == "" || target.Entry.SHA256 == "" {
		t.Fatal("章节 SHA 缺失")
	}
	candidate := chapterRollbackCandidate{
		OperationID: strings.Repeat("9", 32), ChapterID: "ch_1", Confirm: true,
		AuthorDecision: "作者确认回退第一章", TargetRevision: 2,
		TargetChapterSHA256: target.Entry.SHA256, ExpectedChapterSHA256: currentSHA,
	}
	data, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	inbox := w.inbox + string(os.PathSeparator) + id
	if err := os.MkdirAll(inbox, 0700); err != nil {
		t.Fatal(err)
	}
	path := inbox + string(os.PathSeparator) + strings.Repeat("9", 32) + ".json"
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.rollbackChapterFromFile(id, path, head.Revision); err != nil {
		t.Fatalf("归档后安全回退失败：%v", err)
	}
	assertArchivedLifecycleState(t, w, id, archivesBefore, summariesBefore, manifestArchivesBefore, archiveHashesBefore)
	chapter, err := w.readChapter(id, "ch_1", 0, 8192)
	if err != nil {
		t.Fatal(err)
	}
	page := chapter.(map[string]any)
	if !strings.Contains(page["text"].(string), "第1章虚构正文。") {
		t.Fatalf("回退后正文不是原修订：%v", page["text"])
	}
}

func assertArchivedLifecycleState(t *testing.T, w *workspace, id string, wantArchives, wantSummaries any, wantDescriptors []map[string]any, wantHashes map[string]string) {
	t.Helper()
	root, head, manifest, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	var memory map[string]any
	if err := loadJSON(root, manifest.MemoryPath, &memory); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(memory["archives"], wantArchives) || !reflect.DeepEqual(memory["summaries"], wantSummaries) ||
		!reflect.DeepEqual(manifest.MemoryArchives, wantDescriptors) {
		t.Fatalf("替换/回退改变了既有归档目录或摘要：archives=%v summaries=%v descriptors=%v", memory["archives"], memory["summaries"], manifest.MemoryArchives)
	}
	for path, want := range wantHashes {
		got, err := fileSHA(root, path)
		if err != nil || got != want {
			t.Fatalf("不可变归档发生变化：%s got=%s want=%s err=%v", path, got, want, err)
		}
	}
	for _, field := range []string{"constraints", "timeline"} {
		items, err := memoryItems(memory, field)
		if err != nil || len(items) != 0 {
			t.Fatalf("已归档 %s 重复回到活动层：%v %v", field, items, err)
		}
	}
	archiveRecords, err := w.loadArchiveRecords(root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	archived := map[string]bool{}
	for _, items := range archiveRecords {
		for _, raw := range items {
			encoded, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			archived[string(encoded)] = true
		}
	}
	history, err := memoryItems(memory, "history")
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range history {
		encoded, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		if archived[string(encoded)] {
			t.Fatalf("已归档记录重复回到 history：%s", encoded)
		}
	}
	for _, keyword := range []string{"constraint_ch1", "timeline_ch1"} {
		result, err := w.queryFiltered(id, "memory", keyword, "", 0, 50, 0, 0, 0, "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		items := result.(map[string]any)["items"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["type"] != "archivedHistory" {
			t.Fatalf("归档记录查询出现重复或来源错误：%s %v", keyword, items)
		}
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatalf("归档替换/回退后健康检查失败：%v", err)
	}
	if !w.backupExists(id, head.Revision) {
		t.Fatalf("归档替换/回退后缺少 revision %d 的独立备份", head.Revision)
	}
}

func markReplace(t *testing.T, w *workspace, path, expectedSHA string) {
	t.Helper()
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
	d.ExpectedChapterSHA256 = expectedSHA
	updated, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, updated, 0600); err != nil {
		t.Fatal(err)
	}
}

func chapterSHAOf(t *testing.T, w *workspace, workID, chapterID string) string {
	t.Helper()
	root, _, m, err := w.load(workID)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := w.entries(root, m)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.ID == chapterID {
			return e.SHA256
		}
	}
	t.Fatalf("章节不存在：%s", chapterID)
	return ""
}
