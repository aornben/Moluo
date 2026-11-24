package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func oldV4PhoneFixture(t *testing.T, w *workspace, count int) (string, head, manifest) {
	t.Helper()
	source := filepath.Join(w.root, "moluo-v4")
	h := head{Format: formatName, SchemaVersion: 4, Revision: 1, Manifest: "manifests/r000001.json"}
	m := manifest{Format: formatName, SchemaVersion: 4, Revision: 1, Meta: map[string]any{"title": "隔离旧作"},
		CharactersPath: "characters/r000001.json", TasksPath: "tasks/r000001.json", MemoryPath: "memory/r000001.json",
		MemoryRevision: 1, ChapterCount: count}
	write := func(path string, value any) {
		t.Helper()
		if err := newJSON(source, path, value); err != nil {
			t.Fatal(err)
		}
	}
	write(m.CharactersPath, []any{map[string]any{"id": "char_a", "name": "虚构人物", "extra": map[string]any{"preserved": true}}})
	write(m.TasksPath, []any{map[string]any{"id": "task_a", "title": "虚构待办", "status": "done",
		"plannedChapter": 1, "priority": "high", "characterIds": []string{"char_a"},
		"completedChapterId": "ch_1", "completionEvidence": "虚构原文"}})
	write(m.MemoryPath, map[string]any{"constraints": nil, "sourceRevision": 1,
		"activeFacts": []any{map[string]any{"id": "fact_a", "sourceChapterId": "ch_1", "evidence": "虚构原文", "value": "虚构事实", "certainty": "certain"}},
		"timeline":    []any{map[string]any{"id": "tl_a", "sourceChapterId": "ch_1", "evidence": "虚构原文", "date": "2157-04-12"}}})
	batch := []map[string]any{}
	for i := 1; i <= count; i++ {
		id := fmt.Sprintf("ch_%d", i)
		path := fmt.Sprintf("chapters/%s-r000001.json", id)
		title := fmt.Sprintf("虚构第%d章", i)
		write(path, map[string]any{"id": id, "title": title, "content": "虚构原文" + strings.Repeat("纸", 3500),
			"customNote": map[string]any{"ordered": []any{"保留", nil}}})
		batch = append(batch, map[string]any{"id": id, "path": path, "number": i, "title": title, "cardPath": nil})
		if i%50 == 0 || i == count {
			p := page{Start: i - len(batch) + 1, End: i, Path: fmt.Sprintf("pages/p%04d-r000001.json", len(m.Pages)+1)}
			m.Pages = append(m.Pages, p)
			write(p.Path, batch)
			batch = nil
		}
	}
	write(h.Manifest, m)
	write("HEAD.json", h)
	return source, h, m
}

func oldV4FileHashes(t *testing.T, root string) map[string]string {
	t.Helper()
	hashes := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		hashes[rel], err = fileSHA(root, rel)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return hashes
}

