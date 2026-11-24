package main

import (
	"errors"
	"fmt"
	"strings"
)

func historySource(record map[string]any) string {
	field, _ := record["type"].(string)
	value, ok := record["value"].(map[string]any)
	if !ok {
		return ""
	}
	key := map[string]string{"activeFacts": "sourceChapterId", "knowledge": "learnedChapterId", "openHooks": "openedChapterId", "openQuestions": "sourceChapterId", "constraints": "sourceChapterId", "timeline": "sourceChapterId"}[field]
	if field == "openQuestions" && value["sourceChapterId"] == nil {
		key = "openedChapterId"
	}
	if field == "openHooks" && value["status"] == "resolved" {
		key = "sourceChapterId"
	}
	id, _ := value[key].(string)
	return id
}

func validateHistory(items []any, contents map[string]string) error {
	for _, raw := range items {
		record, ok := raw.(map[string]any)
		if !ok {
			return errors.New("历史记忆条目格式无效")
		}
		value, ok := record["value"].(map[string]any)
		if !ok {
			return errors.New("历史记忆内容格式无效")
		}
		source := historySource(record)
		quote, _ := value["evidence"].(string)
		if source == "" || quote == "" || !strings.Contains(contents[source], quote) {
			return errors.New("历史记忆原文证据不一致")
		}
	}
	return nil
}

func archiveHistory(root string, m *manifest, memory map[string]any, chapters []entry, chapterNumber int, tag string) error {
	if chapterNumber <= 50 {
		return nil
	}
	chapterNumbers := map[string]int{}
	for _, entry := range chapters {
		chapterNumbers[entry.ID] = entry.Number
	}
	cutoff := chapterNumber - 50
	toArchive := []any{}
	firstArchived, lastArchived := 0, 0
	markRange := func(n int) {
		if firstArchived == 0 || n < firstArchived {
			firstArchived = n
		}
		if n > lastArchived {
			lastArchived = n
		}
	}
	// Superseded records kept in the history layer.
	history, err := memoryItems(memory, "history")
	if err != nil {
		return err
	}
	remaining := []any{}
	for _, raw := range history {
		item, ok := raw.(map[string]any)
		if !ok {
			return errors.New("历史记忆条目无效")
		}
		n := chapterNumbers[historySource(item)]
		if n > 0 && n <= cutoff {
			toArchive = append(toArchive, item)
			markRange(n)
		} else {
			remaining = append(remaining, item)
		}
	}
	// Constraints and timeline entries are historical events: they accumulate
	// per chapter and can move to archives once they are older than the window.
	// Active facts, knowledge, open hooks and open questions stay in the active
	// layer because they describe the current state that later chapters update
	// by ID; archiving them would make the "current" view lose its basis.
	for _, field := range []string{"constraints", "timeline"} {
		items, err := memoryItems(memory, field)
		if err != nil {
			return err
		}
		kept := make([]any, 0, len(items))
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("记忆条目格式无效：%s", field)
			}
			n := chapterNumbers[historySource(map[string]any{"type": field, "value": item})]
			if n > 0 && n <= cutoff {
				toArchive = append(toArchive, map[string]any{"type": field, "value": item})
				markRange(n)
			} else {
				kept = append(kept, item)
			}
		}
		memory[field] = kept
	}
	memory["history"] = remaining
	if len(toArchive) == 0 {
		return nil
	}
	path := fmt.Sprintf("memory/archives/a%06d-%s.json", cutoff, tag)
	if err := newJSON(root, path, toArchive); err != nil {
		return err
	}
	hash, err := fileSHA(root, path)
	if err != nil {
		return err
	}
	descriptor := map[string]any{"path": path, "sha256": hash, "fromChapter": firstArchived, "throughChapter": lastArchived, "rangeExact": true, "count": len(toArchive)}
	m.MemoryArchives = append(m.MemoryArchives, descriptor)
	summaries, err := memoryItems(memory, "summaries")
	if err != nil {
		return err
	}
	memory["summaries"] = append(summaries, descriptor)
	return nil
}

func memoryItems(memory map[string]any, key string) ([]any, error) {
	raw, ok := memory[key]
	if !ok {
		return nil, fmt.Errorf("记忆缺少 %s", key)
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("记忆字段 %s 格式无效", key)
	}
	return items, nil
}

func evidenceIn(item map[string]any, sourceKey string, contents map[string]string) error {
	chapterID, _ := item[sourceKey].(string)
	if chapterID == "" && sourceKey == "sourceChapterId" {
		chapterID, _ = item["openedChapterId"].(string)
	}
	quote, _ := item["evidence"].(string)
	if quote == "" || !strings.Contains(contents[chapterID], quote) {
		return errors.New("长期记忆的章节原文证据不一致")
	}
	return nil
}

func validateMemory(memory map[string]any, entries []entry, contents map[string]string, characters map[string]bool) error {
	history, err := memoryItems(memory, "history")
	if err == nil {
		if err := validateHistory(history, contents); err != nil {
			return err
		}
	}
	cardPaths := map[string]string{}
	for _, e := range entries {
		if e.CardPath != "" {
			cardPaths[e.ID] = e.CardPath
		}
	}
	cards, err := memoryItems(memory, "chapterCards")
	if err != nil {
		return err
	}
	cardSeen := map[string]bool{}
	for _, raw := range cards {
		item, ok := raw.(map[string]any)
		if !ok {
			return errors.New("证据卡索引格式无效")
		}
		id, _ := item["chapterId"].(string)
		path, _ := item["path"].(string)
		if id == "" || cardSeen[id] || cardPaths[id] != path {
			return errors.New("证据卡索引重复或路径错误")
		}
		cardSeen[id] = true
	}
	if memory["coverage"] == "complete" && len(cards) != len(entries) {
		return errors.New("记忆完整覆盖声明与证据卡数量不符")
	}
	for _, spec := range []struct{ field, source string }{{"activeFacts", "sourceChapterId"}, {"knowledge", "learnedChapterId"}, {"openHooks", "openedChapterId"}, {"openQuestions", "sourceChapterId"}, {"constraints", "sourceChapterId"}, {"timeline", "sourceChapterId"}} {
		items, err := memoryItems(memory, spec.field)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("记忆条目格式无效：%s", spec.field)
			}
			id, _ := item["id"].(string)
			if spec.field == "knowledge" {
				id = fmt.Sprint(item["characterId"], ":", item["factId"])
			}
			if id == "" || seen[id] {
				return fmt.Errorf("记忆条目 ID 重复或缺失：%s", spec.field)
			}
			seen[id] = true
			if spec.field == "knowledge" {
				charID, _ := item["characterId"].(string)
				if !characters[charID] {
					return errors.New("人物认知引用缺失人物")
				}
			}
			if spec.field == "openHooks" && item["status"] != "open" {
				return errors.New("未解伏笔状态无效")
			}
			if spec.field == "activeFacts" {
				certainty, _ := item["certainty"].(string)
				if certainty != "certain" && certainty != "possible" && certainty != "unknown" && certainty != "conflict" {
					return errors.New("长期事实确定性无效")
				}
			}
			if err := evidenceIn(item, spec.source, contents); err != nil {
				return fmt.Errorf("%s：%w", spec.field, err)
			}
		}
	}
	return nil
}
