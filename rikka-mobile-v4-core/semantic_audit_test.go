package main

import (
	"fmt"
	"strings"
	"testing"
)

func semanticEntity(t *testing.T, fixture stateTransitionFixture, op string, record map[string]any) (any, error) {
	t.Helper()
	path := testRecordStage(t, fixture.w, fixture.id, op, "entity-add", record)
	return fixture.w.mutateFromFile(fixture.id, path, fixture.revision)
}

func semanticEntityConfirmed(t *testing.T, fixture stateTransitionFixture, op string, record map[string]any) (any, error) {
	t.Helper()
	path := writeConfirmedRecord(t, fixture.w, fixture.id, op, "entity-add", record)
	return fixture.w.mutateFromFile(fixture.id, path, fixture.revision)
}

func loadEntities(t *testing.T, fixture stateTransitionFixture) []map[string]any {
	t.Helper()
	root, _, m, err := fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	var entities []map[string]any
	if err := loadJSON(root, m.EntitiesPath, &entities); err != nil {
		t.Fatal(err)
	}
	return entities
}

func loadEntity(t *testing.T, fixture stateTransitionFixture, id string) map[string]any {
	t.Helper()
	for _, entity := range loadEntities(t, fixture) {
		if entity["id"] == id {
			return entity
		}
	}
	t.Fatalf("实体不存在：%s", id)
	return nil
}

func bindToTarget(t *testing.T, fixture stateTransitionFixture, record map[string]any, targetID string) map[string]any {
	t.Helper()
	target := loadEntity(t, fixture, targetID)
	bound := copyMap(record)
	bound["targetRevision"] = target[recordedRevisionField]
	bound["targetContentSha256"] = target[contentSHA256Field]
	return bound
}

func bindSupersedes(t *testing.T, fixture stateTransitionFixture, record map[string]any, targetID string) map[string]any {
	t.Helper()
	target := loadEntity(t, fixture, targetID)
	bound := copyMap(record)
	bound["supersedesId"] = targetID
	bound["supersededRevision"] = target[recordedRevisionField]
	bound["supersededContentSha256"] = target[contentSHA256Field]
	return bound
}

func TestSemanticAuditCandidateRequiresEvidenceAndAuthorDecision(t *testing.T) {
	fixture := newStateTransitionFixture(t)
	audit := map[string]any{
		"id": "audit_1", "name": "虚构连续性审计", "kind": "audit", "status": "candidate",
		"auditType": "continuity", "result": "potential_conflict",
		"claim": "铜钥匙同时出现在旧桥与钟楼", "subjectId": "item_key",
		"sourceChapterId": "ch_1", "evidence": "铜钥匙留在旧桥", "uncertainty": "medium",
		"model": "deepseek-unverified",
	}
	if _, err := semanticEntity(t, fixture, strings.Repeat("a", 32), audit); err != nil {
		t.Fatalf("合法审计候选被拒绝：%v", err)
	}
	fixture.revision++

	canon := copyMap(audit)
	canon["id"] = "audit_canon"
	canon["status"] = "canon"
	if _, err := semanticEntity(t, fixture, strings.Repeat("b", 32), canon); err == nil {
		t.Fatal("审计记录被允许写入正史")
	}
	badEvidence := copyMap(audit)
	badEvidence["id"] = "audit_bad"
	badEvidence["evidence"] = "正文中不存在的证据"
	if _, err := semanticEntity(t, fixture, strings.Repeat("c", 32), badEvidence); err == nil {
		t.Fatal("缺少正文证据的审计被接受")
	}
	missingTarget := map[string]any{
		"id": "audit_decision_missing", "name": "虚构审计决定", "kind": "audit-decision",
		"status": "candidate", "targetId": "audit_absent", "decision": "confirmed",
		"authorDecision": "作者确认",
	}
	if _, err := semanticEntityConfirmed(t, fixture, strings.Repeat("d", 32), missingTarget); err == nil {
		t.Fatal("引用不存在审计的决定被接受")
	}
	noDecision := map[string]any{
		"id": "audit_decision_1", "name": "虚构审计决定", "kind": "audit-decision",
		"status": "candidate", "targetId": "audit_1", "decision": "confirmed",
	}
	if _, err := semanticEntityConfirmed(t, fixture, strings.Repeat("e", 32), noDecision); err == nil {
		t.Fatal("缺少作者决定的审计决定被接受")
	}
	decision := bindToTarget(t, fixture, noDecision, "audit_1")
	decision["authorDecision"] = "作者确认这是需要修改的冲突"
	if _, err := semanticEntityConfirmed(t, fixture, strings.Repeat("f", 32), decision); err != nil {
		t.Fatalf("合法审计决定被拒绝：%v", err)
	}
	fixture.revision++

	// Audit corrections are new records bound to the original audit; the
	// original record is never rewritten.
	correction := copyMap(audit)
	correction["id"] = "audit_1_fix"
	correction["result"] = "consistent"
	correction = bindSupersedes(t, fixture, correction, "audit_1")
	if _, err := semanticEntity(t, fixture, fmt.Sprintf("%032x", 0xe100), correction); err != nil {
		t.Fatalf("审计更正被拒绝：%v", err)
	}
	fixture.revision++

	if loadEntity(t, fixture, "audit_1")["result"] != "potential_conflict" {
		t.Fatal("原始审计被更正覆盖")
	}
	if loadEntity(t, fixture, "audit_1")["status"] != "candidate" {
		t.Fatal("审计记录被自动晋升")
	}
	if _, err := fixture.w.doctor(fixture.id); err != nil {
		t.Fatalf("审计写入后健康检查失败：%v", err)
	}
}

