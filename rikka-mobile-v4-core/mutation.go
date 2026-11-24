package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

type recordDraft struct {
	OperationID string         `json:"operationId"`
	Kind        string         `json:"kind"`
	Confirm     bool           `json:"confirm,omitempty"`
	Record      map[string]any `json:"record"`
}

func (w *workspace) recordStage(id, path string) (recordDraft, string, error) {
	clean, err := cleanAbs(path)
	if err != nil {
		return recordDraft{}, "", err
	}
	base := filepath.Join(w.inbox, id)
	if filepath.Dir(clean) != base {
		return recordDraft{}, "", errors.New("候选文件必须位于当前作品的隔离 inbox")
	}
	data, err := readFile(base, filepath.Base(clean))
	if err != nil {
		return recordDraft{}, "", err
	}
	var d recordDraft
	if err := decodeJSON(data, &d); err != nil {
		return d, "", err
	}
	if !safeOpID.MatchString(d.OperationID) || filepath.Base(clean) != d.OperationID+".json" {
		return d, "", errors.New("操作 ID 与候选文件名不一致")
	}
	if d.Record == nil {
		return d, "", errors.New("候选记录缺失")
	}
	return d, sumBytes(data), nil
}

// recoverCommittedMutation is the record-side counterpart of
// recoverCommittedAppend: HEAD's operation ID is the durable commit marker.
func (w *workspace) recoverCommittedMutation(id, root string, h head, m manifest,
	d recordDraft, inputHash string, expected int) (any, bool, error) {
	if h.OperationID != d.OperationID || h.Revision != expected+1 {
		return nil, false, nil
	}
	recordID := "blueprint"
	if d.Kind == "chapter-status" {
		recordID, _ = d.Record["chapterId"].(string)
	} else if d.Kind == "task-archive-done" {
		recordID = "tasks-archive"
	} else if d.Kind != "blueprint-set" && d.Kind != "blueprint-promote" {
		var err error
		recordID, err = idFromRecord(d.Record)
		if err != nil {
			return nil, true, err
		}
	}
	exists := false
	switch d.Kind {
	case "character-add", "character-update":
		records, err := currentRecords(root, m.CharactersPath)
		if err != nil {
			return nil, true, err
		}
		for _, item := range records {
			if item["id"] == recordID && committedRecordMatches(item, d.Record) {
				exists = true
				break
			}
		}
	case "task-add", "task-update", "task-reopen":
		records, err := currentRecords(root, m.TasksPath)
		if err != nil {
			return nil, true, err
		}
		for _, item := range records {
			if item["id"] == recordID && committedRecordMatches(item, d.Record) {
				exists = true
				break
			}
		}
	case "entity-add", "entity-update":
		if m.EntitiesPath != "" {
			records, err := currentRecords(root, m.EntitiesPath)
			if err != nil {
				return nil, true, err
			}
			for _, item := range records {
				if item["id"] == recordID && committedRecordMatches(item, d.Record) {
					exists = true
					break
				}
			}
		}
	case "state-transition":
		committed, err := verifyCommittedStateTransition(root, m, d)
		if err != nil {
			return nil, true, err
		}
		d.Record = committed
		exists = true
	case "blueprint-set":
		if m.BlueprintPath != "" {
			var blueprint map[string]any
			if err := loadJSON(root, m.BlueprintPath, &blueprint); err == nil {
				_, plan := blueprint["plan"]
				_, candidate := blueprint["candidate"]
				_, canon := blueprint["canon"]
				exists = plan && candidate && canon && committedRecordMatches(blueprint, d.Record)
			}
		}
	case "chapter-status":
		cid, _ := d.Record["chapterId"].(string)
		status, _ := d.Record["status"].(string)
		recordID = cid
		exists = safeID.MatchString(cid) && m.ChapterStatus[cid] == status
	case "chapter-order":
		recordID = "chapter-order"
		rawOrder, _ := d.Record["order"].([]any)
		desired := make([]string, 0, len(rawOrder))
		for _, raw := range rawOrder {
			if id, ok := raw.(string); ok {
				desired = append(desired, id)
			}
		}
		exists = reflect.DeepEqual(m.ChapterOrder, desired)
	case "task-delete", "task-archive-done":
		committedID, err := w.verifyCommittedTaskArchive(root, h.Revision, m, d)
		if err != nil {
			return nil, true, err
		}
		recordID = committedID
		exists = true
	case "blueprint-promote":
		bp, err := currentBlueprint(root, m)
		if err != nil {
			return nil, true, err
		}
		if err := verifyBlueprintPromotion(bp, d, h.Revision); err != nil {
			return nil, true, err
		}
		exists = true
	}
	if !exists {
		return nil, true, errors.New("HEAD 已提交该操作，但目标记录不存在，拒绝伪装幂等")
	}
	opPath := "operations/" + d.OperationID + ".json"
	result := map[string]any{"workId": id, "revision": h.Revision,
		"recordId": recordID, "kind": d.Kind, "idempotent": true}
	if err := newJSON(root, opPath, mutationOperationRecord(d, inputHash, h.Revision, recordID)); err != nil {
		return result, true, savedBackupError{Cause: errors.New("记录已保存，但操作记录补写失败：" + err.Error())}
	}
	if !w.backupExists(id, h.Revision) {
		if err := w.backupWork(id, root, h); err != nil {
			return result, true, savedBackupError{Cause: errors.New("记录已保存，但独立备份补写失败：" + err.Error())}
		}
	}
	result["backup"] = "正常"
	return result, true, nil
}

func committedRecordMatches(committed, desired map[string]any) bool {
	for key, want := range desired {
		if !reflect.DeepEqual(committed[key], want) {
			return false
		}
	}
	return true
}

func stringsField(value any, name string) ([]any, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("蓝图 %s 必须是数组", name)
	}
	return items, nil
}

