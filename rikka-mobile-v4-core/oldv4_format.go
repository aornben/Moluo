package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The historical Skill and offline reference write arrays of directory entries.
func readOldV4Entries(root string, m manifest) ([]entry, error) {
	all := []entry{}
	for _, p := range m.Pages {
		if p.Start != len(all)+1 || p.End < p.Start || p.End-p.Start >= 50 {
			return nil, fmt.Errorf("%s：旧 v4 章节页范围无效", p.Path)
		}
		var value any
		if err := loadJSON(root, p.Path, &value); err != nil {
			return nil, fmt.Errorf("%s：章节页 JSON 无效：%w", p.Path, err)
		}
		items, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("%s $：实际类型 %s，预期章节目录对象数组", p.Path, oldV4JSONType(value))
		}
		if len(items) != p.End-p.Start+1 {
			return nil, fmt.Errorf("%s：章节页数量与范围不一致", p.Path)
		}
		for i, item := range items {
			if _, ok := item.(map[string]any); !ok {
				return nil, fmt.Errorf("%s $[%d]：实际类型 %s，预期章节目录对象", p.Path, i, oldV4JSONType(item))
			}
			raw, err := json.Marshal(item)
			if err != nil {
				return nil, err
			}
			var e entry
			if err := decodeJSON(raw, &e); err != nil {
				return nil, fmt.Errorf("%s $[%d]：预期章节目录对象：%w", p.Path, i, err)
			}
			all = append(all, e)
		}
	}
	if len(all) != m.ChapterCount {
		return nil, fmt.Errorf("旧 v4 章节总数与目录不一致")
	}
	return all, nil
}

func oldV4JSONType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case float64:
		return "number"
	default:
		return "boolean"
	}
}

func normalizeOldV4Memory(stage string, m *manifest, entries []entry) ([]string, error) {
	var memory map[string]any
	if m.MemoryPath == "" {
		if len(m.MemoryArchives) != 0 {
			return nil, fmt.Errorf("旧 v4 缺少 memoryPath，但仍引用记忆归档")
		}
		m.MemoryPath = "memory/import-" + revTag(m.Revision) + ".json"
	} else if err := loadJSON(stage, m.MemoryPath, &memory); err != nil {
		return nil, fmt.Errorf("%s：预期记忆快照对象：%w", m.MemoryPath, err)
	}
	if memory == nil {
		memory = map[string]any{}
	}
	missing := []string{}
	for _, field := range []string{"chapterCards", "activeFacts", "knowledge", "openHooks", "openQuestions", "constraints", "timeline", "history", "archives", "summaries"} {
		switch value := memory[field].(type) {
		case nil:
			missing = append(missing, field)
			memory[field] = []any{}
		case []any:
		default:
			return nil, fmt.Errorf("%s $.%s：实际类型 %s，预期记录数组", m.MemoryPath, field, oldV4JSONType(value))
		}
	}
	if memory["sourceRevision"] == nil {
		memory["sourceRevision"] = m.Revision
	}
	if m.MemoryRevision == 0 {
		m.MemoryRevision = m.Revision
	}
	if len(missing) != 0 || memory["coverage"] == nil {
		memory["coverage"] = "partial"
	}
	for _, e := range entries {
		if e.CardPath == "" {
			memory["coverage"] = "partial"
			break
		}
	}
	return missing, atomicJSON(stage, m.MemoryPath, memory)
}

// The offline reference used revision-named transactions with baseManifest
// and planned paths. Keep the original bytes as history in the imported copy.
func preserveOldV4Transactions(stage string, revision int) error {
	files, err := filepath.Glob(filepath.Join(stage, "transactions", "*.json"))
	if err != nil {
		return err
	}
	for _, file := range files {
		name := filepath.Base(file)
		rel := "transactions/" + name
		data, err := readFile(stage, rel)
		if err != nil {
			return err
		}
		var record map[string]any
		if err := decodeJSON(data, &record); err != nil {
			return fmt.Errorf("%s：旧 v4 事务 JSON 无效：%w", rel, err)
		}
		if _, legacy := record["baseManifest"]; !legacy {
			continue
		}
		base, baseOK := integer(record["baseRevision"])
		target, targetOK := integer(record["targetRevision"])
		baseManifest, basePathOK := record["baseManifest"].(string)
		targetManifest, targetPathOK := record["targetManifest"].(string)
		planned, plannedOK := record["planned"].([]any)
		op, opOK := record["operation"].(string)
		if !baseOK || !targetOK || base < 1 || target != base+1 || name != revTag(target)+".json" ||
			!basePathOK || !targetPathOK || !plannedOK || len(planned) == 0 || !opOK || strings.TrimSpace(op) == "" {
			return fmt.Errorf("%s：旧 v4 事务对象字段无效", rel)
		}
		if target > revision {
			return fmt.Errorf("%s：旧 v4 存在未完成事务（目标 revision %d）", rel, target)
		}
		for _, path := range append([]any{baseManifest, targetManifest}, planned...) {
			text, ok := path.(string)
			if !ok {
				return fmt.Errorf("%s：事务路径实际类型 %s，预期字符串", rel, oldV4JSONType(path))
			}
			if _, err := safePath(stage, text); err != nil {
				return fmt.Errorf("%s：旧 v4 事务路径无效：%w", rel, err)
			}
		}
		if err := newFile(stage, "legacy-v4-transactions/"+name, data); err != nil {
			return err
		}
		if err := os.Remove(file); err != nil {
			return err
		}
	}
	return nil
}
