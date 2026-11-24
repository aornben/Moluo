package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The causal state engine derives current states from auditable history at
// read time. History entries always keep their chapter evidence; the derived
// view never replaces or rewrites the underlying records.

func charSlice(object map[string]any, field string) []any {
	items, _ := object[field].([]any)
	return items
}

func entryChapter(entry map[string]any, numbers map[string]int) int {
	// Keep this order aligned with validateEvidenceItem. Older compatible
	// records commonly use chapterId/fromChapterId rather than sourceChapterId.
	for _, key := range []string{"sourceChapterId", "learnedChapterId", "chapterId", "fromChapterId", "toChapterId", "sinceChapterId"} {
		if value, _ := entry[key].(string); value != "" {
			return numbers[value]
		}
	}
	return 0
}

func latestEvidenceEntry(items []any, numbers map[string]int) (map[string]any, int, bool) {
	best := 0
	var out map[string]any
	for _, raw := range items {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if n := entryChapter(entry, numbers); n > best {
			best = n
			out = entry
		}
	}
	if best == 0 {
		return nil, 0, false
	}
	return out, best, true
}

func latestPerKey(items []any, numbers map[string]int, keyField string) []map[string]any {
	latest := map[string]map[string]any{}
	chapterOf := map[string]int{}
	for _, raw := range items {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		key, _ := entry[keyField].(string)
		if key == "" {
			continue
		}
		n := entryChapter(entry, numbers)
		if n <= 0 {
			continue
		}
		if _, seen := latest[key]; seen && chapterOf[key] >= n {
			continue
		}
		merged := copyMap(entry)
		merged["chapter"] = n
		latest[key] = merged
		chapterOf[key] = n
	}
	keys := make([]string, 0, len(latest))
	for key := range latest {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		out = append(out, latest[key])
	}
	return out
}

func currentDeathState(char map[string]any, numbers map[string]int) (map[string]any, int, bool) {
	if death, ok := char["death"].(map[string]any); ok {
		status, _ := death["status"].(string)
		if status == "" {
			return nil, 0, false
		}
		return death, entryChapter(death, numbers), true
	}
	if history, ok := char["death"].([]any); ok {
		return latestEvidenceEntry(history, numbers)
	}
	return nil, 0, false
}

func deriveCharacterState(char map[string]any, numbers map[string]int) map[string]any {
	id, _ := char["id"].(string)
	name, _ := char["name"].(string)
	state := map[string]any{"id": id, "name": name}
	if death, n, found := currentDeathState(char, numbers); found {
		entry := copyMap(death)
		if n > 0 {
			entry["chapter"] = n
		}
		state["death"] = entry
	}
	if entry, n, ok := latestEvidenceEntry(charSlice(char, "injuries"), numbers); ok {
		merged := copyMap(entry)
		merged["chapter"] = n
		state["injuryLatest"] = merged
	}
	if relationships := latestPerKey(charSlice(char, "relationships"), numbers, "targetId"); len(relationships) > 0 {
		state["relationships"] = relationships
	}
	// cognition is the current view across the current field and both legacy
	// compatible field names. Facts absent from the newest field must not vanish.
	knowledgeHistory := []any{}
	for _, field := range []string{"cognition", "knowledge", "beliefs"} {
		knowledgeHistory = append(knowledgeHistory, charSlice(char, field)...)
	}
	if knowledge := latestPerKey(knowledgeHistory, numbers, "factId"); len(knowledge) > 0 {
		state["cognition"] = knowledge
	}
	return state
}

func deriveEntityState(entity map[string]any, numbers map[string]int) map[string]any {
	id, _ := entity["id"].(string)
	name, _ := entity["name"].(string)
	kind, _ := entity["kind"].(string)
	state := map[string]any{"id": id, "name": name, "kind": kind, "factStatus": entity["status"]}
	if owner, n, ok := latestEvidenceEntry(charSlice(entity, "ownershipHistory"), numbers); ok {
		merged := copyMap(owner)
		merged["chapter"] = n
		state["ownerLatest"] = merged
	}
	if entry, n, ok := latestEvidenceEntry(charSlice(entity, "transfers"), numbers); ok {
		merged := copyMap(entry)
		merged["chapter"] = n
		state["transferLatest"] = merged
	}
	if entry, n, ok := latestEvidenceEntry(charSlice(entity, "conditionHistory"), numbers); ok {
		merged := copyMap(entry)
		merged["chapter"] = n
		state["conditionLatest"] = merged
	}
	if relations := latestPerKey(charSlice(entity, "relations"), numbers, "targetId"); len(relations) > 0 {
		state["relations"] = relations
	}
	state["relationsCount"] = len(charSlice(entity, "relations"))
	if kind == "state" {
		if subject, _ := entity["subjectId"].(string); subject != "" {
			state["subjectId"] = subject
		}
		if stateType, _ := entity["stateType"].(string); stateType != "" {
			state["stateType"] = stateType
		}
		if src, _ := entity["sourceChapterId"].(string); src != "" {
			state["chapter"] = numbers[src]
		}
		if evidence, _ := entity["evidence"].(string); evidence != "" {
			state["evidence"] = evidence
		}
		if source, _ := entity["sourceChapterId"].(string); source != "" {
			state["sourceChapterId"] = source
		}
		if value, exists := entity["value"]; exists {
			state["value"] = value
		}
	}
	return state
}

