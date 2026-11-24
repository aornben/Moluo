package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func archiveArray(object map[string]any, key string) ([]any, error) {
	value, ok := object[key].([]any)
	if !ok {
		return nil, fmt.Errorf("旧 v4 归档 %s 不是数组", key)
	}
	return value, nil
}

func appendUniqueRecord(memory map[string]any, key string, item any) error {
	record, ok := item.(map[string]any)
	if !ok {
		return errors.New("旧 v4 归档记录不是对象")
	}
	id, _ := record["id"].(string)
	if !safeID.MatchString(id) {
		return errors.New("旧 v4 归档记录 ID 无效")
	}
	current, err := memoryItems(memory, key)
	if err != nil {
		return err
	}
	for _, raw := range current {
		old, ok := raw.(map[string]any)
		if ok && old["id"] == id {
			return nil
		}
	}
	memory[key] = append(current, record)
	return nil
}

func convertOldV4Archives(stage string, m *manifest, tasks []map[string]any, entries []entry) ([]map[string]any, error) {
	var memory map[string]any
	if err := loadJSON(stage, m.MemoryPath, &memory); err != nil {
		return nil, err
	}
	for _, key := range []string{"chapterCards", "activeFacts", "knowledge", "openHooks", "openQuestions", "constraints", "timeline"} {
		if _, err := memoryItems(memory, key); err != nil {
			return nil, err
		}
	}
	resolvedHooks := map[string]bool{}
	resolvedQuestions := map[string]bool{}
	for _, e := range entries {
		if e.CardPath == "" {
			continue
		}
		var card map[string]any
		if err := loadJSON(stage, e.CardPath, &card); err != nil {
			return nil, err
		}
		for _, spec := range []struct {
			field    string
			resolved map[string]bool
		}{{"hookChanges", resolvedHooks}, {"openQuestions", resolvedQuestions}} {
			if raw, ok := card[spec.field].([]any); ok {
				for _, value := range raw {
					item, ok := value.(map[string]any)
					if !ok {
						return nil, errors.New("旧 v4 证据卡条目无效")
					}
					if item["status"] == "resolved" {
						id, _ := item["id"].(string)
						spec.resolved[id] = true
					}
				}
			}
		}
	}
	converted := []map[string]any{}
	summaries := []any{}
	oldIndexes, err := memoryItems(memory, "archives")
	if err != nil {
		return nil, err
	}
	oldSummaries, err := memoryItems(memory, "summaries")
	if err != nil {
		return nil, err
	}
	if len(oldIndexes) != len(m.MemoryArchives) || len(oldSummaries) != len(m.MemoryArchives) {
		return nil, errors.New("旧 v4 归档目录或摘要数量不一致")
	}
	covered := map[int]bool{}
	seenCards := map[string]bool{}
	for _, raw := range memory["chapterCards"].([]any) {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("旧 v4 证据卡索引无效")
		}
		id, _ := item["chapterId"].(string)
		seenCards[id] = true
	}
	for index, descriptor := range m.MemoryArchives {
		path, _ := descriptor["path"].(string)
		if path == "" || m.ContentHashes[path] == "" {
			return nil, errors.New("旧 v4 归档路径未受哈希保护")
		}
		start, end := intValue(descriptor["start"]), intValue(descriptor["end"])
		if start < 1 || end < start || end > len(entries) {
			return nil, errors.New("旧 v4 归档章节范围无效")
		}
		for number := start; number <= end; number++ {
			if covered[number] {
				return nil, errors.New("旧 v4 归档章节范围重叠")
			}
			covered[number] = true
		}
		indexed, ok := oldIndexes[index].(map[string]any)
		if !ok || indexed["path"] != path {
			return nil, errors.New("旧 v4 记忆归档索引与 manifest 不一致")
		}
		oldSummary, ok := oldSummaries[index].(map[string]any)
		if !ok || oldSummary["archivePath"] != path {
			return nil, errors.New("旧 v4 摘要归档路径不一致")
		}
		var archive map[string]any
		if err := loadJSON(stage, path, &archive); err != nil {
			return nil, err
		}
		if intValue(archive["start"]) != start || intValue(archive["end"]) != end {
			return nil, errors.New("旧 v4 归档范围与描述不一致")
		}
		ids, ok := archive["chapterIds"].([]any)
		if !ok || len(ids) != end-start+1 {
			return nil, errors.New("旧 v4 归档章节 ID 清单无效")
		}
		expectedIDs := map[string]bool{}
		for _, entry := range entries[start-1 : end] {
			expectedIDs[entry.ID] = true
		}
		for _, raw := range ids {
			id, ok := raw.(string)
			if !ok || !expectedIDs[id] {
				return nil, errors.New("旧 v4 归档章节 ID 与目录不一致")
			}
			delete(expectedIDs, id)
		}
		if len(expectedIDs) != 0 {
			return nil, errors.New("旧 v4 归档章节 ID 有遗漏")
		}
		history := []any{}
		for _, spec := range []struct{ source, target string }{{"facts", "activeFacts"}, {"knowledge", "knowledge"}, {"constraints", "constraints"}, {"timeline", "timeline"}, {"hooks", "openHooks"}, {"questions", "openQuestions"}} {
			items, err := archiveArray(archive, spec.source)
			if err != nil {
				return nil, err
			}
			counts, ok := descriptor["counts"].(map[string]any)
			if !ok || intValue(counts[spec.source]) != len(items) {
				return nil, fmt.Errorf("旧 v4 归档 %s 数量与描述不一致", spec.source)
			}
			for _, raw := range items {
				item, ok := raw.(map[string]any)
				if !ok {
					return nil, errors.New("旧 v4 归档条目不是对象")
				}
				history = append(history, map[string]any{"type": spec.target, "value": raw})
				if spec.target == "openHooks" {
					id, _ := item["id"].(string)
					if !resolvedHooks[id] {
						if err := appendUniqueRecord(memory, "openHooks", raw); err != nil {
							return nil, err
						}
					}
				}
				if spec.target == "openQuestions" {
					id, _ := item["id"].(string)
					if !resolvedQuestions[id] {
						if err := appendUniqueRecord(memory, "openQuestions", raw); err != nil {
							return nil, err
						}
					}
				}
			}
		}
		cards, err := archiveArray(archive, "chapterCards")
		if err != nil {
			return nil, err
		}
		if intValue(descriptor["cardCount"]) != len(cards) {
			return nil, errors.New("旧 v4 归档证据卡数量与描述不一致")
		}
		for _, raw := range cards {
			item, ok := raw.(map[string]any)
			if !ok {
				return nil, errors.New("旧 v4 归档证据卡索引无效")
			}
			id, _ := item["chapterId"].(string)
			if seenCards[id] {
				return nil, errors.New("旧 v4 证据卡索引重复")
			}
			seenCards[id] = true
			memory["chapterCards"] = append(memory["chapterCards"].([]any), item)
		}
		convertedPath := fmt.Sprintf("memory/archives/converted-%04d.json", index+1)
		if err := newJSON(stage, convertedPath, history); err != nil {
			return nil, err
		}
		hash, err := fileSHA(stage, convertedPath)
		if err != nil {
			return nil, err
		}
		newDescriptor := map[string]any{"path": convertedPath, "sha256": hash, "fromChapter": start, "throughChapter": end, "count": len(history), "legacySummary": oldSummary["summary"]}
		converted = append(converted, newDescriptor)
		summaries = append(summaries, newDescriptor)
	}
	for _, descriptor := range m.TaskArchives {
		path, _ := descriptor["path"].(string)
		if path == "" || m.ContentHashes[path] == "" {
			return nil, errors.New("旧 v4 待办归档路径未受哈希保护")
		}
		var archived []map[string]any
		if err := loadJSON(stage, path, &archived); err != nil {
			return nil, err
		}
		if intValue(descriptor["count"]) != len(archived) {
			return nil, errors.New("旧 v4 待办归档数量与描述不一致")
		}
		tasks = append(tasks, archived...)
	}
	memory["archives"] = []any{}
	memory["summaries"] = summaries
	if err := os.Remove(filepath.Join(stage, filepath.FromSlash(m.MemoryPath))); err != nil {
		return nil, err
	}
	if err := newJSON(stage, m.MemoryPath, memory); err != nil {
		return nil, err
	}
	m.MemoryArchives = converted
	m.TaskArchives = []map[string]any{}
	return tasks, nil
}
