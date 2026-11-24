package main

import (
	"maps"
	"path/filepath"
	"strings"
	"testing"
)

func fictionalTimeline(id, eventID, subject, event, date, end, status, chapterID string) map[string]any {
	record := map[string]any{"id": id, "subject": subject, "event": event, "sourceChapterId": chapterID,
		"evidence": "虚构的交接原文", "timeStatus": status}
	if eventID != "" {
		record["eventId"] = eventID
	}
	if date != "" {
		record["date"] = date
	}
	if end != "" {
		record["endDate"] = end
	}
	return record
}

func TestTimelineIdentityAndPrecision(t *testing.T) {
	first := fictionalTimeline("tl_a", "handoff", "纸桥", "交出铜钥匙", "2157-04-12", "", "known", "ch_1")
	for _, test := range []struct {
		name     string
		a, b     map[string]any
		severity string
	}{
		{"precise", first, fictionalTimeline("tl_b", "handoff", "纸桥", "交出铜钥匙", "2157-05-01", "", "known", "ch_2"), "conflict"},
		{"disjoint_approximate_ranges",
			fictionalTimeline("tl_a", "handoff", "纸桥", "交出铜钥匙", "2157-04-11", "2157-04-14", "approximate", "ch_1"),
			fictionalTimeline("tl_b", "handoff", "纸桥", "交出铜钥匙", "2157-05-01", "2157-05-02", "approximate", "ch_2"), "conflict"},
		{"overlapping_range", first, fictionalTimeline("tl_b", "handoff", "纸桥", "交出铜钥匙", "2157-04-10", "2157-04-12", "approximate", "ch_2"), ""},
		{"unknown_refines", first, fictionalTimeline("tl_a", "handoff", "纸桥", "交出铜钥匙", "", "", "unknown", "ch_2"), ""},
		{"different_events_at_location", first, fictionalTimeline("tl_b", "", "纸桥", "船只经过", "2157-05-01", "", "known", "ch_2"), ""},
		{"uncertain_identity", first, fictionalTimeline("tl_b", "", "纸桥", "交出铜钥匙", "2157-05-01", "", "known", "ch_2"), "suspected"},
		{"approximate_point", first, fictionalTimeline("tl_b", "handoff", "纸桥", "交出铜钥匙", "2157-05-01", "", "approximate", "ch_2"), "suspected"},
		{"mobile_report_shape",
			fictionalTimeline("tl_ch12_01", "", "虚构人物", "交出三层钥匙", "2157-04-12", "", "approximate", "ch_12"),
			fictionalTimeline("tl_test_conflict", "", "虚构人物", "", "2157-05-01", "", "approximate", "ch_13"), "suspected"},
	} {
		t.Run(test.name, func(t *testing.T) {
			found := timelineFindings(nil, timelineRecords([]any{test.a, test.b}, false), nil)
			if test.severity == "" && len(found) == 0 {
				return
			}
			if len(found) != 1 || found[0].Severity != test.severity || found[0].First.Source == "" ||
				found[0].Second.Evidence == "" || found[0].Basis == "" {
				t.Fatal(found)
			}
		})
	}
}