// loadStateSources performs the complete integrity gate required before state
// derivation. A valid outer manifest hash alone is insufficient: every index,
// chapter and evidence reference used by the derived view must also validate.
func (w *workspace) loadStateSources(root string, m manifest) (map[string]int, []map[string]any, []map[string]any, error) {
	for _, p := range m.Pages {
		expected := m.ContentHashes[p.Path]
		actual, err := fileSHA(root, p.Path)
		if expected == "" || err != nil || actual != expected {
			return nil, nil, nil, errors.New("章节目录页哈希错误")
		}
	}
	all, err := w.entries(root, m)
	if err != nil {
		return nil, nil, nil, err
	}
	numbers := map[string]int{}
	contents := map[string]string{}
	for _, e := range all {
		actual, err := fileSHA(root, e.Path)
		if err != nil || actual != e.SHA256 {
			return nil, nil, nil, fmt.Errorf("章节哈希不一致：%s", e.ID)
		}
		ch, err := loadChapter(root, e.Path)
		if err != nil {
			return nil, nil, nil, err
		}
		if ch.ID != e.ID || ch.Title != e.Title || ch.VolumeID != e.VolumeID {
			return nil, nil, nil, errors.New("章节目录与正文不一致")
		}
		numbers[e.ID] = e.Number
		contents[e.ID] = ch.Content
	}
	if m.ContentHashes[m.CharactersPath] == "" {
		return nil, nil, nil, errors.New("人物索引哈希缺失")
	}
	actual, err := fileSHA(root, m.CharactersPath)
	if err != nil || actual != m.ContentHashes[m.CharactersPath] {
		return nil, nil, nil, errors.New("人物索引哈希错误")
	}
	characters, err := currentRecords(root, m.CharactersPath)
	if err != nil {
		return nil, nil, nil, err
	}
	characterIDs, err := ensureCharacters(characters)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, character := range characters {
		if err := validateCharacter(character, contents, characterIDs); err != nil {
			return nil, nil, nil, fmt.Errorf("人物状态证据无效：%w", err)
		}
	}
	entities := []map[string]any{}
	if m.EntitiesPath != "" {
		if m.ContentHashes[m.EntitiesPath] == "" {
			return nil, nil, nil, errors.New("实体索引哈希缺失")
		}
		entityHash, err := fileSHA(root, m.EntitiesPath)
		if err != nil || entityHash != m.ContentHashes[m.EntitiesPath] {
			return nil, nil, nil, errors.New("实体索引哈希错误")
		}
		entities, err = currentRecords(root, m.EntitiesPath)
		if err != nil {
			return nil, nil, nil, err
		}
		seen := map[string]bool{}
		for _, entity := range entities {
			id, err := idFromRecord(entity)
			if err != nil || seen[id] {
				return nil, nil, nil, errors.New("世界实体 ID 无效或重复")
			}
			seen[id] = true
			if err := validateEntity(entity, contents); err != nil {
				return nil, nil, nil, fmt.Errorf("实体状态证据无效：%w", err)
			}
		}
		if err := validateEntityReferences(entities, characterIDs); err != nil {
			return nil, nil, nil, err
		}
	}
	return numbers, characters, entities, nil
}

func stateValueContainsID(value any, id string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for _, nested := range typed {
			if stateValueContainsID(nested, id) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if stateValueContainsID(nested, id) {
				return true
			}
		}
	case string:
		return typed == id
	}
	return false
}

func stateRecordMatchesType(record map[string]any, wanted string) bool {
	if record["type"] == wanted {
		return true
	}
	value, _ := record["value"].(map[string]any)
	if value["stateType"] == wanted {
		return true
	}
	_, nestedStateType := value[wanted]
	return record["type"] == "subject_state" && nestedStateType
}

func stateRecordHasChapterInRange(value any, fromChapter, toChapter int) bool {
	switch typed := value.(type) {
	case map[string]any:
		if n := intValue(typed["chapter"]); n > 0 &&
			(fromChapter == 0 || n >= fromChapter) && (toChapter == 0 || n <= toChapter) {
			return true
		}
		for _, nested := range typed {
			if stateRecordHasChapterInRange(nested, fromChapter, toChapter) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if stateRecordHasChapterInRange(nested, fromChapter, toChapter) {
				return true
			}
		}
	}
	return false
}