func TestStateProposalNeedsAuthorApprovalBeforeCanon(t *testing.T) {
	fixture := newStateTransitionFixture(t)
	proposal := map[string]any{
		"id": "proposal_1", "name": "钥匙位置：旧桥", "kind": "state-proposal", "status": "candidate",
		"subjectId": "item_key", "stateType": "location", "value": "旧桥", "allowInitial": true,
		"sourceChapterId": "ch_1", "evidence": "铜钥匙留在旧桥", "uncertainty": "low",
	}
	if _, err := semanticEntity(t, fixture, fmt.Sprintf("%032x", 0xe200), proposal); err != nil {
		t.Fatalf("合法状态候选被拒绝：%v", err)
	}
	fixture.revision++

	transition := copyMap(initialLocationTransition())
	transition["proposalId"] = "proposal_1"
	path := stageStateTransition(t, fixture, fmt.Sprintf("%032x", 0xe201), transition, true)
	if _, err := fixture.w.mutateFromFile(fixture.id, path, fixture.revision); err == nil ||
		!strings.Contains(err.Error(), "同时提供") {
		t.Fatalf("只有 proposalId 的状态被提交：%v", err)
	}

	approval := bindToTarget(t, fixture, map[string]any{
		"id": "approval_1", "name": "虚构状态授权", "kind": "state-approval", "status": "candidate",
		"targetId": "proposal_1", "decision": "approved", "authorDecision": "作者确认钥匙初始位于旧桥",
	}, "proposal_1")
	path = testRecordStage(t, fixture.w, fixture.id, fmt.Sprintf("%032x", 0xe202), "entity-add", approval)
	if _, err := fixture.w.mutateFromFile(fixture.id, path, fixture.revision); err == nil ||
		!strings.Contains(err.Error(), "confirm") {
		t.Fatalf("未显式确认的批准被接受：%v", err)
	}
	if _, err := semanticEntityConfirmed(t, fixture, fmt.Sprintf("%032x", 0xe203), approval); err != nil {
		t.Fatalf("合法状态批准被拒绝：%v", err)
	}
	fixture.revision++

	// Duplicate approval for the same proposal must be refused explicitly.
	duplicate := bindToTarget(t, fixture, map[string]any{
		"id": "approval_1_dup", "name": "虚构状态授权", "kind": "state-approval", "status": "candidate",
		"targetId": "proposal_1", "decision": "approved", "authorDecision": "作者确认钥匙初始位于旧桥",
	}, "proposal_1")
	if _, err := semanticEntityConfirmed(t, fixture, fmt.Sprintf("%032x", 0xe204), duplicate); err == nil ||
		!strings.Contains(err.Error(), "重复或冲突") {
		t.Fatalf("重复批准未被拒绝：%v", err)
	}

	transition["approvalId"] = "approval_1"
	path = stageStateTransition(t, fixture, fmt.Sprintf("%032x", 0xe205), transition, true)
	if _, err := fixture.w.mutateFromFile(fixture.id, path, fixture.revision); err != nil {
		t.Fatalf("带唯一批准的候选状态提交失败：%v", err)
	}
	fixture.revision++

	// Wrong approvalId cannot be used to authorize the transition.
	other := map[string]any{
		"id": "proposal_other", "name": "虚构其他候选", "kind": "state-proposal", "status": "candidate",
		"subjectId": "item_key", "stateType": "location", "value": "钟楼",
		"sourceChapterId": "ch_2", "evidence": "铜钥匙带到钟楼", "uncertainty": "low",
	}
	if _, err := semanticEntity(t, fixture, fmt.Sprintf("%032x", 0xe206), other); err != nil {
		t.Fatal(err)
	}
	fixture.revision++
	approvalOther := bindToTarget(t, fixture, map[string]any{
		"id": "approval_other", "name": "虚构其他授权", "kind": "state-approval", "status": "candidate",
		"targetId": "proposal_other", "decision": "approved", "authorDecision": "作者确认钟楼",
	}, "proposal_other")
	if _, err := semanticEntityConfirmed(t, fixture, fmt.Sprintf("%032x", 0xe207), approvalOther); err != nil {
		t.Fatal(err)
	}
	fixture.revision++
	wrong := copyMap(nextLocationTransition())
	wrong["proposalId"] = "proposal_1"
	wrong["approvalId"] = "approval_other"
	wrong["authorDecision"] = "作者确认钥匙初始位于旧桥"
	path = stageStateTransition(t, fixture, fmt.Sprintf("%032x", 0xe208), wrong, true)
	if _, err := fixture.w.mutateFromFile(fixture.id, path, fixture.revision); err == nil {
		t.Fatal("错误 approvalId 被用于授权正史")
	}

	entities := loadEntities(t, fixture)
	proposalStatus := ""
	canon := map[string]any{}
	for _, entity := range entities {
		if entity["id"] == "proposal_1" {
			proposalStatus, _ = entity["status"].(string)
		}
		if entity["id"] == "state_key_bridge" && entity["kind"] == "state" && entity["status"] == "canon" {
			canon = entity
		}
	}
	if proposalStatus != "candidate" {
		t.Fatalf("状态候选被自动晋升：%q", proposalStatus)
	}
	if canon == nil || canon["proposalId"] != "proposal_1" || canon["approvalId"] != "approval_1" {
		t.Fatalf("正史状态缺少候选/批准追溯：%v", canon)
	}
	if canon["proposalContentSha256"] == "" || canon["approvalContentSha256"] == "" ||
		canon[statePayloadSHAField] == "" || canon["proposalRevision"] == nil || canon["approvalRevision"] == nil {
		t.Fatalf("正史状态缺少不可变绑定字段：%v", canon)
	}
	if _, err := fixture.w.doctor(fixture.id); err != nil {
		t.Fatalf("状态候选流程后健康检查失败：%v", err)
	}
}