func validateBlueprintLayer(layer map[string]any, name string, requireFull bool) error {
	if requireFull {
		theme, _ := layer["theme"].(string)
		if strings.TrimSpace(theme) == "" {
			return fmt.Errorf("蓝图 %s 缺少主题", name)
		}
		main, ok := layer["mainPlot"].(map[string]any)
		if !ok {
			return fmt.Errorf("蓝图 %s 缺少主线对象", name)
		}
		mainID, _ := main["id"].(string)
		mainSummary, _ := main["summary"].(string)
		if !safeID.MatchString(mainID) || strings.TrimSpace(mainSummary) == "" {
			return fmt.Errorf("蓝图 %s 主线需要稳定 ID 和摘要", name)
		}
	}
	seen := map[string]bool{}
	for _, field := range []string{"subplots", "volumes", "chapterOutline", "chapterLengths", "characterArcs", "pacing", "majorEvents"} {
		value, exists := layer[field]
		if !exists {
			if requireFull {
				return fmt.Errorf("蓝图 %s 缺少 %s", name, field)
			}
			continue
		}
		items, err := stringsField(value, name+"."+field)
		if err != nil {
			return err
		}
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("蓝图 %s.%s 条目必须是对象", name, field)
			}
			id, _ := item["id"].(string)
			if field == "pacing" {
				chapterID, _ := item["chapterId"].(string)
				if !safeID.MatchString(chapterID) {
					return fmt.Errorf("蓝图 %s.pacing 缺少章节 ID", name)
				}
				key := field + ":" + chapterID
				if seen[key] {
					return fmt.Errorf("蓝图 %s.pacing 章节重复：%s", name, chapterID)
				}
				seen[key] = true
				tension, ok := item["tension"].(float64)
				if !ok || tension < 1 || tension > 10 {
					return fmt.Errorf("蓝图 %s.pacing 张力必须是 1～10", name)
				}
				continue
			}
			if !safeID.MatchString(id) {
				return fmt.Errorf("蓝图 %s.%s 条目缺少稳定 ID", name, field)
			}
			key := field + ":" + id
			if seen[key] {
				return fmt.Errorf("蓝图 %s.%s ID 重复：%s", name, field, id)
			}
			seen[key] = true
			if field == "chapterOutline" {
				if title, _ := item["title"].(string); strings.TrimSpace(title) == "" {
					return fmt.Errorf("蓝图 %s.chapterOutline 条目缺少标题", name)
				}
			}
			if field == "chapterLengths" {
				minWords := intValue(item["minWords"])
				maxWords := intValue(item["maxWords"])
				targetWords := intValue(item["targetWords"])
				if minWords < 0 || maxWords < 0 || targetWords < 0 {
					return fmt.Errorf("蓝图 %s.chapterLengths 字数无效", name)
				}
				if minWords == 0 && maxWords == 0 && targetWords == 0 {
					return fmt.Errorf("蓝图 %s.chapterLengths 缺少字数限制", name)
				}
				if minWords > 1000000 || maxWords > 1000000 || targetWords > 1000000 {
					return fmt.Errorf("蓝图 %s.chapterLengths 字数超过上限", name)
				}
				if minWords > 0 && maxWords > 0 && minWords > maxWords {
					return fmt.Errorf("蓝图 %s.chapterLengths 下限大于上限", name)
				}
				if targetWords > 0 && minWords > 0 && targetWords < minWords {
					return fmt.Errorf("蓝图 %s.chapterLengths 目标低于下限", name)
				}
				if targetWords > 0 && maxWords > 0 && targetWords > maxWords {
					return fmt.Errorf("蓝图 %s.chapterLengths 目标高于上限", name)
				}
			}
		}
	}
	return nil
}

func validateBlueprint(blueprint map[string]any) error {
	version, _ := blueprint["schemaVersion"].(float64)
	if version < 2 {
		// Imported/legacy blueprints remain readable; new structured blueprints opt in.
		return nil
	}
	plan, ok := blueprint["plan"].(map[string]any)
	if !ok {
		return errors.New("v2 蓝图缺少 plan 对象")
	}
	if err := validateBlueprintLayer(plan, "plan", true); err != nil {
		return err
	}
	for _, name := range []string{"candidate", "canon"} {
		layer, ok := blueprint[name].(map[string]any)
		if !ok {
			return fmt.Errorf("v2 蓝图缺少 %s 对象", name)
		}
		if len(layer) > 0 {
			if err := validateBlueprintLayer(layer, name, false); err != nil {
				return err
			}
		}
	}
	if history, exists := blueprint["promotionHistory"]; exists {
		entries, ok := history.([]any)
		if !ok {
			return errors.New("蓝图 promotionHistory 必须是数组")
		}
		seenOperations := map[string]bool{}
		for _, raw := range entries {
			record, ok := raw.(map[string]any)
			if !ok {
				return errors.New("蓝图 promotionHistory 条目必须是对象")
			}
			operationID, _ := record["operationId"].(string)
			decision, _ := record["authorDecision"].(string)
			if !safeOpID.MatchString(operationID) || seenOperations[operationID] ||
				strings.TrimSpace(decision) == "" || intValue(record["revision"]) < 1 {
				return errors.New("蓝图 promotionHistory 审计记录无效")
			}
			seenOperations[operationID] = true
			if _, err := blueprintPromotionSelections(record["items"]); err != nil {
				return fmt.Errorf("蓝图 promotionHistory 晋升项无效：%w", err)
			}
		}
	}
	return nil
}

var blueprintPromotableFields = map[string]bool{
	"subplots": true, "volumes": true, "chapterOutline": true, "chapterLengths": true,
	"characterArcs": true, "pacing": true, "majorEvents": true,
}

type blueprintPromotionSelection struct {
	Field string
	ID    string
}

func blueprintEntryID(field string, entry map[string]any) string {
	key := "id"
	if field == "pacing" {
		key = "chapterId"
	}
	id, _ := entry[key].(string)
	return id
}

func blueprintPromotionSelections(raw any) ([]blueprintPromotionSelection, error) {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return nil, errors.New("晋升需要非空 items 列表")
	}
	selections := make([]blueprintPromotionSelection, 0, len(items))
	seen := map[string]bool{}
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return nil, errors.New("晋升条目必须是对象")
		}
		field, _ := item["field"].(string)
		id, _ := item["id"].(string)
		if !blueprintPromotableFields[field] || !safeID.MatchString(id) {
			return nil, fmt.Errorf("晋升条目无效：%s/%s", field, id)
		}
		key := field + ":" + id
		if seen[key] {
			return nil, fmt.Errorf("晋升条目重复：%s/%s", field, id)
		}
		seen[key] = true
		selections = append(selections, blueprintPromotionSelection{Field: field, ID: id})
	}
	return selections, nil
}