// validateEntityReferences rejects entity records whose references point
// outside the known entity/character ID space, both at write time and in the
// full health check, so a dangling reference can never be committed.
func validateEntityReferences(entities []map[string]any, characterIDs map[string]bool) error {
	ids := map[string]bool{}
	byID := map[string]map[string]any{}
	boundApprovals := map[string]int{}
	for _, entity := range entities {
		eid, err := idFromRecord(entity)
		if err != nil {
			return err
		}
		ids[eid] = true
		byID[eid] = entity
	}
	for id := range characterIDs {
		ids[id] = true
	}
	for _, entity := range entities {
		eid, _ := entity["id"].(string)
		kind, _ := entity["kind"].(string)
		if kind == "audit-decision" || kind == "state-approval" {
			target, _ := entity["targetId"].(string)
			targetEntity := byID[target]
			if targetEntity == nil {
				return fmt.Errorf("%s %s 引用的目标不存在：%s", kind, eid, target)
			}
			wanted := "audit"
			if kind == "state-approval" {
				wanted = "state-proposal"
			}
			if targetEntity["kind"] != wanted {
				return fmt.Errorf("%s %s 只能引用 %s", kind, eid, wanted)
			}
			if semanticRecordIsBound(entity) {
				if err := verifyTargetBinding(entity, targetEntity, "targetRevision", "targetContentSha256", kind+" "+eid); err != nil {
					return err
				}
				if kind == "state-approval" {
					boundApprovals[target]++
					if boundApprovals[target] > 1 {
						return fmt.Errorf("状态候选 %s 存在重复或冲突批准；请新建候选后重新批准", target)
					}
				}
			}
		}
		if kind == "audit" {
			if supersedesID, ok := entity["supersedesId"].(string); ok && supersedesID != "" {
				target := byID[supersedesID]
				if target == nil || target["kind"] != "audit" || supersedesID == eid {
					return fmt.Errorf("审计更正 %s 的 supersedesId 必须指向既有其他 audit", eid)
				}
				if err := verifyTargetBinding(entity, target, "supersededRevision", "supersededContentSha256", "审计更正 "+eid); err != nil {
					return err
				}
			}
		}
		if kind == "state" {
			proposalID, hasProposal := entity["proposalId"].(string)
			approvalID, hasApproval := entity["approvalId"].(string)
			// A proposalId without approvalId is a legacy 4.9.0/4.9.1 state.
			// It remains readable, but new writes can no longer create this form.
			if hasApproval || (!hasProposal && entity["approvalId"] != nil) {
				if !hasProposal || !safeID.MatchString(proposalID) || !hasApproval || !safeID.MatchString(approvalID) {
					return fmt.Errorf("正史状态 %s 的 proposalId/approvalId 绑定无效", eid)
				}
				proposal := byID[proposalID]
				approval := byID[approvalID]
				if proposal == nil || proposal["kind"] != "state-proposal" || approval == nil || approval["kind"] != "state-approval" {
					return fmt.Errorf("正史状态 %s 的候选或批准不存在", eid)
				}
				proposalRevision, proposalSHA, err := requireBoundSemanticRecord(proposal, "正史状态候选")
				if err != nil {
					return err
				}
				approvalRevision, approvalSHA, err := requireBoundSemanticRecord(approval, "正史状态批准")
				if err != nil {
					return err
				}
				if approval["targetId"] != proposalID || approval["decision"] != "approved" {
					return fmt.Errorf("正史状态 %s 没有绑定 approved 批准", eid)
				}
				if err := verifyTargetBinding(approval, proposal, "targetRevision", "targetContentSha256", "正史状态批准"); err != nil {
					return err
				}
				if strings.TrimSpace(strValue(entity["authorDecision"])) != strings.TrimSpace(strValue(approval["authorDecision"])) {
					return fmt.Errorf("正史状态 %s 的作者决定与批准不一致", eid)
				}
				payloadSHA, err := statePayloadSHA(entity)
				if err != nil || payloadSHA != proposal[statePayloadSHAField] || entity[statePayloadSHAField] != payloadSHA {
					return fmt.Errorf("正史状态 %s 的 payload 与候选不一致", eid)
				}
				if revision, ok := semanticInt(entity, "proposalRevision"); !ok || revision != proposalRevision || entity["proposalContentSha256"] != proposalSHA {
					return fmt.Errorf("正史状态 %s 的候选 provenance 不一致", eid)
				}
				if revision, ok := semanticInt(entity, "approvalRevision"); !ok || revision != approvalRevision || entity["approvalContentSha256"] != approvalSHA {
					return fmt.Errorf("正史状态 %s 的批准 provenance 不一致", eid)
				}
			}
		}
		if subject, _ := entity["subjectId"].(string); subject != "" && !ids[subject] {
			return fmt.Errorf("状态实体 %s 的 subjectId 引用缺失：%s", eid, subject)
		}
		for _, field := range []string{"relations", "ownershipHistory", "conditionHistory", "transfers"} {
			for _, raw := range charSlice(entity, field) {
				entry, ok := raw.(map[string]any)
				if !ok {
					return fmt.Errorf("实体 %s 的 %s 条目无效", eid, field)
				}
				for _, key := range []string{"targetId", "ownerId", "subjectId"} {
					if value, _ := entry[key].(string); value != "" && !ids[value] {
						return fmt.Errorf("实体 %s 的 %s.%s 引用缺失：%s", eid, field, key, value)
					}
				}
			}
		}
	}
	return nil
}