func TestSemanticRecordsAreAppendOnly(t *testing.T) {
	fixture := newStateTransitionFixture(t)
	audit := map[string]any{
		"id": "audit_1", "name": "虚构审计", "kind": "audit", "status": "candidate",
		"auditType": "continuity", "result": "uncertain", "claim": "原始主张",
		"subjectId": "item_key", "sourceChapterId": "ch_1", "evidence": "铜钥匙留在旧桥",
		"uncertainty": "low",
	}
	if _, err := semanticEntity(t, fixture, fmt.Sprintf("%032x", 0xe300), audit); err != nil {
		t.Fatal(err)
	}
	fixture.revision++
	tampered := copyMap(audit)
	tampered["claim"] = "被篡改的主张"
	path := testRecordStage(t, fixture.w, fixture.id, fmt.Sprintf("%032x", 0xe301), "entity-update", tampered)
	if _, err := fixture.w.mutateFromFile(fixture.id, path, fixture.revision); err == nil ||
		!strings.Contains(err.Error(), "不可原地修改") {
		t.Fatalf("审计候选被允许原地修改：%v", err)
	}
	proposal := map[string]any{
		"id": "proposal_1", "name": "虚构候选", "kind": "state-proposal", "status": "candidate",
		"subjectId": "item_key", "stateType": "location", "value": "旧桥",
		"sourceChapterId": "ch_1", "evidence": "铜钥匙留在旧桥", "uncertainty": "low",
	}
	if _, err := semanticEntity(t, fixture, fmt.Sprintf("%032x", 0xe302), proposal); err != nil {
		t.Fatal(err)
	}
	fixture.revision++
	tampered = copyMap(proposal)
	tampered["value"] = "钟楼"
	path = testRecordStage(t, fixture.w, fixture.id, fmt.Sprintf("%032x", 0xe303), "entity-update", tampered)
	if _, err := fixture.w.mutateFromFile(fixture.id, path, fixture.revision); err == nil ||
		!strings.Contains(err.Error(), "不可原地修改") {
		t.Fatalf("状态候选被允许原地修改：%v", err)
	}
	approval := bindToTarget(t, fixture, map[string]any{
		"id": "approval_1", "name": "虚构授权", "kind": "state-approval", "status": "candidate",
		"targetId": "proposal_1", "decision": "approved", "authorDecision": "作者确认旧桥",
	}, "proposal_1")
	if _, err := semanticEntityConfirmed(t, fixture, fmt.Sprintf("%032x", 0xe304), approval); err != nil {
		t.Fatal(err)
	}
	fixture.revision++
	tampered = copyMap(approval)
	tampered["authorDecision"] = "作者确认钟楼"
	path = testRecordStage(t, fixture.w, fixture.id, fmt.Sprintf("%032x", 0xe305), "entity-update", tampered)
	if _, err := fixture.w.mutateFromFile(fixture.id, path, fixture.revision); err == nil ||
		!strings.Contains(err.Error(), "不可原地修改") {
		t.Fatalf("作者授权被允许原地修改：%v", err)
	}
	convert := map[string]any{"id": "item_key", "kind": "audit", "status": "candidate", "name": "伪审计"}
	path = testRecordStage(t, fixture.w, fixture.id, fmt.Sprintf("%032x", 0xe306), "entity-update", convert)
	if _, err := fixture.w.mutateFromFile(fixture.id, path, fixture.revision); err == nil ||
		!strings.Contains(err.Error(), "不能通过更新") {
		t.Fatalf("普通实体被允许改成审计类型：%v", err)
	}
	rename := map[string]any{"id": "item_key", "name": "铜钥匙（作者改名）"}
	path = testRecordStage(t, fixture.w, fixture.id, fmt.Sprintf("%032x", 0xe307), "entity-update", rename)
	if _, err := fixture.w.mutateFromFile(fixture.id, path, fixture.revision); err != nil {
		t.Fatalf("正常实体更新被误伤：%v", err)
	}
	fixture.revision++
	if _, err := fixture.w.doctor(fixture.id); err != nil {
		t.Fatalf("只追加修复后健康检查失败：%v", err)
	}
}

