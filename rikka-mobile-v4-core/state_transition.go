package main

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// mutationOperationRecord keeps the common operation audit small while
// retaining the author's explicit decision for causal state transitions.
func mutationOperationRecord(d recordDraft, inputHash string, revision int, recordID string) opRecord {
	record := opRecord{OperationID: d.OperationID, InputSHA256: inputHash,
		Revision: revision, RecordID: recordID, Kind: d.Kind}
	if d.Kind == "state-transition" {
		record.AuthorDecision, _ = d.Record["authorDecision"].(string)
		record.AuthorDecision = strings.TrimSpace(record.AuthorDecision)
		record.ProposalID, _ = d.Record["proposalId"].(string)
		record.ApprovalID, _ = d.Record["approvalId"].(string)
		record.ProposalRevision, _ = semanticInt(d.Record, "proposalRevision")
		record.ApprovalRevision, _ = semanticInt(d.Record, "approvalRevision")
		record.ProposalContentSHA256, _ = d.Record["proposalContentSha256"].(string)
		record.ApprovalContentSHA256, _ = d.Record["approvalContentSha256"].(string)
		record.StatePayloadSHA256, _ = d.Record[statePayloadSHAField].(string)
	}
	return record
}

func verifyCommittedStateTransition(root string, m manifest, d recordDraft) (map[string]any, error) {
	if m.EntitiesPath == "" {
		return nil, errors.New("HEAD 已提交状态转换，但实体索引缺失")
	}
	recordID, err := idFromRecord(d.Record)
	if err != nil {
		return nil, err
	}
	entities, err := currentRecords(root, m.EntitiesPath)
	if err != nil {
		return nil, err
	}
	for _, entity := range entities {
		if entity["id"] == recordID && entity["kind"] == "state" && entity["status"] == "canon" &&
			committedRecordMatches(entity, d.Record) {
			return entity, nil
		}
	}
	return nil, errors.New("状态转换已提交，但目标正史状态不存在或审计不一致")
}

func stateTransitionValueValid(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) != ""
	case float64, bool:
		return true
	case []any:
		if len(typed) == 0 {
			return false
		}
		for _, item := range typed {
			if item == nil {
				return false
			}
		}
		return true
	case map[string]any:
		return len(typed) > 0
	default:
		return false
	}
}

func latestCanonicalSubjectState(entities []map[string]any, chapterNumbers map[string]int,
	subjectID, stateType string) (map[string]any, int) {
	var latest map[string]any
	latestChapter := -1
	for _, entity := range entities {
		if entity["kind"] != "state" || entity["status"] != "canon" ||
			entity["subjectId"] != subjectID || entity["stateType"] != stateType {
			continue
		}
		chapterID, _ := entity["sourceChapterId"].(string)
		chapter := chapterNumbers[chapterID]
		if latest == nil || chapter >= latestChapter {
			latest = entity
			latestChapter = chapter
		}
	}
	return latest, latestChapter
}

