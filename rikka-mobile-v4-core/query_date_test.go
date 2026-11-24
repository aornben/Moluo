package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

type dateQueryFixture struct {
	w  *workspace
	id string
}

func appendDateChapter(t *testing.T, w *workspace, id, op, chapterID, quote, date string, revision int, final bool) {
	t.Helper()
	path := testStage(t, w, id, op, chapterID, quote)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var candidate draft
	if err := json.Unmarshal(data, &candidate); err != nil {
		t.Fatal(err)
	}
	candidate.Card = map[string]any{"chapterId": chapterID, "timeline": []any{map[string]any{
		"id": "event_shared", "sourceChapterId": chapterID, "timeStatus": "known",
		"startDate": date, "evidence": quote,
	}}}
	if final {
		candidate.Card["facts"] = []any{
			map[string]any{"id": "fact_dated", "sourceChapterId": chapterID, "certainty": "certain", "date": "2026-06-15", "text": "dated fact", "evidence": quote},
			map[string]any{"id": "fact_undated", "sourceChapterId": chapterID, "certainty": "certain", "text": "undated fact", "evidence": quote},
		}
		candidate.Card["constraints"] = []any{map[string]any{
			"id": "constraint_deadline", "sourceChapterId": chapterID, "dueDate": "2027-01-02",
			"kind": "deadline", "subject": "虚构缴费", "status": "open", "text": "deadline task", "evidence": quote,
		}}
	}
	encoded, _ := json.Marshal(candidate)
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.appendFromFile(id, path, revision); err != nil {
		t.Fatal(err)
	}
}

func newDateQueryFixture(t *testing.T) dateQueryFixture {
	t.Helper()
	w := testWorkspace(t)
	id := testInit(t, w)
	appendDateChapter(t, w, id, strings.Repeat("1", 32), "ch_1", "第一章日期证据。", "2025-12-31", 1, false)
	appendDateChapter(t, w, id, strings.Repeat("2", 32), "ch_2", "第二章日期证据。", "2026-01-01", 2, false)
	appendDateChapter(t, w, id, strings.Repeat("3", 32), "ch_3", "第三章日期证据。", "2026-12-31", 3, true)

	root, h, m, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	var memory map[string]any
	if err := loadJSON(root, m.MemoryPath, &memory); err != nil {
		t.Fatal(err)
	}
	entries, err := w.entries(root, m)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the real archive writer while keeping the test fixture small.
	// A logical chapter 51 cutoff archives only the chapter-1 history record;
	// the chapter-2 history record remains in the active memory file.
	if err := archiveHistory(root, &m, memory, entries, 51, "date-query"); err != nil {
		t.Fatal(err)
	}
	if len(m.MemoryArchives) != 1 {
		t.Fatalf("日期测试历史未归档：%v", m.MemoryArchives)
	}
	if err := atomicJSON(root, m.MemoryPath, memory); err != nil {
		t.Fatal(err)
	}
	m.ContentHashes[m.MemoryPath], err = fileSHA(root, m.MemoryPath)
	if err != nil {
		t.Fatal(err)
	}
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
	return dateQueryFixture{w: w, id: id}
}

func dateQuery(t *testing.T, fixture dateQueryFixture, cursor, limit int, keyword, status, from, to string) map[string]any {
	t.Helper()
	result, err := fixture.w.queryFiltered(fixture.id, "memory", keyword, "", cursor, limit, 0, 0, 0, status, from, to)
	if err != nil {
		t.Fatal(err)
	}
	return result.(map[string]any)
}

