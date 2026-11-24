package main

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"time"
)

// metrics reports offline capacity and cost statistics for the current
// revision. Byte counts measure real disk/text volume; they are not tokens.
func (w *workspace) metrics(id string) (any, error) {
	// Metrics must never turn corrupted bytes into plausible statistics. Reuse
	// the full integrity gate without requiring an independent backup merely to
	// read offline measurements.
	if _, err := w.doctorCore(id, false); err != nil {
		return nil, fmt.Errorf("统计前完整性检查失败：%w", err)
	}
	root, h, m, err := w.load(id)
	if err != nil {
		return nil, err
	}
	all, err := w.entries(root, m)
	if err != nil {
		return nil, err
	}
	hanzi, chapterBytes := 0, 0
	for _, e := range all {
		ch, err := loadChapter(root, e.Path)
		if err != nil {
			return nil, err
		}
		hanzi += countHanzi(ch.Content)
		chapterBytes += len([]byte(ch.Content))
	}
	tasks, err := currentRecords(root, m.TasksPath)
	if err != nil {
		return nil, err
	}
	pending, completed := 0, 0
	for _, task := range tasks {
		if task["status"] == "done" || task["status"] == "cancelled" {
			completed++
		} else {
			pending++
		}
	}
	archivedTasks, archivedDone := 0, 0
	for _, descriptor := range m.TaskArchives {
		archive, err := loadTaskArchive(root, descriptor)
		if err != nil {
			return nil, err
		}
		archivedTasks += len(archive.Tasks)
		for _, task := range archive.Tasks {
			if task["status"] == "done" || task["status"] == "cancelled" {
				archivedDone++
			}
		}
	}
	characters, err := currentRecords(root, m.CharactersPath)
	if err != nil {
		return nil, err
	}
	entitiesCount := 0
	if m.EntitiesPath != "" {
		items, err := currentRecords(root, m.EntitiesPath)
		if err != nil {
			return nil, err
		}
		entitiesCount = len(items)
	}
	actualMemoryHash, err := fileSHA(root, m.MemoryPath)
	if err != nil || actualMemoryHash != m.ContentHashes[m.MemoryPath] {
		return nil, fmt.Errorf("记忆索引哈希错误")
	}
	var memory map[string]any
	if err := loadJSON(root, m.MemoryPath, &memory); err != nil {
		return nil, err
	}
	memoryActive := map[string]int{}
	for _, field := range []string{"activeFacts", "knowledge", "openHooks", "openQuestions", "constraints", "timeline"} {
		items, err := memoryItems(memory, field)
		if err != nil {
			return nil, err
		}
		memoryActive[field] = len(items)
	}
	history, err := memoryItems(memory, "history")
	if err != nil {
		return nil, err
	}
	memoryArchived := 0
	for _, descriptor := range m.MemoryArchives {
		path, _ := descriptor["path"].(string)
		var items []any
		if err := loadJSON(root, path, &items); err != nil {
			return nil, err
		}
		memoryArchived += len(items)
	}
	var diskBytes int64
	diskFiles := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		diskBytes += info.Size()
		diskFiles++
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"workId": id, "revision": h.Revision, "version": buildVersion,
		"workPath":                  root,
		"manifestPath":              h.Manifest,
		"manifestSHA256":            h.ManifestSHA256,
		"contentSHA256":             m.ContentHashes,
		"chapters":                  len(all),
		"chapterHanzi":              hanzi,
		"chapterUTF8Bytes":          chapterBytes,
		"characters":                len(characters),
		"entities":                  entitiesCount,
		"tasksActive":               len(tasks),
		"tasksPending":              pending,
		"tasksCompletedOrCancelled": completed,
		"tasksArchived":             archivedTasks,
		"tasksArchivedDone":         archivedDone,
		"tasksCompletedTotal":       completed + archivedDone,
		"taskArchives":              len(m.TaskArchives),
		"memoryArchives":            len(m.MemoryArchives),
		"memoryArchived":            memoryArchived,
		"memoryActive":              memoryActive,
		"memoryHistoryPending":      len(history),
		"diskFiles":                 diskFiles,
		"diskBytes":                 diskBytes,
		"manifestSealed":            h.ManifestSHA256 != "",
		"units":                     "汉字=Unicode Han；字节=UTF-8/磁盘；不是 Token 或费用",
		"generatedAt":               time.Now().UTC().Format(time.RFC3339),
	}, nil
}