// prepareStateTransition turns an explicitly authorised causal transition into
// a normal immutable canon state entity. It validates the current state before
// any transaction file is created, so a rejected precondition cannot partially
// change the entity index.
func (w *workspace) prepareStateTransition(root string, m manifest, d recordDraft) ([]map[string]any, map[string]any, error) {
	if !d.Confirm {
		return nil, nil, errors.New("因果状态转换需要明确确认")
	}
	decision, _ := d.Record["authorDecision"].(string)
	if strings.TrimSpace(decision) == "" {
		return nil, nil, errors.New("因果状态转换缺少作者决定记录")
	}
	recordID, err := idFromRecord(d.Record)
	if err != nil {
		return nil, nil, err
	}
	subjectID, _ := d.Record["subjectId"].(string)
	if !safeID.MatchString(subjectID) {
		return nil, nil, errors.New("因果状态转换 subjectId 无效")
	}
	stateType, _ := d.Record["stateType"].(string)
	if strings.TrimSpace(stateType) == "" {
		return nil, nil, errors.New("因果状态转换缺少 stateType")
	}
	value, hasValue := d.Record["value"]
	if !hasValue || !stateTransitionValueValid(value) {
		return nil, nil, errors.New("因果状态转换 value 无效")
	}
	if raw, exists := d.Record["kind"]; exists && raw != "state" {
		return nil, nil, errors.New("因果状态转换不能伪装实体类型")
	}
	if raw, exists := d.Record["status"]; exists && raw != "canon" {
		return nil, nil, errors.New("因果状态转换只能写入正史状态")
	}

	entities := []map[string]any{}
	if m.EntitiesPath != "" {
		entities, err = currentRecords(root, m.EntitiesPath)
		if err != nil {
			return nil, nil, err
		}
	}
	for _, entity := range entities {
		if entity["id"] == recordID {
			return nil, nil, errors.New("因果状态转换 ID 已存在")
		}
	}

	entries, err := w.entries(root, m)
	if err != nil {
		return nil, nil, err
	}
	chapterNumbers := map[string]int{}
	chapterText := map[string]string{}
	for _, entry := range entries {
		chapter, err := loadChapter(root, entry.Path)
		if err != nil {
			return nil, nil, err
		}
		chapterNumbers[entry.ID] = entry.Number
		chapterText[entry.ID] = chapter.Content
	}
	sourceChapterID, _ := d.Record["sourceChapterId"].(string)
	sourceChapter := chapterNumbers[sourceChapterID]
	if sourceChapter == 0 {
		return nil, nil, errors.New("因果状态转换引用的证据章节不存在")
	}

	transition := copyMap(d.Record)
	transition["kind"] = "state"
	transition["status"] = "canon"
	transition["authorDecision"] = strings.TrimSpace(decision)
	if err := validateEntity(transition, chapterText); err != nil {
		return nil, nil, err
	}
	for _, field := range []string{"proposalRevision", "proposalContentSha256", "approvalRevision", "approvalContentSha256", statePayloadSHAField} {
		if _, exists := d.Record[field]; exists {
			return nil, nil, fmt.Errorf("%s 由程序生成，状态转换候选不得提供", field)
		}
	}
	// A model-proposed state candidate becomes canon only through one exact,
	// immutable author approval. Without either ID, the historical direct-author
	// path remains available for an author who explicitly confirms the mutation.
	proposalRaw, hasProposal := d.Record["proposalId"]
	approvalRaw, hasApproval := d.Record["approvalId"]
	if hasProposal != hasApproval {
		return nil, nil, errors.New("proposalId 与 approvalId 必须同时提供")
	}
	if hasProposal {
		proposalID, proposalOK := proposalRaw.(string)
		approvalID, approvalOK := approvalRaw.(string)
		if !proposalOK || !safeID.MatchString(proposalID) {
			return nil, nil, errors.New("状态候选 ID 无效")
		}
		if !approvalOK || !safeID.MatchString(approvalID) {
			return nil, nil, errors.New("状态批准 ID 无效")
		}
		var proposal, approval map[string]any
		approvalCount := 0
		for _, entity := range entities {
			if entity["id"] == proposalID && entity["kind"] == "state-proposal" {
				proposal = entity
			}
			if entity["kind"] == "state-approval" && entity["targetId"] == proposalID {
				approvalCount++
				if entity["id"] == approvalID {
					approval = entity
				}
			}
		}
		if proposal == nil {
			return nil, nil, errors.New("状态候选不存在")
		}
		if proposal["status"] != "candidate" {
			return nil, nil, errors.New("状态候选已不是 candidate，拒绝提交")
		}
		if approvalCount != 1 || approval == nil {
			return nil, nil, errors.New("状态候选必须且只能绑定一条明确的 approvalId；请新建候选后重新批准")
		}
		proposalRevision, proposalSHA, err := requireBoundSemanticRecord(proposal, "状态候选")
		if err != nil {
			return nil, nil, err
		}
		approvalRevision, approvalSHA, err := requireBoundSemanticRecord(approval, "状态批准")
		if err != nil {
			return nil, nil, err
		}
		if err := verifyTargetBinding(approval, proposal, "targetRevision", "targetContentSha256", "状态批准"); err != nil {
			return nil, nil, err
		}
		if approval["decision"] != "approved" {
			return nil, nil, errors.New("所选状态批准不是 approved，拒绝提交正史")
		}
		approvalDecision, _ := approval["authorDecision"].(string)
		if strings.TrimSpace(approvalDecision) != strings.TrimSpace(decision) {
			return nil, nil, errors.New("状态候选授权与本次作者决定不一致")
		}
		proposalPayloadSHA, _ := proposal[statePayloadSHAField].(string)
		transitionPayloadSHA, err := statePayloadSHA(d.Record)
		if err != nil {
			return nil, nil, err
		}
		if proposalPayloadSHA == "" || proposalPayloadSHA != transitionPayloadSHA {
			return nil, nil, errors.New("状态候选与提交的完整状态 payload 不一致")
		}
		transition["proposalRevision"] = proposalRevision
		transition["proposalContentSha256"] = proposalSHA
		transition["approvalRevision"] = approvalRevision
		transition["approvalContentSha256"] = approvalSHA
		transition[statePayloadSHAField] = transitionPayloadSHA
	}

	characters, err := currentRecords(root, m.CharactersPath)
	if err != nil {
		return nil, nil, err
	}
	characterIDs, err := ensureCharacters(characters)
	if err != nil {
		return nil, nil, err
	}
	if err := validateEntityReferences(append(append([]map[string]any{}, entities...), transition), characterIDs); err != nil {
		return nil, nil, err
	}

	previous, previousChapter := latestCanonicalSubjectState(entities, chapterNumbers, subjectID, stateType)
	allowInitial := d.Record["allowInitial"] == true
	expectedID, _ := d.Record["expectedPreviousStateId"].(string)
	expectedValue, hasExpectedValue := d.Record["expectedPreviousValue"]
	if previous == nil {
		if !allowInitial || expectedID != "" || hasExpectedValue {
			return nil, nil, errors.New("没有既有状态；首次状态必须明确 allowInitial 且不得伪造前置状态")
		}
	} else {
		previousID, _ := previous["id"].(string)
		if allowInitial {
			return nil, nil, errors.New("已有状态，不能按首次状态写入")
		}
		if expectedID == "" || expectedID != previousID {
			return nil, nil, fmt.Errorf("前置状态冲突：当前 %s，预期 %s", previousID, expectedID)
		}
		if !hasExpectedValue || !reflect.DeepEqual(expectedValue, previous["value"]) {
			return nil, nil, errors.New("前置状态值冲突")
		}
		if reflect.DeepEqual(value, previous["value"]) {
			return nil, nil, errors.New("因果状态没有发生变化")
		}
		if sourceChapter < previousChapter {
			return nil, nil, errors.New("新状态的证据章节早于当前状态")
		}
	}
	return entities, transition, nil
}