func TestOldV4PhoneFormatMillionHanAndIsolation(t *testing.T) {
	w := testWorkspace(t)
	other := testInit(t, w)
	otherRoot, _, _, err := w.load(other)
	if err != nil {
		t.Fatal(err)
	}
	otherBefore := oldV4FileHashes(t, otherRoot)
	source, _, _ := oldV4PhoneFixture(t, w, 300)
	before := oldV4FileHashes(t, source)
	started := time.Now()
	out, err := w.importOldV4()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("300 章旧 v4 手机格式迁移耗时 %v", time.Since(started))
	result := out.(map[string]any)
	id := result["workId"].(string)
	if id == other || result["chaptersWithoutEvidenceCards"] != 300 || len(result["missingMemoryFields"].([]string)) == 0 {
		t.Fatal(result)
	}
	root, _, m, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	all, err := w.entries(root, m)
	if err != nil || len(all) != 300 {
		t.Fatal(len(all), err)
	}
	han := 0
	for _, entry := range all {
		ch, err := loadChapter(root, entry.Path)
		if err != nil || entry.SHA256 != before[entry.Path] {
			t.Fatal(entry.ID, err)
		}
		han += countHanzi(ch.Content)
	}
	if han != 1051200 {
		t.Fatal(han)
	}
	t.Logf("逐章磁盘回读 %d Han", han)
	for _, path := range []string{m.CharactersPath, m.TasksPath} {
		hash, err := fileSHA(root, path)
		// Character bytes are copied; tasks are reserialized with stable keys.
		if err != nil || hash != before[path] {
			t.Fatal(path, err)
		}
	}
	var memory map[string]any
	if err := loadJSON(root, m.MemoryPath, &memory); err != nil {
		t.Fatal(err)
	}
	for field, id := range map[string]string{"activeFacts": "fact_a", "timeline": "tl_a"} {
		items := memory[field].([]any)
		if len(items) != 1 || items[0].(map[string]any)["id"] != id {
			t.Fatal(field, items)
		}
	}
	if memory["coverage"] != "partial" || len(memory["chapterCards"].([]any)) != 0 {
		t.Fatal(memory)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
	repeat, err := w.importOldV4()
	if err != nil || repeat.(map[string]any)["workId"] != id || repeat.(map[string]any)["idempotent"] != true {
		t.Fatal(repeat, err)
	}
	if !maps.Equal(before, oldV4FileHashes(t, source)) || !maps.Equal(otherBefore, oldV4FileHashes(t, otherRoot)) {
		t.Fatal("旧源或其他作品改变")
	}
}

func TestOldV4PageTypeAndAtomicFailure(t *testing.T) {
	for _, test := range []struct{ name, value, want string }{
		{"object", `{"entries":[]}`, "实际类型 object"},
		{"nested_array", `[[]]`, "$[0]：实际类型 array"},
		{"null_record", `[null]`, "$[0]：实际类型 null"},
		{"broken", `[{`, "章节页 JSON 无效"},
		{"invalid_chapter_reference", `[{"id":"other","number":1,"title":"虚构第1章","path":"chapters/ch_1-r000001.json"}]`, "未发布作品"},
		{"blocked_backup", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := testWorkspace(t)
			source, _, m := oldV4PhoneFixture(t, w, 1)
			if test.name == "blocked_backup" {
				if err := os.WriteFile(w.backups, []byte("虚构的路径冲突"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if err := atomicFile(source, m.Pages[0].Path, []byte(test.value)); err != nil {
				t.Fatal(err)
			}
			if test.name == "object" {
				if _, err := w.entries(source, m); err == nil || !strings.Contains(err.Error(), "[]main.entry") {
					t.Fatalf("4.9.2 原解析路径未复现预期错误：%v", err)
				} else {
					t.Log(err)
				}
			}
			before := oldV4FileHashes(t, source)
			if _, err := w.importOldV4(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("导入错误 %v，应包含 %q", err, test.want)
			}
			found, err := os.ReadDir(w.works)
			if err != nil && !os.IsNotExist(err) || len(found) != 0 {
				t.Fatal(found, err)
			}
			if !maps.Equal(before, oldV4FileHashes(t, source)) {
				t.Fatal("失败后旧源改变")
			}
		})
	}
}

func TestOldV4ReferenceTransactionHistoryAndPending(t *testing.T) {
	for _, pending := range []bool{false, true} {
		w := testWorkspace(t)
		source, h, m := oldV4PhoneFixture(t, w, 1)
		rel := "transactions/r000002.json"
		record := map[string]any{"baseRevision": 1, "targetRevision": 2,
			"baseManifest": h.Manifest, "targetManifest": "manifests/r000002.json",
			"planned": []any{"manifests/r000002.json"}, "operation": "append_chapter"}
		if err := newJSON(source, rel, record); err != nil {
			t.Fatal(err)
		}
		if !pending {
			h.Revision, m.Revision = 2, 2
			h.Manifest = "manifests/r000002.json"
			m.MemoryRevision = 2
			var memory map[string]any
			if err := loadJSON(source, m.MemoryPath, &memory); err != nil {
				t.Fatal(err)
			}
			memory["sourceRevision"] = 2
			for path, value := range map[string]any{"HEAD.json": h, h.Manifest: m, m.MemoryPath: memory} {
				if err := atomicJSON(source, path, value); err != nil {
					t.Fatal(err)
				}
			}
		}
		before := oldV4FileHashes(t, source)
		out, err := w.importOldV4()
		if pending {
			if err == nil || !strings.Contains(err.Error(), "未完成事务") || !maps.Equal(before, oldV4FileHashes(t, source)) {
				t.Fatal(out, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		root, _, _, err := w.load(out.(map[string]any)["workId"].(string))
		if err != nil {
			t.Fatal(err)
		}
		hash, err := fileSHA(root, "legacy-v4-transactions/r000002.json")
		if err != nil || hash != before[rel] || !maps.Equal(before, oldV4FileHashes(t, source)) {
			t.Fatal(hash, err)
		}
	}
}
