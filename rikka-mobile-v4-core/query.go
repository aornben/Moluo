package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// query only reads the selected current revision. A cursor is an item offset,
// not a byte offset (read uses UTF-8 byte cursors for chapter text).
func (w *workspace) query(id, kind, keyword, character string, cursor, limit, chapterNumber int) (any, error) {
	return w.queryFiltered(id, kind, keyword, character, cursor, limit, chapterNumber, 0, 0, "", "", "")
}

func queryMemorySource(record map[string]any) string {
	if record["type"] == "history" || record["type"] == "archivedHistory" {
		if value, ok := record["value"].(map[string]any); ok {
			return historySource(value)
		}
		return ""
	}
	return historySource(record)
}

func queryMemoryValue(record map[string]any) map[string]any {
	if record["type"] == "history" || record["type"] == "archivedHistory" {
		if nested, ok := record["value"].(map[string]any); ok {
			record = nested
		}
	}
	value, _ := record["value"].(map[string]any)
	return value
}

func queryMemoryDate(record map[string]any) (string, bool, error) {
	value := queryMemoryValue(record)
	for _, key := range []string{"startDate", "date", "dueDate"} {
		raw, exists := value[key]
		if !exists {
			continue
		}
		dateText, ok := raw.(string)
		if !ok {
			return "", false, fmt.Errorf("记忆日期字段 %s 必须是字符串", key)
		}
		if dateText == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", dateText); err != nil {
			return "", false, fmt.Errorf("记忆日期字段 %s 无效：%s", key, dateText)
		}
		return dateText, true, nil
	}
	return "", false, nil
}

