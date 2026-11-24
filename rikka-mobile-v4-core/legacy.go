package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// importLegacy only reads a user-provided copy under the current workspace
// import inbox. The source and any existing v3/v4 directory are never changed.
func (w *workspace) importLegacy(input string) (any, error) {
	clean, err := cleanAbs(input)
	if err != nil {
		return nil, err
	}
	importDir := filepath.Join(w.root, "moluo-v4-imports")
	if filepath.Dir(clean) != importDir || !strings.HasSuffix(strings.ToLower(clean), ".json") {
		return nil, errors.New("旧版导入必须使用 moluo-v4-imports 内的 JSON 副本")
	}
	data, err := readFile(importDir, filepath.Base(clean))
	if err != nil {
		return nil, err
	}
	sourceHash := sumBytes(data)
	var envelope map[string]any
	if err := decodeJSON(data, &envelope); err != nil {
		return nil, err
	}
	if envelope["app"] != "moluo-novel-manager" || envelope["format"] != "moluo-novel-backup" || intValue(envelope["schemaVersion"]) != 1 {
		return nil, errors.New("旧版数据格式或版本不支持")
	}
	legacyRevision := intValue(envelope["revision"])
	if legacyRevision < 0 {
		return nil, errors.New("旧版 revision 无效")
	}
	content, ok := envelope["data"].(map[string]any)
	if !ok || intValue(content["schemaVersion"]) != 1 {
		return nil, errors.New("旧版数据结构无效")
	}
	meta, ok := content["meta"].(map[string]any)
	if !ok {
		return nil, errors.New("旧版作品资料无效")
	}
	title, _ := meta["title"].(string)
	if strings.TrimSpace(title) == "" || len([]rune(title)) > 160 {
		return nil, errors.New("旧版作品标题无效")
	}
	characters, ok := content["characters"].([]any)
	if !ok {
		return nil, errors.New("旧版人物列表无效")
	}
	characterMaps := []map[string]any{}
	for _, raw := range characters {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("旧版人物条目无效")
		}
		characterMaps = append(characterMaps, item)
	}
	charIDs, err := ensureCharacters(characterMaps)
	if err != nil {
		return nil, err
	}
	tasks, ok := content["plotTasks"].([]any)
	if !ok {
		return nil, errors.New("旧版待办列表无效")
	}
	chapterList, ok := content["chapters"].([]any)
	if !ok {
		return nil, errors.New("旧版章节列表无效")
	}
	if len(chapterList) > 9999 {
		return nil, errors.New("旧版章节超过安全上限")
	}
	chapterText := map[string]string{}
	chapterSeen := map[string]bool{}
	chapterMaps := []map[string]any{}
	for _, raw := range chapterList {
		ch, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("旧版章节条目无效")
		}
		id, _ := ch["id"].(string)
		name, _ := ch["title"].(string)
		body, ok := ch["content"].(string)
		if !safeID.MatchString(id) || strings.TrimSpace(name) == "" || !ok || chapterSeen[id] {
			return nil, errors.New("旧版章节 ID、标题、正文或重复 ID 无效")
		}
		chapterSeen[id] = true
		chapterText[id] = body
		chapterMaps = append(chapterMaps, ch)
	}
	taskMaps := []map[string]any{}
	for _, raw := range tasks {
		task, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("旧版待办条目无效")
		}
		copy := copyMap(task)
		if copy["status"] == "done" {
			cid, _ := copy["completedChapterId"].(string)
			quote, _ := copy["completionEvidence"].(string)
			if quote == "" || !strings.Contains(chapterText[cid], quote) {
				copy["legacyUnverified"] = true
			}
		}
		if err := validateTask(copy, charIDs, chapterText, true); err != nil {
			return nil, fmt.Errorf("旧版待办不兼容：%w", err)
		}
		taskMaps = append(taskMaps, copy)
	}
	if existing, err := os.ReadDir(w.works); err == nil {
		for _, item := range existing {
			if !item.IsDir() || !safeWorkID.MatchString(item.Name()) {
				continue
			}
			root, err := w.workRoot(item.Name())
			if err != nil {
				return nil, err
			}
			var info map[string]any
			if err := loadJSON(root, "work.json", &info); err != nil {
				return nil, err
			}
			if info["legacySourceSHA256"] == sourceHash {
				root, h, _, err := w.load(item.Name())
				if err != nil {
					return nil, err
				}
				repaired, err := w.ensureImportedIntegrity(item.Name(), root, h)
				if err != nil {
					return nil, err
				}
				result := map[string]any{"workId": item.Name(), "revision": h.Revision, "sourceSHA256": sourceHash, "idempotent": true}
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
	root, err := w.workRoot(id)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(w.works, 0700); err != nil {
		return nil, err
	}
	stage := filepath.Join(w.works, ".legacy-"+id)
	if err := os.Mkdir(stage, 0700); err != nil {
		return nil, err
	}
	// A failed or interrupted import must never leave a half-built work or a
	// staging directory that later looks like user data.
	renamed := false
	defer func() {
		if !renamed {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := newJSON(stage, "work.json", map[string]any{"workId": id, "title": title, "schemaVersion": 4, "legacySourceSHA256": sourceHash, "createdAt": time.Now().UTC().Format(time.RFC3339)}); err != nil {
		return nil, err
	}
	if err := newJSON(stage, "characters/r000001.json", characters); err != nil {
		return nil, err
	}
	if err := newJSON(stage, "entities/r000001.json", []any{}); err != nil {
		return nil, err
	}
	if err := newJSON(stage, "tasks/r000001.json", taskMaps); err != nil {
		return nil, err
	}
	memory := emptyMemory(1)
	if len(chapterMaps) > 0 {
		memory["coverage"] = "partial"
	}
	if err := newJSON(stage, "memory/r000001.json", memory); err != nil {
		return nil, err
	}
	entries := []entry{}
	for n, ch := range chapterMaps {
		cid := ch["id"].(string)
		name := ch["title"].(string)
		path := fmt.Sprintf("chapters/%s-r000001.json", cid)
		if err := newJSON(stage, path, ch); err != nil {
			return nil, err
		}
		digest, err := fileSHA(stage, path)
		if err != nil {
			return nil, err
		}
		volumeID, _ := ch["volumeId"].(string)
		entries = append(entries, entry{ID: cid, Number: n + 1, Title: name, VolumeID: volumeID, Path: path, SHA256: digest})
	}
	pages := []page{}
	for offset := 0; offset < len(entries); offset += 50 {
		end := offset + 50
		if end > len(entries) {
			end = len(entries)
		}
		path := fmt.Sprintf("pages/p%04d-r000001.json", offset/50+1)
		if err := newJSON(stage, path, entries[offset:end]); err != nil {
			return nil, err
		}
		pages = append(pages, page{Start: offset + 1, End: end, Path: path})
	}
	envExtra := copyMap(envelope)
	for _, k := range []string{"app", "format", "schemaVersion", "revision", "savedAt", "data"} {
		delete(envExtra, k)
	}
	dataExtra := copyMap(content)
	for _, k := range []string{"schemaVersion", "meta", "characters", "chapters", "plotTasks"} {
		delete(dataExtra, k)
	}
	m := manifest{Format: formatName, SchemaVersion: 4, Revision: 1, LegacyRevision: legacyRevision, ImportedLegacy: true, LegacySavedAt: envelope["savedAt"], LegacyEnvelopeExtra: envExtra, LegacyDataExtra: dataExtra, Meta: meta, WorkID: id, CharactersPath: "characters/r000001.json", EntitiesPath: "entities/r000001.json", TasksPath: "tasks/r000001.json", MemoryPath: "memory/r000001.json", MemoryRevision: 1, ChapterCount: len(entries), Pages: pages, MemoryArchives: []map[string]any{}, TaskArchives: []map[string]any{}, ContentHashes: map[string]string{}}
	for _, path := range []string{m.CharactersPath, m.EntitiesPath, m.TasksPath, m.MemoryPath} {
		digest, err := fileSHA(stage, path)
		if err != nil {
			return nil, err
		}
		m.ContentHashes[path] = digest
	}
	for _, p := range pages {
		digest, err := fileSHA(stage, p.Path)
		if err != nil {
			return nil, err
		}
		m.ContentHashes[p.Path] = digest
	}
	if err := newJSON(stage, "manifests/r000001.json", m); err != nil {
		return nil, err
	}
	manifestHash, err := fileSHA(stage, "manifests/r000001.json")
	if err != nil {
		return nil, err
	}
	h := head{Format: formatName, SchemaVersion: 4, Revision: 1, Manifest: "manifests/r000001.json", ManifestSHA256: manifestHash, WorkID: id}
	if err := newJSON(stage, "HEAD.json", h); err != nil {
		return nil, err
	}
	check, err := readFile(importDir, filepath.Base(clean))
	if err != nil || sumBytes(check) != sourceHash {
		return nil, errors.New("导入期间旧版源文件发生变化")
	}
	if _, err := os.Lstat(root); err == nil {
		return nil, errors.New("导入目标作品 ID 已存在")
	}
	if err := os.Rename(stage, root); err != nil {
		return nil, err
	}
	renamed = true
	result := map[string]any{"workId": id, "revision": 1, "legacyRevision": legacyRevision, "chapters": len(entries), "sourceSHA256": sourceHash, "legacyUnverifiedTasks": countUnverified(taskMaps)}
	if err := w.backupWork(id, root, h); err != nil {
		return result, savedBackupError{Cause: fmt.Errorf("旧版已导入但独立备份失败：%w", err)}
	}
	if _, err := w.doctor(id); err != nil {
		return result, savedBackupError{Cause: fmt.Errorf("旧版已导入但健康检查失败：%w", err)}
	}
	result["backup"] = "正常"
	return result, nil
}

// ensureImportedIntegrity verifies a previously imported work and repairs a
// missing independent backup for its current revision. It never rewrites the
// imported chapter bodies, so a re-run cannot silently "fix" corrupted data.
func (w *workspace) ensureImportedIntegrity(id, root string, h head) (bool, error) {
	repaired := false
	if !w.backupExists(id, h.Revision) {
		if err := w.backupWork(id, root, h); err != nil {
			return false, fmt.Errorf("导入作品缺少独立备份且补备份失败：%w", err)
		}
		repaired = true
	}
	if _, err := w.doctorCore(id, true); err != nil {
		return repaired, fmt.Errorf("导入作品完整性校验失败：%w", err)
	}
	return repaired, nil
}

func countUnverified(tasks []map[string]any) int {
	n := 0
	for _, item := range tasks {
		if item["legacyUnverified"] == true {
			n++
		}
	}
	return n
}

func (w *workspace) exportLegacy(id string) (any, error) {
	if _, err := w.doctor(id); err != nil {
		return nil, err
	}
	root, h, m, err := w.load(id)
	if err != nil {
		return nil, err
	}
	entries, err := w.entries(root, m)
	if err != nil {
		return nil, err
	}
	chapters := make([]any, 0, len(entries))
	for _, e := range entries {
		var raw map[string]any
		if err := loadJSON(root, e.Path, &raw); err != nil {
			return nil, err
		}
		chapters = append(chapters, raw)
	}
	var characters []any
	if err := loadJSON(root, m.CharactersPath, &characters); err != nil {
		return nil, err
	}
	var tasks []any
	if err := loadJSON(root, m.TasksPath, &tasks); err != nil {
		return nil, err
	}
	data := map[string]any{"schemaVersion": 1, "meta": m.Meta, "characters": characters, "chapters": chapters, "plotTasks": tasks}
	for k, v := range m.LegacyDataExtra {
		data[k] = v
	}
	envelope := map[string]any{"app": "moluo-novel-manager", "format": "moluo-novel-backup", "schemaVersion": 1, "revision": m.LegacyRevision + h.Revision - 1, "savedAt": m.LegacySavedAt, "data": data}
	for k, v := range m.LegacyEnvelopeExtra {
		envelope[k] = v
	}
	encoded, err := jsonData(envelope)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxJSONFile {
		return nil, errors.New("旧版单文件导出超过 16 MiB；请使用完整 ZIP 导出")
	}
	dir := filepath.Join(w.root, "moluo-v4-exports")
	if err := rejectSymlinks(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	token, err := randomHex(8)
	if err != nil {
		return nil, err
	}
	name := id + "-" + revTag(h.Revision) + "-" + token + "-legacy.json"
	if err := newFile(dir, name, encoded); err != nil {
		return nil, err
	}
	if actual, err := fileSHA(dir, name); err != nil || actual != sumBytes(encoded) {
		return nil, errors.New("旧版导出回读哈希错误")
	}
	return map[string]any{"workId": id, "revision": h.Revision, "path": filepath.Join(dir, name), "sha256": sumBytes(encoded), "bytes": len(encoded)}, nil
}