func stageTimeline(t *testing.T, w *workspace, work, op, chapterID string, records ...map[string]any) string {
	t.Helper()
	path := testStage(t, w, work, op, chapterID, "虚构的交接原文。")
	card := map[string]any{"chapterId": chapterID, "timeline": records}
	if err := atomicJSON(filepath.Dir(path), filepath.Base(path), draft{OperationID: op,
		Chapter: chapter{ID: chapterID, Title: "虚构章节", Content: "虚构的交接原文。"}, Card: card}); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTimelineWriteGateAndBoundAuthorRuling(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	first := fictionalTimeline("tl_a", "handoff", "纸桥", "交出铜钥匙", "2157-04-12", "", "known", "ch_1")
	firstPath := stageTimeline(t, w, id, strings.Repeat("1", 32), "ch_1", first)
	if _, err := w.appendFromFile(id, firstPath, 1); err != nil {
		t.Fatal(err)
	}
	second := fictionalTimeline("tl_b", "handoff", "纸桥", "交出铜钥匙", "2157-05-01", "", "known", "ch_2")
	path := stageTimeline(t, w, id, strings.Repeat("2", 32), "ch_2", second)
	root, _, _, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	before := oldV4FileHashes(t, root)
	if _, err := w.appendFromFile(id, path, 2); err == nil || !strings.Contains(err.Error(), "tl_a") ||
		!strings.Contains(err.Error(), "2157-05-01") || !strings.Contains(err.Error(), "虚构的交接原文") {
		t.Fatal(err)
	}
	if !maps.Equal(before, oldV4FileHashes(t, root)) {
		t.Fatal("拒绝后工作文件或 revision 改变")
	}
	findings := timelineFindings(nil, timelineRecords([]any{first, second}, false), nil)
	if len(findings) != 1 {
		t.Fatal(findings)
	}
	f := findings[0]
	fixture := stateTransitionFixture{w: w, id: id, revision: 2}
	audit := map[string]any{"id": "audit_tl", "kind": "audit", "name": "虚构时间线审计", "status": "candidate",
		"auditType": "timeline", "result": "potential_conflict", "uncertainty": "low", "claim": "作者待裁定交接时间",
		"sourceChapterId": "ch_1", "evidence": "虚构的交接原文",
		"keepTimelineSha256": f.Second.ContentSHA256, "otherTimelineSha256": f.First.ContentSHA256}
	if _, err := semanticEntity(t, fixture, strings.Repeat("3", 32), audit); err != nil {
		t.Fatal(err)
	}
	fixture.revision++
	decision := bindToTarget(t, fixture, map[string]any{"id": "decision_tl", "kind": "audit-decision", "name": "虚构决定",
		"status": "candidate", "targetId": "audit_tl", "decision": "confirmed", "authorDecision": "采用五月交接时间"}, "audit_tl")
	if _, err := semanticEntity(t, fixture, strings.Repeat("4", 32), decision); err == nil {
		t.Fatal("无确认的决定通过")
	}
	if _, err := w.appendFromFile(id, path, 3); err == nil {
		t.Fatal("只有候选也绕过写入门禁")
	}
	if _, err := semanticEntityConfirmed(t, fixture, strings.Repeat("5", 32), decision); err != nil {
		t.Fatal(err)
	}
	if _, err := w.appendFromFile(id, path, 4); err != nil {
		t.Fatal(err)
	}
	out, err := w.doctor(id)
	if err != nil {
		t.Fatal(err)
	}
	found := out.(map[string]any)["timelineConflicts"].([]timelineConflict)
	if len(found) != 1 || found[0].Chosen != f.Second.ContentSHA256 || found[0].AuditID != "audit_tl" {
		t.Fatal(found)
	}
	third := fictionalTimeline("tl_c", "arrival", "纸桥", "乘船到达", "2157-06-01", "", "known", "ch_3")
	person := fictionalTimeline("tl_d", "person_depart", "虚构人物", "离港", "2157-06-02", "", "known", "ch_3")
	more := stageTimeline(t, w, id, strings.Repeat("6", 32), "ch_3", third, person)
	if _, err := w.appendFromFile(id, more, 5); err != nil {
		t.Fatal(err)
	}
	known := fictionalTimeline("tl_e", "person_arrive", "虚构人物", "入港", "2157-06-03", "", "known", "ch_4")
	overlap := fictionalTimeline("tl_f", "handoff", "纸桥", "交出铜钥匙", "2157-04-11", "2157-05-02", "approximate", "ch_4")
	unknown := fictionalTimeline("tl_g", "sighting", "虚构人物", "观星", "", "", "unknown", "ch_4")
	more = stageTimeline(t, w, id, strings.Repeat("7", 32), "ch_4", known, overlap, unknown)
	if _, err := w.appendFromFile(id, more, 6); err != nil {
		t.Fatal(err)
	}
	refined := fictionalTimeline("tl_g", "sighting", "虚构人物", "观星", "2157-06-06", "", "known", "ch_5")
	more = stageTimeline(t, w, id, strings.Repeat("8", 32), "ch_5", refined)
	if _, err := w.appendFromFile(id, more, 7); err != nil {
		t.Fatal(err)
	}
}

func TestTimelineDoctorAcrossArchivesAndActive(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	for n, cid := range []string{"ch_1", "ch_2", "ch_3"} {
		path := testStage(t, w, id, strings.Repeat(string(rune('a'+n)), 32), cid, "虚构的交接原文。")
		if _, err := w.appendFromFile(id, path, n+1); err != nil {
			t.Fatal(err)
		}
	}
	root, h, m, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	var memory map[string]any
	if err := loadJSON(root, m.MemoryPath, &memory); err != nil {
		t.Fatal(err)
	}
	memory["timeline"] = []any{fictionalTimeline("tl_active", "handoff", "纸桥", "交钥匙", "2157-05-01", "", "known", "ch_3")}
	archives := []any{}
	for _, old := range []struct{ id, chapter, date string }{{"tl_archive_a", "ch_1", "2157-03-01"}, {"tl_archive_b", "ch_2", "2157-04-01"}} {
		path := "memory/archives/" + old.id + ".json"
		items := []any{map[string]any{"type": "timeline", "value": fictionalTimeline(old.id, "handoff", "纸桥", "交钥匙", old.date, "", "known", old.chapter)}}
		if err := newJSON(root, path, items); err != nil {
			t.Fatal(err)
		}
		hash, err := fileSHA(root, path)
		if err != nil {
			t.Fatal(err)
		}
		desc := map[string]any{"path": path, "sha256": hash, "count": 1}
		m.MemoryArchives = append(m.MemoryArchives, desc)
		archives = append(archives, desc)
	}
	memory["summaries"] = archives
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
	if _, err := w.readChapter(id, "ch_2", 0, 1024); err != nil {
		t.Fatal(err)
	}
	out, err := w.doctorCore(id, false)
	if err != nil {
		t.Fatal(err)
	}
	findings := out.(map[string]any)["timelineConflicts"].([]timelineConflict)
	if len(findings) != 3 {
		t.Fatal(findings)
	}
	for _, f := range findings {
		if f.Severity != "conflict" {
			t.Fatal(f)
		}
	}
	incoming := fictionalTimeline("tl_later", "handoff", "纸桥", "交钥匙", "2157-05-01", "", "known", "ch_4")
	if _, err := w.checkIncomingTimeline(root, m, memory, map[string]any{"timeline": []any{incoming}}); err == nil ||
		!strings.Contains(err.Error(), "tl_archive_a") {
		t.Fatal(err)
	}
}