func (w *workspace) queryFiltered(id, kind, keyword, character string, cursor, limit, chapterNumber, fromChapter, toChapter int, status, fromDate, toDate string) (any, error) {
	if cursor < 0 || limit < 1 || chapterNumber < 0 || fromChapter < 0 || toChapter < 0 ||
		(fromChapter > 0 && toChapter > 0 && fromChapter > toChapter) {
		return nil, errors.New("查询分页参数无效")
	}
	if character != "" && !safeID.MatchString(character) {
		return nil, errors.New("人物 ID 无效")
	}
	if limit > 20 {
		limit = 20
	}
	parsedDates := make([]time.Time, 2)
	for index, value := range []string{fromDate, toDate} {
		if value == "" {
			continue
		}
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			return nil, errors.New("日期过滤必须是 YYYY-MM-DD")
		}
		parsedDates[index] = parsed
	}
	if fromDate != "" && toDate != "" && parsedDates[0].After(parsedDates[1]) {
		return nil, errors.New("日期过滤起始日期不能晚于结束日期")
	}
	root, h, m, err := w.load(id)
	if err != nil {
		return nil, err
	}
	var records []any
	chapterNumbers := map[string]int{}
	archivesRead, archivesSkipped := 0, 0
	switch kind {
	case "chapters":
		all, err := w.entries(root, m)
		if err != nil {
			return nil, err
		}
		orderIndex := map[string]int{}
		for index, id := range m.ChapterOrder {
			orderIndex[id] = index + 1
		}
		for _, e := range all {
			order := orderIndex[e.ID]
			if order == 0 {
				order = e.Number
			}
			records = append(records, map[string]any{"id": e.ID, "number": e.Number,
				"order": order, "title": e.Title, "volumeId": e.VolumeID,
				"status": chapterState(m, e.ID), "sha256": e.SHA256})
		}
	case "characters", "tasks", "entities":
		path := m.CharactersPath
		if kind == "tasks" {
			path = m.TasksPath
		}
		if kind == "entities" {
			path = m.EntitiesPath
			if path == "" {
				return map[string]any{"workId": id, "revision": h.Revision, "items": []any{}, "complete": true}, nil
			}
		}
		if m.ContentHashes[path] == "" {
			return nil, errors.New("索引哈希缺失")
		}
		actual, err := fileSHA(root, path)
		if err != nil || actual != m.ContentHashes[path] {
			return nil, errors.New("索引哈希错误")
		}
		var items []map[string]any
		if err := loadJSON(root, path, &items); err != nil {
			return nil, err
		}
		for _, item := range items {
			records = append(records, item)
		}
		if kind == "tasks" {
			// Archived tasks remain queryable; the archive copy is authoritative
			// and is hash-, range-, evidence-, and duplicate-verified on every read.
			characterIDs, contents, chapterNumbers, err := w.taskValidationContext(root, m)
			if err != nil {
				return nil, err
			}
			seenTaskIDs := map[string]bool{}
			for _, item := range items {
				id, _ := item["id"].(string)
				seenTaskIDs[id] = true
			}
			seenArchivePaths := map[string]bool{}
			for _, descriptor := range m.TaskArchives {
				archive, err := loadTaskArchive(root, descriptor)
				if err != nil {
					return nil, err
				}
				if seenArchivePaths[archive.Path] {
					return nil, errors.New("待办归档路径重复")
				}
				seenArchivePaths[archive.Path] = true
				if err := validateTaskArchiveRange(archive, chapterNumbers); err != nil {
					return nil, err
				}
				for _, task := range archive.Tasks {
					id, _ := task["id"].(string)
					if seenTaskIDs[id] {
						return nil, errors.New("活动任务与归档任务或多个归档之间存在重复 ID")
					}
					if err := validateTask(task, characterIDs, contents, m.ImportedLegacy); err != nil {
						return nil, fmt.Errorf("待办归档证据失效：%w", err)
					}
					seenTaskIDs[id] = true
					marked := copyMap(task)
					marked["archived"] = true
					marked["archivePath"] = archive.Path
					records = append(records, marked)
				}
			}
		}
	case "states":
		// Derived causal states: computed from auditable history at read time.
		numbers, characters, entities, err := w.loadStateSources(root, m)
		if err != nil {
			return nil, err
		}
		for _, char := range characters {
			records = append(records, map[string]any{"type": "character_state", "value": deriveCharacterState(char, numbers)})
		}
		if len(entities) > 0 {
			subjects := map[string]map[string]any{}
			for _, entity := range entities {
				records = append(records, map[string]any{"type": "entity_state", "value": deriveEntityState(entity, numbers)})
				if entity["kind"] == "state" && entity["status"] == "canon" {
					if subject, _ := entity["subjectId"].(string); subject != "" {
						bucket := subjects[subject]
						if bucket == nil {
							bucket = map[string]any{}
							subjects[subject] = bucket
						}
						derived := deriveEntityState(entity, numbers)
						stateType, _ := entity["stateType"].(string)
						if stateType == "" {
							stateType = "state"
						}
						existing, _ := bucket[stateType].(map[string]any)
						if existing == nil || intValue(derived["chapter"]) >= intValue(existing["chapter"]) {
							bucket[stateType] = derived
						}
					}
				}
			}
			for subject, bucket := range subjects {
				value := map[string]any{"subjectId": subject}
				for key, item := range bucket {
					value[key] = item
				}
				records = append(records, map[string]any{"type": "subject_state", "value": value})
			}
		}
	case "memory":
		if fromChapter > 0 || toChapter > 0 {
			all, err := w.entries(root, m)
			if err != nil {
				return nil, err
			}
			for _, chapter := range all {
				chapterNumbers[chapter.ID] = chapter.Number
			}
		}
		actual, err := fileSHA(root, m.MemoryPath)
		if err != nil || actual != m.ContentHashes[m.MemoryPath] {
			return nil, errors.New("记忆哈希错误")
		}
		var memory map[string]any
		if err := loadJSON(root, m.MemoryPath, &memory); err != nil {
			return nil, err
		}
		for _, field := range []string{"activeFacts", "knowledge", "openHooks", "openQuestions", "constraints", "timeline"} {
			items, err := memoryItems(memory, field)
			if err != nil {
				return nil, err
			}
			for _, raw := range items {
				records = append(records, map[string]any{"type": field, "value": raw})
			}
		}
		if history, err := memoryItems(memory, "history"); err == nil {
			for _, raw := range history {
				records = append(records, map[string]any{"type": "history", "value": raw})
			}
		}
		for _, descriptor := range m.MemoryArchives {
			first, last := intValue(descriptor["fromChapter"]), intValue(descriptor["throughChapter"])
			// Older indexes may omit or overstate the range. Only skip when the
			// indexed range proves that no requested chapter can be present.
			if first > 0 && last >= first && ((fromChapter > 0 && last < fromChapter) || (toChapter > 0 && first > toChapter)) {
				archivesSkipped++
				continue
			}
			path, _ := descriptor["path"].(string)
			hash, _ := descriptor["sha256"].(string)
			actual, err := fileSHA(root, path)
			if err != nil || actual != hash {
				return nil, errors.New("历史归档哈希错误")
			}
			var items []any
			if err := loadJSON(root, path, &items); err != nil {
				return nil, err
			}
			archivesRead++
			for _, raw := range items {
				records = append(records, map[string]any{"type": "archivedHistory", "archive": path, "value": raw})
			}
		}
	case "blueprint":
		if m.BlueprintPath == "" {
			return map[string]any{"workId": id, "revision": h.Revision, "items": []any{}, "complete": true}, nil
		}
		actual, err := fileSHA(root, m.BlueprintPath)
		if err != nil || actual != m.BlueprintSHA256 {
			return nil, errors.New("蓝图哈希错误")
		}
		var blueprint map[string]any
		if err := loadJSON(root, m.BlueprintPath, &blueprint); err != nil {
			return nil, err
		}
		records = []any{blueprint}
	default:
		return nil, errors.New("查询种类无效")
	}
	needle := strings.ToLower(strings.TrimSpace(keyword))
	filtered := make([]any, 0)
	for _, item := range records {
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		if needle != "" && !strings.Contains(strings.ToLower(string(encoded)), needle) {
			continue
		}
		if character != "" && !strings.Contains(string(encoded), `"`+character+`"`) {
			if kind != "states" {
				continue
			}
			record, ok := item.(map[string]any)
			if !ok || !stateValueContainsID(record, character) {
				continue
			}
		}
		if kind == "tasks" && chapterNumber > 0 {
			task, ok := item.(map[string]any)
			if !ok || task["status"] == "done" || intValue(task["plannedChapter"]) > chapterNumber {
				continue
			}
		}
		if (kind == "chapters" || kind == "tasks") && (fromChapter > 0 || toChapter > 0) {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			number := intValue(record["number"])
			if kind == "tasks" {
				number = intValue(record["plannedChapter"])
			}
			if fromChapter > 0 && number < fromChapter {
				continue
			}
			if toChapter > 0 && number > toChapter {
				continue
			}
		}
		if kind == "memory" && (fromChapter > 0 || toChapter > 0) {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			number := chapterNumbers[queryMemorySource(record)]
			if number == 0 || (fromChapter > 0 && number < fromChapter) || (toChapter > 0 && number > toChapter) {
				continue
			}
		}
		if kind == "states" && (fromChapter > 0 || toChapter > 0) {
			record, ok := item.(map[string]any)
			if !ok || !stateRecordHasChapterInRange(record, fromChapter, toChapter) {
				continue
			}
		}
		if kind == "memory" && (fromDate != "" || toDate != "") {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			dateText, dated, err := queryMemoryDate(record)
			if err != nil {
				return nil, err
			}
			// Date-range queries return only dated records; undated memory
			// cannot be placed on a timeline and must not be guessed.
			if !dated {
				continue
			}
			if fromDate != "" && dateText < fromDate {
				continue
			}
			if toDate != "" && dateText > toDate {
				continue
			}
		}
		if status != "" {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if kind == "states" {
				if !stateRecordMatchesType(record, status) {
					continue
				}
				filtered = append(filtered, item)
				continue
			}
			actual := record["status"]
			if kind == "memory" {
				actual = queryMemoryValue(record)["status"]
			}
			if actual != status {
				continue
			}
		}
		filtered = append(filtered, item)
	}
	if cursor > len(filtered) {
		return nil, errors.New("查询游标超出范围")
	}
	end := cursor + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	items := filtered[cursor:end]
	var data []byte
	for {
		var err error
		data, err = json.Marshal(items)
		if err != nil {
			return nil, err
		}
		if len(data) <= 32768 {
			break
		}
		if len(items) <= 1 {
			return nil, fmt.Errorf("单条结果超过 32 KiB；请缩小 --query")
		}
		// Return a smaller page instead of truncating the JSON or claiming complete.
		items = items[:len(items)-1]
	}
	end = cursor + len(items)
	return map[string]any{"workId": id, "revision": h.Revision, "kind": kind, "items": items, "total": len(filtered), "cursor": cursor, "nextCursor": end, "complete": end == len(filtered), "outputBytes": len(data), "archivesRead": archivesRead, "archivesSkipped": archivesSkipped}, nil
}
