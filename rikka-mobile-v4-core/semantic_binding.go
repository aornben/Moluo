package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	recordedRevisionField = "recordedRevision"
	contentSHA256Field    = "contentSha256"
	statePayloadSHAField  = "statePayloadSha256"
)

var statePayloadFields = []string{
	"name",
	"subjectId",
	"stateType",
	"value",
	"sourceChapterId",
	"evidence",
	"allowInitial",
	"expectedPreviousStateId",
	"expectedPreviousValue",
}

func semanticInt(record map[string]any, field string) (int, bool) {
	raw, exists := record[field]
	if !exists {
		return 0, false
	}
	switch value := raw.(type) {
	case int:
		return value, true
	case float64:
		converted := intValue(value)
		if converted < 0 {
			return 0, false
		}
		return converted, true
	default:
		return 0, false
	}
}

func validSemanticSHA(value any) (string, bool) {
	text, ok := value.(string)
	if !ok || len(text) != 64 || text != strings.ToLower(text) {
		return "", false
	}
	decoded, err := hex.DecodeString(text)
	return text, err == nil && len(decoded) == 32
}

func semanticRecordContentSHA(record map[string]any) (string, error) {
	canonical := copyMap(record)
	delete(canonical, contentSHA256Field)
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	return sumBytes(data), nil
}

func statePayload(record map[string]any) map[string]any {
	payload := map[string]any{}
	for _, field := range statePayloadFields {
		if value, exists := record[field]; exists {
			payload[field] = value
		}
	}
	return payload
}

func statePayloadSHA(record map[string]any) (string, error) {
	data, err := json.Marshal(statePayload(record))
	if err != nil {
		return "", err
	}
	return sumBytes(data), nil
}

func semanticRecordIsBound(record map[string]any) bool {
	_, hasRevision := record[recordedRevisionField]
	_, hasSHA := record[contentSHA256Field]
	return hasRevision || hasSHA
}

// validateSemanticBinding accepts completely unbound legacy records for
// read-only compatibility. Once any binding field is present, the binding must
// be complete and cryptographically self-consistent.
func validateSemanticBinding(record map[string]any) error {
	kind, _ := record["kind"].(string)
	if !immutableAuditKind(kind) {
		return nil
	}
	if !semanticRecordIsBound(record) {
		if _, exists := record[statePayloadSHAField]; exists {
			return errors.New("旧语义记录不能单独携带状态 payload SHA-256")
		}
		return nil
	}
	revision, ok := semanticInt(record, recordedRevisionField)
	if !ok || revision < 1 {
		return errors.New("语义记录 recordedRevision 无效")
	}
	stored, ok := validSemanticSHA(record[contentSHA256Field])
	if !ok {
		return errors.New("语义记录 contentSha256 无效")
	}
	actual, err := semanticRecordContentSHA(record)
	if err != nil {
		return err
	}
	if stored != actual {
		return errors.New("语义记录 contentSha256 与不可变内容不一致")
	}
	if kind == "state-proposal" {
		storedPayload, ok := validSemanticSHA(record[statePayloadSHAField])
		if !ok {
			return errors.New("状态候选缺少有效 statePayloadSha256")
		}
		actualPayload, err := statePayloadSHA(record)
		if err != nil {
			return err
		}
		if storedPayload != actualPayload {
			return errors.New("状态候选 statePayloadSha256 与内容不一致")
		}
	} else if _, exists := record[statePayloadSHAField]; exists {
		return fmt.Errorf("%s 不得伪造 statePayloadSha256", kind)
	}
	return nil
}

func stampSemanticRecord(record map[string]any, revision int) (map[string]any, error) {
	if revision < 1 {
		return nil, errors.New("语义记录 revision 无效")
	}
	for _, field := range []string{recordedRevisionField, contentSHA256Field, statePayloadSHAField} {
		if _, exists := record[field]; exists {
			return nil, fmt.Errorf("%s 由程序生成，候选不得提供", field)
		}
	}
	stamped := copyMap(record)
	stamped[recordedRevisionField] = revision
	if stamped["kind"] == "state-proposal" {
		payloadSHA, err := statePayloadSHA(stamped)
		if err != nil {
			return nil, err
		}
		stamped[statePayloadSHAField] = payloadSHA
	}
	contentSHA, err := semanticRecordContentSHA(stamped)
	if err != nil {
		return nil, err
	}
	stamped[contentSHA256Field] = contentSHA
	return stamped, validateSemanticBinding(stamped)
}

func requireBoundSemanticRecord(record map[string]any, label string) (int, string, error) {
	if !semanticRecordIsBound(record) {
		return 0, "", fmt.Errorf("%s 是旧版未绑定记录；请追加新的不可变记录", label)
	}
	if err := validateSemanticBinding(record); err != nil {
		return 0, "", fmt.Errorf("%s 绑定无效：%w", label, err)
	}
	revision, _ := semanticInt(record, recordedRevisionField)
	contentSHA, _ := record[contentSHA256Field].(string)
	return revision, contentSHA, nil
}

func verifyTargetBinding(record, target map[string]any, revisionField, shaField, label string) error {
	targetRevision, targetSHA, err := requireBoundSemanticRecord(target, label)
	if err != nil {
		return err
	}
	wantedRevision, ok := semanticInt(record, revisionField)
	if !ok || wantedRevision != targetRevision {
		return fmt.Errorf("%s %s 与目标记录不一致", label, revisionField)
	}
	wantedSHA, ok := validSemanticSHA(record[shaField])
	if !ok || wantedSHA != targetSHA {
		return fmt.Errorf("%s %s 与目标记录不一致", label, shaField)
	}
	return nil
}
