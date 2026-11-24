package main

import (
	"errors"
	"fmt"
	"strings"
)

var characterBeliefTypes = map[string]bool{
	"fact": true, "rumor": true, "lie": true, "secret": true,
	"uncertain": true, "provisional": true,
}

func validateEvidenceItem(item map[string]any, chapters map[string]string, label string) error {
	chapterID, _ := item["sourceChapterId"].(string)
	if chapterID == "" {
		chapterID, _ = item["learnedChapterId"].(string)
	}
	if chapterID == "" {
		chapterID, _ = item["chapterId"].(string)
	}
	if chapterID == "" {
		chapterID, _ = item["fromChapterId"].(string)
	}
	if chapterID == "" {
		chapterID, _ = item["toChapterId"].(string)
	}
	if chapterID == "" {
		chapterID, _ = item["sinceChapterId"].(string)
	}
	if !safeID.MatchString(chapterID) {
		return fmt.Errorf("%s 缺少有效章节 ID", label)
	}
	quote, _ := item["evidence"].(string)
	if strings.TrimSpace(quote) == "" || !strings.Contains(chapters[chapterID], quote) {
		return fmt.Errorf("%s 缺少已存章节逐字证据", label)
	}
	return nil
}

func validateCharacter(character map[string]any, chapters map[string]string, known map[string]bool) error {
	if _, err := idFromRecord(character); err != nil {
		return err
	}
	name, _ := character["name"].(string)
	if strings.TrimSpace(name) == "" {
		return errors.New("人物姓名为空")
	}
	if age, ok := character["age"]; ok {
		switch age.(type) {
		case string, float64:
		default:
			return errors.New("人物年龄必须是字符串或数字")
		}
	}
	for _, field := range []string{"cognition", "knowledge", "beliefs"} {
		raw, exists := character[field]
		if !exists {
			continue
		}
		items, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("%s 必须是数组", field)
		}
		for _, item := range items {
			entry, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("%s 条目必须是对象", field)
			}
			beliefType, _ := entry["beliefType"].(string)
			if beliefType == "" {
				beliefType, _ = entry["status"].(string)
			}
			if beliefType == "" {
				beliefType = "fact"
			}
			if !characterBeliefTypes[beliefType] {
				return fmt.Errorf("%s 认知类型无效：%s", field, beliefType)
			}
			if factID, _ := entry["factId"].(string); factID != "" && !safeID.MatchString(factID) {
				return fmt.Errorf("%s factId 无效", field)
			}
			if err := validateEvidenceItem(entry, chapters, field); err != nil {
				return err
			}
		}
	}
	for _, field := range []string{"secrets", "lies", "rumors"} {
		raw, exists := character[field]
		if !exists {
			continue
		}
		items, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("%s 必须是数组", field)
		}
		for _, item := range items {
			entry, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("%s 条目必须是对象", field)
			}
			if rawKnown, exists := entry["knownBy"]; exists {
				knownIDs, ok := rawKnown.([]any)
				if !ok {
					return fmt.Errorf("%s knownBy 必须是数组", field)
				}
				for _, rawID := range knownIDs {
					id, _ := rawID.(string)
					if !known[id] {
						return fmt.Errorf("%s knownBy 引用未知人物", field)
					}
				}
			}
			if err := validateEvidenceItem(entry, chapters, field); err != nil {
				return err
			}
		}
	}
	for _, field := range []string{"relationships", "injuries"} {
		raw, exists := character[field]
		if !exists {
			continue
		}
		items, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("%s 必须是数组", field)
		}
		for _, item := range items {
			entry, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("%s 条目必须是对象", field)
			}
			if target, _ := entry["targetId"].(string); target != "" && !known[target] {
				return fmt.Errorf("%s 引用未知人物", field)
			}
			if err := validateEvidenceItem(entry, chapters, field); err != nil {
				return err
			}
		}
	}
	if rawDeath, exists := character["death"]; exists {
		// death may be an object (state) or an array (history).
		switch death := rawDeath.(type) {
		case map[string]any:
			if status, _ := death["status"].(string); status != "" && status != "alive" && status != "dead" && status != "unknown" {
				return errors.New("人物死亡状态无效")
			}
			_, hasEvidence := death["evidence"]
			hasChapter := false
			for _, key := range []string{"sourceChapterId", "learnedChapterId", "chapterId", "fromChapterId", "toChapterId", "sinceChapterId"} {
				if _, exists := death[key]; exists {
					hasChapter = true
					break
				}
			}
			if status, _ := death["status"].(string); status == "dead" || hasEvidence || hasChapter {
				if err := validateEvidenceItem(death, chapters, "death"); err != nil {
					return err
				}
			}
		case []any:
			for _, raw := range death {
				entry, ok := raw.(map[string]any)
				if !ok {
					return errors.New("death 条目必须是对象")
				}
				status, _ := entry["status"].(string)
				if status != "alive" && status != "dead" && status != "unknown" {
					return errors.New("人物死亡状态无效")
				}
				if err := validateEvidenceItem(entry, chapters, "death"); err != nil {
					return err
				}
			}
		default:
			return errors.New("death 必须是对象或数组")
		}
	}
	return nil
}