func TestMemoryDateRangeActiveHistoryArchiveAndPagination(t *testing.T) {
	fixture := newDateQueryFixture(t)
	archived := dateQuery(t, fixture, 0, 20, "", "", "2025-12-31", "2025-12-31")
	if archived["total"] != 1 || archived["archivesRead"] != 1 {
		t.Fatalf("归档日期包含边界错误：%v", archived)
	}
	item := archived["items"].([]any)[0].(map[string]any)
	if item["type"] != "archivedHistory" || queryMemoryValue(item)["startDate"] != "2025-12-31" {
		t.Fatalf("归档日期记录来源错误：%v", item)
	}

	crossYear := dateQuery(t, fixture, 0, 20, "", "", "2026-01-01", "2027-01-02")
	if crossYear["total"] != 4 {
		t.Fatalf("跨年、历史、活动记录或包含边界错误：%v", crossYear)
	}
	encoded, _ := json.Marshal(crossYear["items"])
	if strings.Contains(string(encoded), "undated fact") {
		t.Fatal("无日期记录被猜测进日期范围")
	}

	deadline := dateQuery(t, fixture, 0, 20, "deadline", "open", "2027-01-02", "2027-01-02")
	if deadline["total"] != 1 || queryMemoryValue(deadline["items"].([]any)[0].(map[string]any))["dueDate"] != "2027-01-02" {
		t.Fatalf("日期 + 关键词 + 状态组合筛选错误：%v", deadline)
	}
	if got := dateQuery(t, fixture, 0, 20, "", "", "2026-12-31", "")["total"]; got != 2 {
		t.Fatalf("仅起始日期过滤错误：%v", got)
	}
	if got := dateQuery(t, fixture, 0, 20, "", "", "", "2026-01-01")["total"]; got != 2 {
		t.Fatalf("仅结束日期过滤错误：%v", got)
	}

	first := dateQuery(t, fixture, 0, 2, "", "", "2026-01-01", "2027-01-02")
	second := dateQuery(t, fixture, intValue(first["nextCursor"]), 2, "", "", "2026-01-01", "2027-01-02")
	if first["complete"] != false || second["complete"] != true ||
		len(first["items"].([]any))+len(second["items"].([]any)) != 4 {
		t.Fatalf("日期分页不完整或游标不一致：first=%v second=%v", first, second)
	}
	seen := map[string]bool{}
	for _, page := range []map[string]any{first, second} {
		for _, raw := range page["items"].([]any) {
			data, _ := json.Marshal(raw)
			key := string(data)
			if seen[key] {
				t.Fatal("日期分页出现重复记录")
			}
			seen[key] = true
		}
	}
	reopened, err := openWorkspace(fixture.w.root)
	if err != nil {
		t.Fatal(err)
	}
	fixture.w = reopened
	if after := dateQuery(t, fixture, 0, 20, "", "", "2026-01-01", "2027-01-02"); !reflect.DeepEqual(crossYear["items"], after["items"]) || crossYear["total"] != after["total"] {
		t.Fatal("重新打开工作区后日期查询结果变化")
	}
}

func TestMemoryDateRangeRejectsInvalidInputAndCorruption(t *testing.T) {
	fixture := newDateQueryFixture(t)
	for _, tc := range []struct{ from, to, want string }{
		{"2026-02-30", "", "YYYY-MM-DD"},
		{"2027-01-02", "2026-01-01", "起始日期不能晚于结束日期"},
	} {
		if _, err := fixture.w.queryFiltered(fixture.id, "memory", "", "", 0, 20, 0, 0, 0, "", tc.from, tc.to); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("非法日期输入未拒绝：%v", err)
		}
	}

	t.Run("memory-index-hash", func(t *testing.T) {
		fixture := newDateQueryFixture(t)
		root, _, m, _ := fixture.w.load(fixture.id)
		var memory map[string]any
		_ = loadJSON(root, m.MemoryPath, &memory)
		memory["coverage"] = "tampered"
		_ = atomicJSON(root, m.MemoryPath, memory)
		if _, err := fixture.w.queryFiltered(fixture.id, "memory", "", "", 0, 20, 0, 0, 0, "", "2026-01-01", "2026-12-31"); err == nil || !strings.Contains(err.Error(), "记忆哈希") {
			t.Fatalf("记忆哈希篡改未拒绝：%v", err)
		}
	})

	t.Run("archive-hash", func(t *testing.T) {
		fixture := newDateQueryFixture(t)
		root, _, m, _ := fixture.w.load(fixture.id)
		path := m.MemoryArchives[0]["path"].(string)
		_ = atomicJSON(root, path, []any{})
		if _, err := fixture.w.queryFiltered(fixture.id, "memory", "", "", 0, 20, 0, 0, 0, "", "2025-12-31", "2025-12-31"); err == nil || !strings.Contains(err.Error(), "归档哈希") {
			t.Fatalf("历史归档篡改未拒绝：%v", err)
		}
	})

	t.Run("resealed-invalid-record-date", func(t *testing.T) {
		fixture := newDateQueryFixture(t)
		root, h, m, _ := fixture.w.load(fixture.id)
		var memory map[string]any
		_ = loadJSON(root, m.MemoryPath, &memory)
		memory["activeFacts"].([]any)[0].(map[string]any)["date"] = "2026-02-30"
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
		if _, err := fixture.w.queryFiltered(fixture.id, "memory", "", "", 0, 20, 0, 0, 0, "", "2026-01-01", "2026-12-31"); err == nil || !strings.Contains(err.Error(), "日期字段") {
			t.Fatalf("重封哈希后的非法记录日期未拒绝：%v", err)
		}
	})
}