func TestConflictingStateApprovalsRejected(t *testing.T) {
	fixture := newStateTransitionFixture(t)
	proposal := map[string]any{
		"id": "proposal_conflict", "name": "虚构候选", "kind": "state-proposal", "status": "candidate",
		"subjectId": "item_key", "stateType": "location", "value": "旧桥",
		"sourceChapterId": "ch_1", "evidence": "铜钥匙留在旧桥", "uncertainty": "medium",
	}
	if _, err := semanticEntity(t, fixture, fmt.Sprintf("%032x", 0xe400), proposal); err != nil {
		t.Fatal(err)
	}
	fixture.revision++
	first := bindToTarget(t, fixture, map[string]any{
		"id": "approval_first", "name": "虚构授权", "kind": "state-approval", "status": "candidate",
		"targetId": "proposal_conflict", "decision": "approved", "authorDecision": "作者确认旧桥",
	}, "proposal_conflict")
	if _, err := semanticEntityConfirmed(t, fixture, fmt.Sprintf("%032x", 0xe401), first); err != nil {
		t.Fatal(err)
	}
	fixture.revision++
	second := bindToTarget(t, fixture, map[string]any{
		"id": "approval_second", "name": "虚构授权", "kind": "state-approval", "status": "candidate",
		"targetId": "proposal_conflict", "decision": "rejected", "authorDecision": "作者改为钟楼",
	}, "proposal_conflict")
	if _, err := semanticEntityConfirmed(t, fixture, fmt.Sprintf("%032x", 0xe402), second); err == nil ||
		!strings.Contains(err.Error(), "重复或冲突") {
		t.Fatalf("冲突批准未被拒绝：%v", err)
	}
}

