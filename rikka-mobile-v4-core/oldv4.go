package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// importOldV4 copies the fixed prior Skill workspace into a new work ID.
// Historical archives are converted only in the private copy.
func (w *workspace) importOldV4() (any, error) {
	source := filepath.Join(w.root, "moluo-v4")
	if err := rejectSymlinks(source); err != nil {
		return nil, err
	}
	info, err := os.Stat(source)
	if err != nil || !info.IsDir() {
		return nil, errors.New("旧 v4 目录不存在")
	}
	headBytes, err := readFile(source, "HEAD.json")
	if err != nil {
		return nil, err
	}
	sourceHash := sumBytes(headBytes)
	var h head
	if err := decodeJSON(headBytes, &h); err != nil {
		return nil, err
	}
	if h.Format != formatName || h.SchemaVersion != 4 || h.Revision < 1 || h.WorkID != "" {
		return nil, errors.New("源目录不是无 workId 的旧 v4")
	}
	if h.ManifestSHA256 != "" {
		actual, err := fileSHA(source, h.Manifest)
		if err != nil || actual != h.ManifestSHA256 {
			return nil, errors.New("旧 v4 manifest 哈希与 HEAD 不一致")
		}
	}
	var m manifest
	if err := loadJSON(source, h.Manifest, &m); err != nil {
		return nil, err
	}
	if m.Format != formatName || m.SchemaVersion != 4 || m.Revision != h.Revision || m.WorkID != "" {
		return nil, errors.New("旧 v4 HEAD 与 manifest 不一致")
	}
	for path, hash := range m.ContentHashes {
		actual, err := fileSHA(source, path)
		if err != nil || actual != hash {
			return nil, fmt.Errorf("旧 v4 内容哈希不一致：%s", path)
		}
	}
	entries, err := readOldV4Entries(source, m)
	if err != nil {
		return nil, err
	}
	for i, e := range entries {
		actual, err := fileSHA(source, e.Path)
		if err != nil || (e.SHA256 != "" && actual != e.SHA256) {
			return nil, fmt.Errorf("旧 v4 章节哈希错误：%s", e.ID)
		}
		entries[i].SHA256 = actual
	}
	if err := os.MkdirAll(w.works, 0700); err != nil {
		return nil, err
	}
	unlock, err := w.lockWork(w.works)
	if err != nil {
		return nil, fmt.Errorf("旧 v4 导入：%w", err)
	}
	defer unlock()
	if existing, err := os.ReadDir(w.works); err == nil {
		for _, item := range existing {
			if !item.IsDir() || !safeWorkID.MatchString(item.Name()) {
				continue
			}
			root, err := w.workRoot(item.Name())
			if err != nil {
				return nil, err
			}
			var work map[string]any
			if err := loadJSON(root, "work.json", &work); err != nil {
				return nil, err
			}
			if work["oldV4SourceSHA256"] == sourceHash {
				root, current, _, err := w.load(item.Name())
				if err != nil {
					return nil, err
				}
				repaired, err := w.ensureImportedIntegrity(item.Name(), root, current)
				if err != nil {
					return nil, err
				}
				result := map[string]any{"workId": item.Name(), "revision": current.Revision, "sourceSHA256": sourceHash, "idempotent": true}
				if repaired {
					result["backup"] = "已补齐"
				} else {
					result["backup"] = "正常"
				}
				return result, nil
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	id, err := randomHex(12)
	if err != nil {
		return nil, err
	}
	stage := filepath.Join(w.works, ".oldv4-"+id)
	if err := os.Mkdir(stage, 0700); err != nil {
		return nil, err
	}
	// A failed or interrupted copy must not leave a staging directory behind.
	renamed := false
	defer func() {
		if !renamed {
			_ = os.RemoveAll(stage)
		}
	}()
	files := 0
	total := int64(0)
	err = filepath.WalkDir(source, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("旧 v4 目录含符号链接")
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".writer.lock" || strings.Contains(rel, ".tmp-") {
			return errors.New("旧 v4 有在途写入；先停止写入后再导入")
		}
		data, err := readFile(source, rel)
		if err != nil {
			return err
		}
		files++
		total += int64(len(data))
		if files > 20000 || total > 512<<20 {
			return errors.New("旧 v4 文件数或容量超过安全上限")
		}
		return newFile(stage, rel, data)
	})
	if err != nil {
		return nil, err
	}
	// Recheck the copied snapshot before any conversion. A concurrent source
	// change must not turn a partially copied archive into a valid import.
	for path, expectedHash := range m.ContentHashes {
		actual, hashErr := fileSHA(stage, path)
		if hashErr != nil || actual != expectedHash {
			return nil, fmt.Errorf("旧 v4 复制期间内容变化：%s", path)
		}
	}
	for _, e := range entries {
		actual, hashErr := fileSHA(stage, e.Path)
		if hashErr != nil || actual != e.SHA256 {
			return nil, fmt.Errorf("旧 v4 复制期间章节变化：%s", e.ID)
		}
	}
	copiedEntries, err := readOldV4Entries(stage, m)
	if err != nil {
		return nil, err
	}
	for i, e := range copiedEntries {
		if e.SHA256 == "" {
			e.SHA256 = entries[i].SHA256
		}
		if e != entries[i] {
			return nil, errors.New("旧 v4 复制期间章节目录变化")
		}
	}
	for _, p := range m.Pages {
		if err := atomicJSON(stage, p.Path, entries[p.Start-1:p.End]); err != nil {
			return nil, err
		}
	}
	if err := preserveOldV4Transactions(stage, h.Revision); err != nil {
		return nil, err
	}
	// Only the private copy is changed. All source files remain byte-identical.
	if err := os.Remove(filepath.Join(stage, "work.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	title, _ := m.Meta["title"].(string)
	if strings.TrimSpace(title) == "" {
		return nil, errors.New("旧 v4 作品标题无效")
	}
	if err := newJSON(stage, "work.json", map[string]any{"workId": id, "title": title, "schemaVersion": 4, "oldV4SourceSHA256": sourceHash, "createdAt": time.Now().UTC().Format(time.RFC3339)}); err != nil {
		return nil, err
	}
	m.WorkID = id
	m.ImportedLegacy = true
	if m.EntitiesPath == "" {
		m.EntitiesPath = "entities/import-" + revTag(h.Revision) + ".json"
		if err := newJSON(stage, m.EntitiesPath, []any{}); err != nil {
			return nil, err
		}
	}
	missingMemory, err := normalizeOldV4Memory(stage, &m, entries)
	if err != nil {
		return nil, err
	}
	var tasks []map[string]any
	if err := loadJSON(stage, m.TasksPath, &tasks); err != nil {
		return nil, err
	}
	if len(m.MemoryArchives) > 0 || len(m.TaskArchives) > 0 {
		tasks, err = convertOldV4Archives(stage, &m, tasks, entries)
		if err != nil {
			return nil, err
		}
	}
	texts := map[string]string{}
	for _, e := range entries {
		ch, err := loadChapter(stage, e.Path)
		if err != nil {
			return nil, err
		}
		texts[e.ID] = ch.Content
	}
	for _, task := range tasks {
		if task["status"] == "done" {
			cid, _ := task["completedChapterId"].(string)
			quote, _ := task["completionEvidence"].(string)
			if quote == "" || !strings.Contains(texts[cid], quote) {
				task["legacyUnverified"] = true
			}
		}
	}
	if err := os.Remove(filepath.Join(stage, filepath.FromSlash(m.TasksPath))); err != nil {
		return nil, err
	}
	if err := newJSON(stage, m.TasksPath, tasks); err != nil {
		return nil, err
	}
	m.ContentHashes = map[string]string{}
	for _, path := range []string{m.CharactersPath, m.EntitiesPath, m.TasksPath, m.MemoryPath} {
		hash, err := fileSHA(stage, path)
		if err != nil {
			return nil, err
		}
		m.ContentHashes[path] = hash
	}
	for _, p := range m.Pages {
		hash, err := fileSHA(stage, p.Path)
		if err != nil {
			return nil, err
		}
		m.ContentHashes[p.Path] = hash
	}
	if err := os.Remove(filepath.Join(stage, filepath.FromSlash(h.Manifest))); err != nil {
		return nil, err
	}
	if err := newJSON(stage, h.Manifest, m); err != nil {
		return nil, err
	}
	h.WorkID = id
	h.ManifestSHA256, err = fileSHA(stage, h.Manifest)
	if err != nil {
		return nil, err
	}
	if err := os.Remove(filepath.Join(stage, "HEAD.json")); err != nil {
		return nil, err
	}
	if err := newJSON(stage, "HEAD.json", h); err != nil {
		return nil, err
	}
	check, err := readFile(source, "HEAD.json")
	if err != nil || sumBytes(check) != sourceHash {
		return nil, errors.New("导入期间旧 v4 HEAD 发生变化")
	}
	target, err := w.workRoot(id)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(target); err == nil {
		return nil, errors.New("导入目标作品 ID 已存在")
	}
	if _, err := w.doctorSnapshot(stage, h, m, false); err != nil {
		return nil, fmt.Errorf("旧 v4 校验失败，未发布作品：%w", err)
	}
	backupRoot, err := w.backupRoot(id)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(backupRoot), 0700); err != nil {
		return nil, err
	}
	if err := os.Mkdir(backupRoot, 0700); err != nil {
		return nil, err
	}
	defer func() {
		if !renamed {
			_ = os.RemoveAll(backupRoot)
		}
	}()
	if err := w.backupWork(id, stage, h); err != nil {
		return nil, fmt.Errorf("旧 v4 独立备份失败，未发布作品：%w", err)
	}
	if err := os.Rename(stage, target); err != nil {
		return nil, err
	}
	renamed = true
	result := map[string]any{"workId": id, "revision": h.Revision, "chapters": len(entries), "sourceSHA256": sourceHash, "legacyUnverifiedTasks": countUnverified(tasks)}
	result["missingMemoryFields"] = missingMemory
	missingCards := 0
	for _, e := range entries {
		if e.CardPath == "" {
			missingCards++
		}
	}
	result["chaptersWithoutEvidenceCards"] = missingCards
	result["backup"] = "正常"
	return result, nil
}
