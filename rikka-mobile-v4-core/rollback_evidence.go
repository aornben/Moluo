package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// verifyArchiveEvidence ensures a chapter replacement does not invalidate the
// quotes preserved in immutable memory archives. Active/history memory is
// rebuilt from chapter cards, but archive files are never rebuilt; their
// evidence must still exist in the replacement body.
func (w *workspace) verifyArchiveEvidence(root string, m manifest, chapterID, newContent string) error {
	for _, descriptor := range m.MemoryArchives {
		path, _ := descriptor["path"].(string)
		hash, _ := descriptor["sha256"].(string)
		if path == "" || len(hash) != 64 {
			return errors.New("记忆归档描述无效")
		}
		actual, err := fileSHA(root, path)
		if err != nil || actual != hash {
			return fmt.Errorf("记忆归档缺失或哈希错误：%s", path)
		}
		var items []any
		if err := loadJSON(root, path, &items); err != nil {
			return err
		}
		for _, raw := range items {
			record, ok := raw.(map[string]any)
			if !ok {
				return errors.New("记忆归档条目格式无效")
			}
			if historySource(record) != chapterID {
				continue
			}
			value, _ := record["value"].(map[string]any)
			quote, _ := value["evidence"].(string)
			if quote == "" || !strings.Contains(newContent, quote) {
				recordID, _ := value["id"].(string)
				recordType, _ := record["type"].(string)
				return fmt.Errorf("归档记忆 %s/%s 的原文证据将被替换破坏", recordType, recordID)
			}
		}
	}
	return nil
}

// loadArchiveRecords returns every archived record so a rebuilt memory can drop
// history entries that are already persisted in immutable archives.
func (w *workspace) loadArchiveRecords(root string, m manifest) ([][]any, error) {
	all := make([][]any, 0, len(m.MemoryArchives))
	for _, descriptor := range m.MemoryArchives {
		path, _ := descriptor["path"].(string)
		hash, _ := descriptor["sha256"].(string)
		if path == "" || len(hash) != 64 {
			return nil, errors.New("记忆归档描述无效")
		}
		actual, err := fileSHA(root, path)
		if err != nil || actual != hash {
			return nil, fmt.Errorf("记忆归档缺失或哈希错误：%s", path)
		}
		var items []any
		if err := loadJSON(root, path, &items); err != nil {
			return nil, err
		}
		all = append(all, items)
	}
	return all, nil
}

// filterArchivedRecords removes rebuilt entries whose exact record is already
// stored in an immutable archive. Superseded facts and similar records live in
// history, while constraints and timeline records are archived directly from
// their active fields; all three paths must be filtered after a rebuild.
func filterArchivedRecords(memory map[string]any, archives [][]any) error {
	history, err := memoryItems(memory, "history")
	if err != nil {
		return err
	}
	historySeen := map[string]bool{}
	directSeen := map[string]map[string]bool{
		"constraints": {},
		"timeline":    {},
	}
	for _, items := range archives {
		for _, raw := range items {
			encoded, err := json.Marshal(raw)
			if err != nil {
				return err
			}
			historySeen[string(encoded)] = true
			record, ok := raw.(map[string]any)
			if !ok {
				return errors.New("记忆归档条目格式无效")
			}
			field, _ := record["type"].(string)
			seen, direct := directSeen[field]
			if !direct {
				continue
			}
			value, ok := record["value"].(map[string]any)
			if !ok {
				return errors.New("记忆归档内容格式无效")
			}
			encoded, err = json.Marshal(value)
			if err != nil {
				return err
			}
			seen[string(encoded)] = true
		}
	}
	kept := make([]any, 0, len(history))
	for _, raw := range history {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return err
		}
		if historySeen[string(encoded)] {
			continue
		}
		kept = append(kept, raw)
	}
	memory["history"] = kept
	for _, field := range []string{"constraints", "timeline"} {
		items, err := memoryItems(memory, field)
		if err != nil {
			return err
		}
		kept = make([]any, 0, len(items))
		for _, raw := range items {
			encoded, err := json.Marshal(raw)
			if err != nil {
				return err
			}
			if directSeen[field][string(encoded)] {
				continue
			}
			kept = append(kept, raw)
		}
		memory[field] = kept
	}
	return nil
}