func TestApprovalBindingRejectsWrongTarget(t *testing.T) {
	fixture := newStateTransitionFixture(t)
	proposal := map[string]any{
		"id": "proposal_bind", "name": "钥匙位置：旧桥", "kind": "state-proposal", "status": "candidate",
		"subjectId": "item_key", "stateType": "location", "value": "旧桥", "allowInitial": true,
		"sourceChapterId": "ch_1", "evidence": "铜钥匙留在旧桥", "uncertainty": "low",
	}
	if _, err := semanticEntity(t, fixture, fmt.Sprintf("%032x", 0xe500), proposal); err != nil {
		t.Fatal(err)
	}
	fixture.revision++
	target := loadEntity(t, fixture, "proposal_bind")
	base := map[string]any{
		"id": "approval_bind", "name": "虚构授权", "kind": "state-approval", "status": "candidate",
		"targetId": "proposal_bind", "targetContentSha256": target[contentSHA256Field],
		"decision": "approved", "authorDecision": "作者确认旧桥",
	}
	wrongRevision := copyMap(base)
	wrongRevision["targetRevision"] = intValue(target[recordedRevisionField]) + 1
	if _, err := semanticEntityConfirmed(t, fixture, fmt.Sprintf("%032x", 0xe501), wrongRevision); err == nil ||
		!strings.Contains(err.Error(), "targetRevision") {
		t.Fatalf("错误 targetRevision 的批准被接受：%v", err)
	}
	wrongSHA := copyMap(base)
	wrongSHA["targetRevision"] = target[recordedRevisionField]
	wrongSHA["targetContentSha256"] = strings.Repeat("0", 64)
	if _, err := semanticEntityConfirmed(t, fixture, fmt.Sprintf("%032x", 0xe502), wrongSHA); err == nil ||
		!strings.Contains(err.Error(), "targetContentSha256") {
		t.Fatalf("错误 targetContentSha256 的批准被接受：%v", err)
	}
}

func TestTransitionRejectsCallerProvidedProvenance(t *testing.T) {
	fixture := newStateTransitionFixture(t)
	proposal := map[string]any{
		"id": "proposal_prov", "name": "钥匙位置：旧桥", "kind": "state-proposal", "status": "candidate",
		"subjectId": "item_key", "stateType": "location", "value": "旧桥", "allowInitial": true,
		"sourceChapterId": "ch_1", "evidence": "铜钥匙留在旧桥", "uncertainty": "low",
	}
	if _, err := semanticEntity(t, fixture, fmt.Sprintf("%032x", 0xe600), proposal); err != nil {
		t.Fatal(err)
	}
	fixture.revision++
	approval := bindToTarget(t, fixture, map[string]any{
		"id": "approval_prov", "name": "虚构授权", "kind": "state-approval", "status": "candidate",
		"targetId": "proposal_prov", "decision": "approved", "authorDecision": "作者确认钥匙初始位于旧桥",
	}, "proposal_prov")
	if _, err := semanticEntityConfirmed(t, fixture, fmt.Sprintf("%032x", 0xe601), approval); err != nil {
		t.Fatal(err)
	}
	fixture.revision++
	transition := copyMap(initialLocationTransition())
	transition["proposalId"] = "proposal_prov"
	transition["approvalId"] = "approval_prov"
	transition["proposalRevision"] = 2
	path := stageStateTransition(t, fixture, fmt.Sprintf("%032x", 0xe602), transition, true)
	if _, err := fixture.w.mutateFromFile(fixture.id, path, fixture.revision); err == nil ||
		!strings.Contains(err.Error(), "不得提供") {
		t.Fatalf("调用方伪造 provenance 字段被接受：%v", err)
	}
}

func TestAuditCorrectionRejectsWrongBinding(t *testing.T) {
	fixture := newStateTransitionFixture(t)
	audit := map[string]any{
		"id": "audit_bind", "name": "虚构审计", "kind": "audit", "status": "candidate",
		"auditType": "continuity", "result": "uncertain", "claim": "原始主张",
		"subjectId": "item_key", "sourceChapterId": "ch_1", "evidence": "铜钥匙留在旧桥",
		"uncertainty": "low",
	}
	if _, err := semanticEntity(t, fixture, fmt.Sprintf("%032x", 0xe700), audit); err != nil {
		t.Fatal(err)
	}
	fixture.revision++
	target := loadEntity(t, fixture, "audit_bind")
	correction := copyMap(audit)
	correction["id"] = "audit_bind_fix"
	correction["supersedesId"] = "audit_bind"
	correction["supersededRevision"] = intValue(target[recordedRevisionField]) + 1
	correction["supersededContentSha256"] = target[contentSHA256Field]
	if _, err := semanticEntity(t, fixture, fmt.Sprintf("%032x", 0xe701), correction); err == nil ||
		!strings.Contains(err.Error(), "supersededRevision") {
		t.Fatalf("错误 supersededRevision 的审计更正被接受：%v", err)
	}
}