func verifyBlueprintPromotion(blueprint map[string]any, draft recordDraft, revision int) error {
	if err := validateBlueprint(blueprint); err != nil {
		return err
	}
	selections, err := blueprintPromotionSelections(draft.Record["items"])
	if err != nil {
		return err
	}
	candidate, _ := blueprint["candidate"].(map[string]any)
	canon, _ := blueprint["canon"].(map[string]any)
	for _, selection := range selections {
		for _, raw := range charSlice(candidate, selection.Field) {
			entry, _ := raw.(map[string]any)
			if blueprintEntryID(selection.Field, entry) == selection.ID {
				return fmt.Errorf("晋升后 candidate.%s 仍包含条目 %s", selection.Field, selection.ID)
			}
		}
		found := 0
		for _, raw := range charSlice(canon, selection.Field) {
			entry, _ := raw.(map[string]any)
			if blueprintEntryID(selection.Field, entry) == selection.ID {
				found++
			}
		}
		if found != 1 {
			return fmt.Errorf("晋升后 canon.%s 中目标条目数量异常：%s", selection.Field, selection.ID)
		}
	}
	decision, _ := draft.Record["authorDecision"].(string)
	history, _ := blueprint["promotionHistory"].([]any)
	matches := 0
	for _, raw := range history {
		record, _ := raw.(map[string]any)
		if record["operationId"] != draft.OperationID {
			continue
		}
		if strings.TrimSpace(decision) != record["authorDecision"] || intValue(record["revision"]) != revision ||
			!reflect.DeepEqual(record["items"], draft.Record["items"]) {
			return errors.New("蓝图晋升审计记录与候选操作不一致")
		}
		matches++
	}
	if matches != 1 {
		return errors.New("蓝图晋升缺少唯一的作者授权审计记录")
	}
	return nil
}

func currentBlueprint(root string, m manifest) (map[string]any, error) {
	if m.BlueprintPath == "" {
		return nil, errors.New("作品没有蓝图")
	}
	var blueprint map[string]any
	if err := loadJSON(root, m.BlueprintPath, &blueprint); err != nil {
		return nil, err
	}
	return blueprint, nil
}

func idFromRecord(record map[string]any) (string, error) {
	id, _ := record["id"].(string)
	if !safeID.MatchString(id) {
		return "", errors.New("记录 ID 无效")
	}
	return id, nil
}