func immutableAuditKind(kind string) bool {
	switch kind {
	case "audit", "audit-decision", "state-proposal", "state-approval":
		return true
	}
	return false
}

func validateEntity(entity map[string]any, chapterText map[string]string) error {
	if _, err := idFromRecord(entity); err != nil {
		return err
	}
	name, _ := entity["name"].(string)
	if strings.TrimSpace(name) == "" {
		return errors.New("世界实体名称为空")
	}
	kind, _ := entity["kind"].(string)
	switch kind {
	case "organization", "place", "rule", "item", "document", "state", "volume", "audit", "audit-decision", "state-proposal", "state-approval":
	default:
		return errors.New("世界实体类型无效")
	}
	status, _ := entity["status"].(string)
	switch status {
	case "plan", "candidate", "canon":
	default:
		return errors.New("世界实体事实层级无效")
	}
	// Semantic audits and state candidates are advisory records produced by a
	// model or author. They may never be stored as canon; promotion requires an
	// explicit author decision and the existing state-transition gate.
	if kind == "audit" || kind == "audit-decision" || kind == "state-proposal" || kind == "state-approval" {
		if status != "candidate" {
			return errors.New("审计与状态候选只能是 candidate，不能自动晋升正史")
		}
		if err := validateSemanticBinding(entity); err != nil {
			return err
		}
	}
	switch kind {
	case "audit":
		auditType, _ := entity["auditType"].(string)
		switch auditType {
		case "continuity", "causality", "character", "timeline", "other":
		default:
			return errors.New("语义审计类型无效")
		}
		result, _ := entity["result"].(string)
		switch result {
		case "potential_conflict", "consistent", "uncertain":
		default:
			return errors.New("语义审计结论无效")
		}
		uncertainty, _ := entity["uncertainty"].(string)
		if uncertainty != "low" && uncertainty != "medium" && uncertainty != "high" {
			return errors.New("语义审计不确定性无效")
		}
		_, hasKeep := entity["keepTimelineSha256"]
		_, hasOther := entity["otherTimelineSha256"]
		if hasKeep || hasOther {
			keep, keepOK := validSemanticSHA(entity["keepTimelineSha256"])
			other, otherOK := validSemanticSHA(entity["otherTimelineSha256"])
			if auditType != "timeline" || result != "potential_conflict" || !keepOK || !otherOK || keep == other {
				return errors.New("时间线裁定需绑定两条不同记录的 SHA-256 及 timeline/potential_conflict 审计")
			}
		}
		if claim, _ := entity["claim"].(string); strings.TrimSpace(claim) == "" {
			return errors.New("语义审计缺少待核对主张")
		}
		chapterID, _ := entity["sourceChapterId"].(string)
		quote, _ := entity["evidence"].(string)
		if quote == "" || !strings.Contains(chapterText[chapterID], quote) {
			return errors.New("语义审计缺少当前章节的逐字证据")
		}
		supersedesRaw, hasSupersedes := entity["supersedesId"]
		_, hasSupersededRevision := entity["supersededRevision"]
		_, hasSupersededSHA := entity["supersededContentSha256"]
		if hasSupersedes {
			supersedesID, ok := supersedesRaw.(string)
			if !ok || !safeID.MatchString(supersedesID) {
				return errors.New("审计更正 supersedesId 无效")
			}
			if revision, ok := semanticInt(entity, "supersededRevision"); !ok || revision < 1 {
				return errors.New("审计更正 supersededRevision 无效")
			}
			if _, ok := validSemanticSHA(entity["supersededContentSha256"]); !ok {
				return errors.New("审计更正 supersededContentSha256 无效")
			}
		} else if hasSupersededRevision || hasSupersededSHA {
			return errors.New("审计更正绑定缺少 supersedesId")
		}
	case "state-proposal":
		subject, _ := entity["subjectId"].(string)
		if !safeID.MatchString(subject) {
			return errors.New("状态候选缺少有效 subjectId")
		}
		stateType, _ := entity["stateType"].(string)
		switch stateType {
		case "location", "inventory", "debt", "promise", "injury", "travel", "condition":
		default:
			return errors.New("状态候选的 stateType 无效")
		}
		value, hasValue := entity["value"]
		if !hasValue || !stateTransitionValueValid(value) {
			return errors.New("状态候选 value 无效")
		}
		chapterID, _ := entity["sourceChapterId"].(string)
		quote, _ := entity["evidence"].(string)
		if quote == "" || !strings.Contains(chapterText[chapterID], quote) {
			return errors.New("状态候选缺少当前章节的逐字证据")
		}
		if uncertainty, _ := entity["uncertainty"].(string); uncertainty != "low" && uncertainty != "medium" && uncertainty != "high" {
			return errors.New("状态候选不确定性无效")
		}
	case "audit-decision", "state-approval":
		target, _ := entity["targetId"].(string)
		if !safeID.MatchString(target) {
			return fmt.Errorf("%s 缺少有效 targetId", kind)
		}
		if decision, _ := entity["authorDecision"].(string); strings.TrimSpace(decision) == "" {
			return fmt.Errorf("%s 缺少作者决定记录", kind)
		}
		if semanticRecordIsBound(entity) {
			if revision, ok := semanticInt(entity, "targetRevision"); !ok || revision < 1 {
				return fmt.Errorf("%s targetRevision 无效", kind)
			}
			if _, ok := validSemanticSHA(entity["targetContentSha256"]); !ok {
				return fmt.Errorf("%s targetContentSha256 无效", kind)
			}
		}
		if kind == "audit-decision" {
			value, _ := entity["decision"].(string)
			if value != "confirmed" && value != "rejected" {
				return errors.New("审计决定必须是 confirmed 或 rejected")
			}
		} else if semanticRecordIsBound(entity) {
			value, _ := entity["decision"].(string)
			if value != "approved" && value != "rejected" {
				return errors.New("状态批准决定必须是 approved 或 rejected")
			}
		}
	}
	if status == "canon" {
		chapterID, _ := entity["sourceChapterId"].(string)
		quote, _ := entity["evidence"].(string)
		if quote == "" || !strings.Contains(chapterText[chapterID], quote) {
			return errors.New("正史实体缺少已保存章节的逐字证据")
		}
	}
	for _, field := range []string{"relations", "ownershipHistory", "conditionHistory", "transfers"} {
		raw, exists := entity[field]
		if !exists {
			continue
		}
		items, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("%s 必须是数组", field)
		}
		for _, item := range items {
			entry, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("%s 条目必须是对象", field)
			}
			if target, _ := entry["targetId"].(string); target != "" && !safeID.MatchString(target) {
				return fmt.Errorf("%s targetId 无效", field)
			}
			if owner, _ := entry["ownerId"].(string); owner != "" && !safeID.MatchString(owner) {
				return fmt.Errorf("%s ownerId 无效", field)
			}
			if kindValue, _ := entry["kind"].(string); field == "relations" && strings.TrimSpace(kindValue) == "" {
				return errors.New("实体关系缺少关系类型")
			}
			if err := validateEvidenceItem(entry, chapterText, field); err != nil {
				return err
			}
		}
	}
	if kind == "state" {
		if subject, _ := entity["subjectId"].(string); subject != "" && !safeID.MatchString(subject) {
			return errors.New("状态实体 subjectId 无效")
		}
		if stateType, _ := entity["stateType"].(string); stateType != "" {
			switch stateType {
			case "location", "inventory", "debt", "promise", "injury", "travel", "condition":
			default:
				return errors.New("状态实体 stateType 无效")
			}
		}
	}
	return nil
}