func currentRecords(root, path string) ([]map[string]any, error) {
	var records []map[string]any
	if err := loadJSON(root, path, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func copyMap(value map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range value {
		out[k] = v
	}
	return out
}

func ensureCharacters(records []map[string]any) (map[string]bool, error) {
	ids := map[string]bool{}
	for _, item := range records {
		id, err := idFromRecord(item)
		if err != nil {
			return nil, err
		}
		if ids[id] {
			return nil, errors.New("人物 ID 重复")
		}
		ids[id] = true
		name, _ := item["name"].(string)
		if strings.TrimSpace(name) == "" {
			return nil, errors.New("人物姓名为空")
		}
	}
	return ids, nil
}

func validateTask(task map[string]any, chars map[string]bool, chapters map[string]string, allowLegacy bool) error {
	if _, err := idFromRecord(task); err != nil {
		return err
	}
	title, _ := task["title"].(string)
	if strings.TrimSpace(title) == "" {
		return errors.New("待办标题为空")
	}
	planned, ok := task["plannedChapter"].(float64)
	if !ok || planned < 1 || planned > 9999 || planned != float64(int(planned)) {
		return errors.New("计划章节无效")
	}
	status, _ := task["status"].(string)
	if status != "todo" && status != "in_progress" && status != "done" && status != "deferred" && status != "cancelled" {
		return errors.New("待办状态无效")
	}
	if status == "deferred" {
		until, ok := task["deferredUntilChapter"].(float64)
		if !ok || until < 1 || until != float64(int(until)) {
			return errors.New("延期待办缺少有效目标章节")
		}
	}
	if status == "cancelled" {
		decision, _ := task["authorDecision"].(string)
		if strings.TrimSpace(decision) == "" {
			return errors.New("撤销待办缺少作者决定记录")
		}
	}
	priority, _ := task["priority"].(string)
	if priority != "normal" && priority != "high" {
		return errors.New("待办优先级无效")
	}
	if links, ok := task["characterIds"].([]any); ok {
		for _, raw := range links {
			id, ok := raw.(string)
			if !ok || !chars[id] {
				return errors.New("待办引用未知人物")
			}
		}
	} else if task["characterIds"] != nil {
		return errors.New("待办人物引用格式无效")
	}
	if status == "done" {
		cid, _ := task["completedChapterId"].(string)
		quote, _ := task["completionEvidence"].(string)
		if quote == "" || !strings.Contains(chapters[cid], quote) {
			if allowLegacy && task["legacyUnverified"] == true {
				return nil
			}
			return errors.New("待办核销缺少实际章节原文依据")
		}
	}
	return nil
}

func (w *workspace) mutateFromFile(id, input string, expected int) (any, error) {
	root, h, m, err := w.load(id)
	if err != nil {
		return nil, err
	}
	d, inputHash, err := w.recordStage(id, input)
	if err != nil {
		return nil, err
	}
	validKinds := map[string]bool{"character-add": true, "character-update": true, "task-add": true, "task-update": true, "task-reopen": true, "task-delete": true, "task-archive-done": true, "entity-add": true, "entity-update": true, "state-transition": true, "blueprint-set": true, "blueprint-promote": true, "chapter-status": true, "chapter-order": true}
	if !validKinds[d.Kind] {
		return nil, errors.New("记录操作类型不支持")
	}
	opPath := "operations/" + d.OperationID + ".json"
	var previous opRecord
	if err := loadJSON(root, opPath, &previous); err == nil {
		if previous.InputSHA256 != inputHash || previous.Kind != d.Kind {
			return nil, errors.New("同一操作 ID 对应不同候选内容")
		}
		if d.Kind == "task-delete" || d.Kind == "task-archive-done" {
			committedID, err := w.verifyCommittedTaskArchive(root, previous.Revision, m, d)
			if err != nil {
				return nil, err
			}
			if committedID != previous.RecordID {
				return nil, errors.New("待办归档与操作记录目标不一致")
			}
		}
		if d.Kind == "blueprint-promote" {
			blueprint, err := currentBlueprint(root, m)
			if err != nil {
				return nil, err
			}
			if err := verifyBlueprintPromotion(blueprint, d, previous.Revision); err != nil {
				return nil, err
			}
		}
		if d.Kind == "state-transition" {
			if _, err := verifyCommittedStateTransition(root, m, d); err != nil {
				return nil, err
			}
		}
		if !w.backupExists(id, h.Revision) {
			if err := w.backupWork(id, root, h); err != nil {
				return map[string]any{"workId": id, "revision": previous.Revision,
						"recordId": previous.RecordID, "kind": d.Kind, "idempotent": true},
					savedBackupError{Cause: errors.New("已提交记录操作仍缺独立备份：" + err.Error())}
			}
		}
		return map[string]any{"workId": id, "revision": previous.Revision, "recordId": previous.RecordID, "kind": d.Kind, "idempotent": true}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if result, recovered, err := w.recoverCommittedMutation(id, root, h, m, d, inputHash, expected); recovered {
		return result, err
	}
	if h.Revision != expected {
		return nil, fmt.Errorf("版本冲突：当前 %d，预期 %d", h.Revision, expected)
	}
	if !w.backupExists(id, h.Revision) {
		return nil, errors.New("当前版本独立备份缺失；先执行 backup 补备份")
	}
	if err := w.pending(root, h.Revision); err != nil {
		return nil, err
	}
	unlock, err := w.lockWork(root)
	if err != nil {
		return nil, err
	}
	defer unlock()
	root, h, m, err = w.load(id)
	if err != nil {
		return nil, err
	}
	if h.Revision != expected {
		return nil, errors.New("加锁后版本已变化")
	}
	if _, err := w.doctor(id); err != nil {
		return nil, err
	}
	if err := w.pending(root, h.Revision); err != nil {
		return nil, err
	}
	next := h.Revision + 1
	tag := revTag(next) + "-" + d.OperationID
	newFiles := map[string]any{}
	var recordID string
	var orderRemap []map[string]any
	var taskArchivePending map[string]any
	switch d.Kind {
	case "character-add", "character-update":
		characters, err := currentRecords(root, m.CharactersPath)
		if err != nil {
			return nil, err
		}
		recordID, err = idFromRecord(d.Record)
		if err != nil {
			return nil, err
		}
		found := -1
		for i, item := range characters {
			if item["id"] == recordID {
				found = i
				break
			}
		}
		if d.Kind == "character-add" && found >= 0 {
			return nil, errors.New("人物 ID 已存在")
		}
		if d.Kind == "character-update" && found < 0 {
			return nil, errors.New("人物不存在")
		}
		if d.Kind == "character-update" && found >= 0 {
			if newName, _ := d.Record["name"].(string); strings.TrimSpace(newName) != "" && newName != characters[found]["name"] {
				if !d.Confirm {
					return nil, errors.New("修改人物姓名属于重大设定变更，需要明确确认")
				}
				if decision, _ := d.Record["authorDecision"].(string); strings.TrimSpace(decision) == "" {
					return nil, errors.New("修改人物姓名需要作者决定记录")
				}
			}
		}
		if found < 0 {
			characters = append(characters, d.Record)
		} else {
			updated := copyMap(characters[found])
			for k, v := range d.Record {
				if v == nil {
					return nil, errors.New("人物修改不能清空字段")
				}
				updated[k] = v
			}
			characters[found] = updated
		}
		charIDs, err := ensureCharacters(characters)
		if err != nil {
			return nil, err
		}
		all, err := w.entries(root, m)
		if err != nil {
			return nil, err
		}
		texts := map[string]string{}
		for _, item := range all {
			ch, err := loadChapter(root, item.Path)
			if err != nil {
				return nil, err
			}
			texts[item.ID] = ch.Content
		}
		for _, char := range characters {
			if err := validateCharacter(char, texts, charIDs); err != nil {
				return nil, err
			}
		}
		m.CharactersPath = "characters/" + tag + ".json"
		newFiles[m.CharactersPath] = characters
	case "task-add", "task-update", "task-reopen":
		if d.Record["legacyUnverified"] != nil {
			return nil, errors.New("新写入不得伪造旧版未核验标记")
		}
		tasks, err := currentRecords(root, m.TasksPath)
		if err != nil {
			return nil, err
		}
		characters, err := currentRecords(root, m.CharactersPath)
		if err != nil {
			return nil, err
		}
		charIDs, err := ensureCharacters(characters)
		if err != nil {
			return nil, err
		}
		all, err := w.entries(root, m)
		if err != nil {
			return nil, err
		}
		chapterText := map[string]string{}
		for _, item := range all {
			ch, err := loadChapter(root, item.Path)
			if err != nil {
				return nil, err
			}
			chapterText[item.ID] = ch.Content
		}
		recordID, err = idFromRecord(d.Record)
		if err != nil {
			return nil, err
		}
		found := -1
		for i, item := range tasks {
			if item["id"] == recordID {
				found = i
				break
			}
		}
		if d.Kind == "task-add" && found >= 0 {
			return nil, errors.New("待办 ID 已存在")
		}
		if d.Kind == "task-add" {
			for _, descriptor := range m.TaskArchives {
				archive, err := loadTaskArchive(root, descriptor)
				if err != nil {
					return nil, err
				}
				for _, archived := range archive.Tasks {
					if archived["id"] == recordID {
						return nil, errors.New("待办 ID 已存在于归档中")
					}
				}
			}
		}
		if (d.Kind == "task-update" || d.Kind == "task-reopen") && found < 0 {
			return nil, errors.New("待办不存在")
		}
		if d.Kind == "task-reopen" {
			if !d.Confirm {
				return nil, errors.New("重开待办需要明确确认")
			}
			decision, _ := d.Record["authorDecision"].(string)
			if strings.TrimSpace(decision) == "" {
				return nil, errors.New("重开待办缺少作者决定记录")
			}
			if d.Record["status"] != "todo" && d.Record["status"] != "in_progress" {
				return nil, errors.New("重开目标状态无效")
			}
			if found < 0 || (tasks[found]["status"] != "done" && tasks[found]["status"] != "cancelled") {
				return nil, errors.New("只有已完成或已撤销待办可重开")
			}
		}
		if found < 0 {
			tasks = append(tasks, d.Record)
		} else {
			updated := copyMap(tasks[found])
			if d.Kind != "task-reopen" && updated["status"] == "done" && d.Record["status"] != nil && d.Record["status"] != "done" {
				return nil, errors.New("已完成待办不能在普通更新中重开")
			}
			if d.Kind != "task-reopen" && updated["status"] == "cancelled" && d.Record["status"] != nil && d.Record["status"] != "cancelled" {
				return nil, errors.New("已撤销待办不能在普通更新中重开")
			}
			if d.Record["status"] == "deferred" || d.Record["status"] == "cancelled" {
				if !d.Confirm {
					return nil, errors.New("延期或撤销待办需要明确确认")
				}
				decision, _ := d.Record["authorDecision"].(string)
				if strings.TrimSpace(decision) == "" {
					return nil, errors.New("延期或撤销缺少作者决定记录")
				}
			}
			for k, v := range d.Record {
				if v == nil {
					return nil, errors.New("待办修改不能清空字段")
				}
				updated[k] = v
			}
			if d.Kind == "task-reopen" {
				delete(updated, "completedChapterId")
				delete(updated, "completionEvidence")
				delete(updated, "deferredUntilChapter")
			}
			tasks[found] = updated
		}
		ids := map[string]bool{}
		titles := map[string]bool{}
		for _, task := range tasks {
			tid, _ := task["id"].(string)
			if ids[tid] {
				return nil, errors.New("待办 ID 重复")
			}
			ids[tid] = true
			if err := validateTask(task, charIDs, chapterText, m.ImportedLegacy); err != nil {
				return nil, err
			}
			title, _ := task["title"].(string)
			key := strings.ToLower(strings.Join(strings.Fields(title), " ")) + fmt.Sprint("/", task["plannedChapter"])
			if titles[key] {
				return nil, errors.New("重复待办标题和计划章节")
			}
			titles[key] = true
		}
		m.TasksPath = "tasks/" + tag + ".json"
		newFiles[m.TasksPath] = tasks
	case "task-delete":
		if !d.Confirm {
			return nil, errors.New("删除待办需要明确确认")
		}
		decision, _ := d.Record["authorDecision"].(string)
		if strings.TrimSpace(decision) == "" {
			return nil, errors.New("删除待办缺少作者决定记录")
		}
		tasks, err := currentRecords(root, m.TasksPath)
		if err != nil {
			return nil, err
		}
		characters, err := currentRecords(root, m.CharactersPath)
		if err != nil {
			return nil, err
		}
		charIDs, err := ensureCharacters(characters)
		if err != nil {
			return nil, err
		}
		all, err := w.entries(root, m)
		if err != nil {
			return nil, err
		}
		chapterText := map[string]string{}
		for _, item := range all {
			ch, err := loadChapter(root, item.Path)
			if err != nil {
				return nil, err
			}
			chapterText[item.ID] = ch.Content
		}
		recordID, err = idFromRecord(d.Record)
		if err != nil {
			return nil, err
		}
		found := -1
		for i, item := range tasks {
			if item["id"] == recordID {
				found = i
				break
			}
		}
		if found < 0 {
			return nil, errors.New("待办不存在，无法删除")
		}
		// A task must be internally consistent at deletion time; its full
		// record then moves into an immutable archive instead of vanishing.
		if err := validateTask(tasks[found], charIDs, chapterText, m.ImportedLegacy); err != nil {
			return nil, fmt.Errorf("待办记录异常，先修复再删除：%w", err)
		}
		removed := tasks[found]
		remaining := append(append([]map[string]any{}, tasks[:found]...), tasks[found+1:]...)
		archivePath := fmt.Sprintf("tasks/archives/deleted-%s.json", tag)
		newFiles[archivePath] = []map[string]any{removed}
		taskArchivePending = map[string]any{"path": archivePath, "count": 1, "kind": "deleted",
			"operationId": d.OperationID, "taskId": recordID, "authorDecision": strings.TrimSpace(decision)}
		m.TasksPath = "tasks/" + tag + ".json"
		newFiles[m.TasksPath] = remaining
	case "task-archive-done":
		if !d.Confirm {
			return nil, errors.New("批量归档已完成待办需要明确确认")
		}
		through := intValue(d.Record["throughChapter"])
		if through < 1 || through > 9999 {
			return nil, errors.New("需要有效的 throughChapter（1～9999）")
		}
		tasks, err := currentRecords(root, m.TasksPath)
		if err != nil {
			return nil, err
		}
		characters, err := currentRecords(root, m.CharactersPath)
		if err != nil {
			return nil, err
		}
		charIDs, err := ensureCharacters(characters)
		if err != nil {
			return nil, err
		}
		all, err := w.entries(root, m)
		if err != nil {
			return nil, err
		}
		chapterText := map[string]string{}
		chapterNumbers := map[string]int{}
		for _, item := range all {
			ch, err := loadChapter(root, item.Path)
			if err != nil {
				return nil, err
			}
			chapterText[item.ID] = ch.Content
			chapterNumbers[item.ID] = item.Number
		}
		remaining := []map[string]any{}
		archived := []map[string]any{}
		firstCompleted, lastCompleted := 0, 0
		for _, task := range tasks {
			if task["status"] == "done" {
				cid, _ := task["completedChapterId"].(string)
				if n := chapterNumbers[cid]; n > 0 && n <= through {
					archived = append(archived, task)
					if firstCompleted == 0 || n < firstCompleted {
						firstCompleted = n
					}
					if n > lastCompleted {
						lastCompleted = n
					}
					continue
				}
			}
			remaining = append(remaining, task)
		}
		if len(archived) == 0 {
			return nil, errors.New("没有可归档的已完成待办")
		}
		for _, task := range archived {
			if err := validateTask(task, charIDs, chapterText, m.ImportedLegacy); err != nil {
				return nil, fmt.Errorf("待归档任务证据异常：%w", err)
			}
		}
		archivePath := fmt.Sprintf("tasks/archives/a%06d-%s.json", through, tag)
		newFiles[archivePath] = archived
		taskArchivePending = map[string]any{"path": archivePath, "count": len(archived), "kind": "done",
			"operationId": d.OperationID, "fromChapter": firstCompleted, "throughChapter": through,
			"lastCompletedChapter": lastCompleted}
		m.TasksPath = "tasks/" + tag + ".json"
		newFiles[m.TasksPath] = remaining
		recordID = "tasks-archive"
	case "entity-add", "entity-update":
		entities := []map[string]any{}
		if m.EntitiesPath != "" {
			var err error
			entities, err = currentRecords(root, m.EntitiesPath)
			if err != nil {
				return nil, err
			}
		}
		var err error
		recordID, err = idFromRecord(d.Record)
		if err != nil {
			return nil, err
		}
		found := -1
		for n, item := range entities {
			if item["id"] == recordID {
				found = n
				break
			}
		}
		if d.Kind == "entity-add" && d.Record["kind"] == "state" && d.Record["status"] == "canon" {
			return nil, errors.New("新增正史状态必须使用 state-transition 以校验前置状态和作者授权")
		}
		// Audits, candidates and author decisions are evidence records. Editing
		// them in place would let a later write rewrite what was previously
		// audited or approved, so they are append-only.
		if d.Kind == "entity-update" && found >= 0 {
			if oldKind, _ := entities[found]["kind"].(string); immutableAuditKind(oldKind) {
				return nil, errors.New("审计、候选与授权记录不可原地修改；请追加新记录")
			}
		}
		if d.Kind == "entity-update" {
			if newKind, _ := d.Record["kind"].(string); immutableAuditKind(newKind) {
				return nil, errors.New("不能通过更新把实体改为审计/候选/授权类型；请使用 entity-add 新建")
			}
		}
		if d.Kind == "entity-update" && found >= 0 && entities[found]["kind"] == "state" {
			return nil, errors.New("状态历史不可原地修改；请新增 state-transition")
		}
		if d.Kind == "entity-add" && found >= 0 {
			return nil, errors.New("世界实体 ID 已存在")
		}
		if d.Kind == "entity-update" && found < 0 {
			return nil, errors.New("世界实体不存在")
		}
		if d.Kind == "entity-update" && found >= 0 {
			if newKind, _ := d.Record["kind"].(string); newKind != "" && newKind != entities[found]["kind"] {
				if !d.Confirm {
					return nil, errors.New("修改实体类型属于重大设定变更，需要明确确认")
				}
				if decision, _ := d.Record["authorDecision"].(string); strings.TrimSpace(decision) == "" {
					return nil, errors.New("修改实体类型需要作者决定记录")
				}
			}
			if newStatus, _ := d.Record["status"].(string); newStatus != "" && entities[found]["status"] == "canon" && newStatus != "canon" {
				if !d.Confirm {
					return nil, errors.New("正史实体降级属于重大设定变更，需要明确确认")
				}
				if decision, _ := d.Record["authorDecision"].(string); strings.TrimSpace(decision) == "" {
					return nil, errors.New("正史实体降级需要作者决定记录")
				}
			}
		}
		storedRecord := d.Record
		if d.Kind == "entity-add" {
			kind, _ := d.Record["kind"].(string)
			if immutableAuditKind(kind) {
				if (kind == "audit-decision" || kind == "state-approval") && !d.Confirm {
					return nil, fmt.Errorf("新增 %s 需要顶层 confirm 明确作者授权", kind)
				}
				storedRecord, err = stampSemanticRecord(d.Record, next)
				if err != nil {
					return nil, err
				}
			}
		}
		if found < 0 {
			entities = append(entities, storedRecord)
		} else {
			updated := copyMap(entities[found])
			for k, v := range d.Record {
				if v == nil {
					return nil, errors.New("实体修改不能清空字段")
				}
				updated[k] = v
			}
			entities[found] = updated
		}
		all, err := w.entries(root, m)
		if err != nil {
			return nil, err
		}
		texts := map[string]string{}
		for _, item := range all {
			ch, err := loadChapter(root, item.Path)
			if err != nil {
				return nil, err
			}
			texts[item.ID] = ch.Content
		}
		seen := map[string]bool{}
		for _, entity := range entities {
			eid, err := idFromRecord(entity)
			if err != nil || seen[eid] {
				return nil, errors.New("世界实体 ID 无效或重复")
			}
			seen[eid] = true
			if err := validateEntity(entity, texts); err != nil {
				return nil, err
			}
		}
		characters, err := currentRecords(root, m.CharactersPath)
		if err != nil {
			return nil, err
		}
		charIDs, err := ensureCharacters(characters)
		if err != nil {
			return nil, err
		}
		if err := validateEntityReferences(entities, charIDs); err != nil {
			return nil, err
		}
		m.EntitiesPath = "entities/" + tag + ".json"
		newFiles[m.EntitiesPath] = entities
	case "state-transition":
		entities, transition, err := w.prepareStateTransition(root, m, d)
		if err != nil {
			return nil, err
		}
		d.Record = transition
		recordID, _ = transition["id"].(string)
		entities = append(entities, transition)
		m.EntitiesPath = "entities/" + tag + ".json"
		newFiles[m.EntitiesPath] = entities
	case "chapter-status":
		cid, _ := d.Record["chapterId"].(string)
		status, _ := d.Record["status"].(string)
		if !safeID.MatchString(cid) || !validChapterStatus(status) {
			return nil, errors.New("章节状态操作需要有效 chapterId 和 draft/review/final")
		}
		all, err := w.entries(root, m)
		if err != nil {
			return nil, err
		}
		found := false
		for _, item := range all {
			if item.ID == cid {
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("章节状态操作的目标章节不存在")
		}
		current := chapterState(m, cid)
		if current == status {
			return nil, errors.New("章节状态没有变化")
		}
		allowed := false
		switch current {
		case "draft":
			allowed = status == "review"
		case "review":
			allowed = status == "draft" || status == "final"
		case "final":
			allowed = status == "review"
		}
		if !allowed {
			return nil, fmt.Errorf("不允许的章节状态转换：%s -> %s", current, status)
		}
		if current == "final" {
			if !d.Confirm {
				return nil, errors.New("已定稿章节退回审校需要明确确认")
			}
			if decision, _ := d.Record["authorDecision"].(string); strings.TrimSpace(decision) == "" {
				return nil, errors.New("已定稿章节退回审校需要作者决定记录")
			}
		}
		if m.ChapterStatus == nil {
			m.ChapterStatus = map[string]string{}
		}
		if m.ChapterOutline == nil {
			m.ChapterOutline = map[string]string{}
		}
		if m.ChapterReviewNotes == nil {
			m.ChapterReviewNotes = map[string]string{}
		}
		m.ChapterStatus[cid] = status
		if outline, _ := d.Record["outline"].(string); strings.TrimSpace(outline) != "" {
			m.ChapterOutline[cid] = outline
		}
		if notes, _ := d.Record["reviewNotes"].(string); strings.TrimSpace(notes) != "" {
			m.ChapterReviewNotes[cid] = notes
		}
		recordID = cid
	case "chapter-order":
		if !d.Confirm {
			return nil, errors.New("调整章节叙事顺序需要确认")
		}
		rawOrder, ok := d.Record["order"].([]any)
		if !ok {
			return nil, errors.New("章节顺序必须是数组")
		}
		all, err := w.entries(root, m)
		if err != nil {
			return nil, err
		}
		byID := map[string]bool{}
		for _, item := range all {
			byID[item.ID] = true
		}
		order := make([]string, 0, len(rawOrder))
		seen := map[string]bool{}
		for _, raw := range rawOrder {
			id, ok := raw.(string)
			if !ok || !byID[id] || seen[id] {
				return nil, errors.New("章节顺序包含未知或重复章节 ID")
			}
			seen[id] = true
			order = append(order, id)
		}
		if len(order) != len(all) {
			return nil, errors.New("章节顺序必须覆盖全部章节且不能增删")
		}
		if d.Record["remapTasks"] == true {
			// Tasks are planned against narrative positions. When the author
			// reorders existing chapters, planned chapters that referenced an
			// existing chapter follow it; plans for not-yet-written chapters
			// stay untouched because no chapter anchors them.
			oldOrder := append([]string(nil), m.ChapterOrder...)
			if len(oldOrder) == 0 {
				for _, item := range all {
					oldOrder = append(oldOrder, item.ID)
				}
			}
			oldPos := map[int]string{}
			for i, cid := range oldOrder {
				oldPos[i+1] = cid
			}
			newPos := map[string]int{}
			for i, cid := range order {
				newPos[cid] = i + 1
			}
			tasks, err := currentRecords(root, m.TasksPath)
			if err != nil {
				return nil, err
			}
			characters, err := currentRecords(root, m.CharactersPath)
			if err != nil {
				return nil, err
			}
			charIDs, err := ensureCharacters(characters)
			if err != nil {
				return nil, err
			}
			chapterText := map[string]string{}
			for _, item := range all {
				ch, err := loadChapter(root, item.Path)
				if err != nil {
					return nil, err
				}
				chapterText[item.ID] = ch.Content
			}
			for i, task := range tasks {
				planned := intValue(task["plannedChapter"])
				targetID, ok := oldPos[planned]
				if !ok || newPos[targetID] == planned {
					continue
				}
				updated := copyMap(task)
				updated["plannedChapter"] = float64(newPos[targetID])
				tid, _ := task["id"].(string)
				if err := validateTask(updated, charIDs, chapterText, m.ImportedLegacy); err != nil {
					return nil, fmt.Errorf("重映射待办 %s 失败：%w", tid, err)
				}
				tasks[i] = updated
				orderRemap = append(orderRemap, map[string]any{"taskId": tid, "from": planned, "to": newPos[targetID], "chapterId": targetID})
			}
			if len(orderRemap) > 0 {
				m.TasksPath = "tasks/" + tag + ".json"
				newFiles[m.TasksPath] = tasks
			}
		}
		// Publish the new order only after every requested task remap has
		// validated, so a rejected operation cannot leave a partial state.
		m.ChapterOrder = order
		recordID = "chapter-order"
	case "blueprint-set":
		for _, key := range []string{"plan", "candidate", "canon"} {
			if _, ok := d.Record[key]; !ok {
				return nil, fmt.Errorf("蓝图缺少 %s 分层", key)
			}
		}
		if err := validateBlueprint(d.Record); err != nil {
			return nil, err
		}
		m.BlueprintPath = "blueprints/" + tag + ".json"
		newFiles[m.BlueprintPath] = d.Record
		recordID = "blueprint"
	case "blueprint-promote":
		// Author confirmation chain: the model may only draft candidates; an
		// item becomes canon (and therefore a hard writing requirement) only
		// through this explicit, author-decision-backed promotion.
		if !d.Confirm {
			return nil, errors.New("晋升蓝图候选需要明确确认")
		}
		decision, _ := d.Record["authorDecision"].(string)
		if strings.TrimSpace(decision) == "" {
			return nil, errors.New("晋升蓝图候选缺少作者决定记录")
		}
		if m.BlueprintPath == "" {
			return nil, errors.New("作品没有蓝图，无法晋升候选")
		}
		bp, err := currentBlueprint(root, m)
		if err != nil {
			return nil, err
		}
		candidate, ok := bp["candidate"].(map[string]any)
		if !ok {
			return nil, errors.New("蓝图缺少 candidate 分层")
		}
		canon, ok := bp["canon"].(map[string]any)
		if !ok {
			return nil, errors.New("蓝图缺少 canon 分层")
		}
		if err := validateBlueprint(bp); err != nil {
			return nil, err
		}
		selections, err := blueprintPromotionSelections(d.Record["items"])
		if err != nil {
			return nil, err
		}
		selected := map[string]map[string]bool{}
		for _, selection := range selections {
			if selected[selection.Field] == nil {
				selected[selection.Field] = map[string]bool{}
			}
			selected[selection.Field][selection.ID] = true
			sourceMatches := 0
			for _, raw := range charSlice(candidate, selection.Field) {
				entry, ok := raw.(map[string]any)
				if ok && blueprintEntryID(selection.Field, entry) == selection.ID {
					sourceMatches++
				}
			}
			if sourceMatches != 1 {
				return nil, fmt.Errorf("candidate.%s 中目标条目数量异常：%s", selection.Field, selection.ID)
			}
			for _, raw := range charSlice(canon, selection.Field) {
				entry, ok := raw.(map[string]any)
				if ok && blueprintEntryID(selection.Field, entry) == selection.ID {
					return nil, fmt.Errorf("canon.%s 已存在冲突条目 %s", selection.Field, selection.ID)
				}
			}
		}
		for field, ids := range selected {
			remaining := []any{}
			promoted := []any{}
			for _, raw := range charSlice(candidate, field) {
				entry, _ := raw.(map[string]any)
				if ids[blueprintEntryID(field, entry)] {
					promoted = append(promoted, raw)
				} else {
					remaining = append(remaining, raw)
				}
			}
			candidate[field] = remaining
			canon[field] = append(charSlice(canon, field), promoted...)
		}
		history, _ := bp["promotionHistory"].([]any)
		bp["promotionHistory"] = append(history, map[string]any{
			"operationId": d.OperationID, "revision": next, "authorDecision": strings.TrimSpace(decision),
			"items": d.Record["items"],
		})
		if err := validateBlueprint(bp); err != nil {
			return nil, err
		}
		m.BlueprintPath = "blueprints/" + tag + ".json"
		newFiles[m.BlueprintPath] = bp
		recordID = "blueprint"
	}
	var memory map[string]any
	if err := loadJSON(root, m.MemoryPath, &memory); err != nil {
		return nil, err
	}
	memory["sourceRevision"] = next
	m.MemoryPath = "memory/" + tag + ".json"
	m.MemoryRevision = next
	newFiles[m.MemoryPath] = memory
	manifestPath := "manifests/" + tag + ".json"
	tx := pendingTxn{OperationID: d.OperationID, InputSHA256: inputHash, BaseRevision: h.Revision, TargetRevision: next, TargetManifest: manifestPath}
	if err := newJSON(root, "transactions/"+d.OperationID+".json", tx); err != nil {
		return nil, err
	}
	if err := transactionCheckpoint("mutation:transaction-created"); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(newFiles))
	for path := range newFiles {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := newJSON(root, path, newFiles[path]); err != nil {
			return nil, err
		}
	}
	if err := transactionCheckpoint("mutation:data-written"); err != nil {
		return nil, err
	}
	if taskArchivePending != nil {
		archivePath, _ := taskArchivePending["path"].(string)
		hash, err := fileSHA(root, archivePath)
		if err != nil {
			return nil, err
		}
		taskArchivePending["sha256"] = hash
		m.TaskArchives = append(m.TaskArchives, taskArchivePending)
	}
	previousManifest := h.Manifest
	m.PreviousManifest = &previousManifest
	m.Revision = next
	m.OperationID = d.OperationID
	m.ContentHashes = map[string]string{}
	for _, path := range []string{m.CharactersPath, m.TasksPath, m.MemoryPath} {
		hash, err := fileSHA(root, path)
		if err != nil {
			return nil, err
		}
		m.ContentHashes[path] = hash
	}
	if m.EntitiesPath != "" {
		hash, err := fileSHA(root, m.EntitiesPath)
		if err != nil {
			return nil, err
		}
		m.ContentHashes[m.EntitiesPath] = hash
	}
	for _, p := range m.Pages {
		hash, err := fileSHA(root, p.Path)
		if err != nil {
			return nil, err
		}
		m.ContentHashes[p.Path] = hash
	}
	if m.BlueprintPath != "" {
		m.BlueprintSHA256, err = fileSHA(root, m.BlueprintPath)
		if err != nil {
			return nil, err
		}
	}
	if err := newJSON(root, manifestPath, m); err != nil {
		return nil, err
	}
	if err := transactionCheckpoint("mutation:manifest-written"); err != nil {
		return nil, err
	}
	if err := ensureJSON(root, "recovery/head-"+revTag(h.Revision)+".json", h); err != nil {
		return nil, err
	}
	if err := atomicJSON(root, "HEAD.previous.json", h); err != nil {
		return nil, err
	}
	latest, err := readFile(root, "HEAD.json")
	if err != nil {
		return nil, err
	}
	var check head
	if err := decodeJSON(latest, &check); err != nil {
		return nil, err
	}
	if check.Revision != h.Revision || check.Manifest != h.Manifest {
		return nil, errors.New("切换 HEAD 前发生版本冲突")
	}
	manifestHash, err := fileSHA(root, manifestPath)
	if err != nil {
		return nil, err
	}
	newHead := head{Format: formatName, SchemaVersion: 4, Revision: next, Manifest: manifestPath, ManifestSHA256: manifestHash, WorkID: id, OperationID: d.OperationID}
	if err := transactionCheckpoint("mutation:before-head-switch"); err != nil {
		return nil, err
	}
	if err := atomicJSON(root, "HEAD.json", newHead); err != nil {
		return nil, err
	}
	if err := transactionCheckpoint("mutation:head-switched"); err != nil {
		return nil, err
	}
	result := map[string]any{"workId": id, "revision": next, "kind": d.Kind, "recordId": recordID, "idempotent": false}
	if len(orderRemap) > 0 {
		result["remappedTasks"] = orderRemap
	}
	if taskArchivePending != nil {
		result["archivedCount"] = taskArchivePending["count"]
	}
	if err := newJSON(root, opPath, mutationOperationRecord(d, inputHash, next, recordID)); err != nil {
		return result, savedBackupError{Cause: errors.New("记录已保存，但操作记录写入失败：" + err.Error())}
	}
	if err := w.backupWork(id, root, newHead); err != nil {
		return result, savedBackupError{Cause: errors.New("记录已保存、备份失败：" + err.Error())}
	}
	result["backup"] = "正常"
	return result, nil
}
