package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type timelineRecord struct {
	ID            string `json:"id"`
	EventID       string `json:"eventId,omitempty"`
	Subject       string `json:"subject,omitempty"`
	Event         string `json:"event,omitempty"`
	Source        string `json:"sourceChapterId"`
	Evidence      string `json:"evidence"`
	Date          string `json:"date,omitempty"`
	StartDate     string `json:"startDate,omitempty"`
	EndDate       string `json:"endDate,omitempty"`
	TimeStatus    string `json:"timeStatus,omitempty"`
	ContentSHA256 string `json:"contentSha256"`
}

type timelineConflict struct {
	Severity string         `json:"severity"`
	Basis    string         `json:"basis"`
	First    timelineRecord `json:"first"`
	Second   timelineRecord `json:"second"`
	AuditID  string         `json:"auditId,omitempty"`
	Chosen   string         `json:"chosenContentSha256,omitempty"`
}

func timelineRecords(items []any, history bool) []timelineRecord {
	records := make([]timelineRecord, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue // Existing structural validators diagnose malformed records.
		}
		if history {
			if item["type"] != "timeline" {
				continue
			}
			item, _ = item["value"].(map[string]any)
		}
		field := func(key string) string {
			value, _ := item[key].(string)
			return strings.TrimSpace(value)
		}
		status := field("timeStatus")
		if status == "" {
			status = field("certainty")
		}
		event := field("event")
		if event == "" {
			event = field("description")
		}
		if event == "" {
			event = field("text")
		}
		record := timelineRecord{ID: field("id"), EventID: field("eventId"), Subject: field("subject"),
			Event: event, Source: field("sourceChapterId"), Evidence: field("evidence"), Date: field("date"),
			StartDate: field("startDate"), EndDate: field("endDate"), TimeStatus: status}
		if record.ID == "" {
			continue
		}
		data, err := json.Marshal(item)
		if err != nil {
			continue // Input records have already passed JSON decoding.
		}
		record.ContentSHA256 = sumBytes(data)
		records = append(records, record)
	}
	return records
}

func snapshotTimeline(memory map[string]any) []timelineRecord {
	active, _ := memory["timeline"].([]any)
	history, _ := memory["history"].([]any)
	return append(timelineRecords(active, false), timelineRecords(history, true)...)
}

func (r timelineRecord) bounds() (time.Time, time.Time, bool, bool) {
	if r.TimeStatus == "unknown" {
		return time.Time{}, time.Time{}, false, false
	}
	start, end := r.StartDate, r.EndDate
	if start == "" {
		start = r.Date
	}
	bounded := end != "" || r.TimeStatus != "approximate"
	if end == "" {
		end = start
	}
	a, err := parseISODate(start)
	b, endErr := parseISODate(end)
	return a, b, err == nil && endErr == nil && !b.Before(a), bounded
}

func compareTimeline(a, b timelineRecord) (timelineConflict, bool) {
	if a.ContentSHA256 == b.ContentSHA256 {
		return timelineConflict{}, false
	}
	sameID := a.ID == b.ID
	sameEvent := a.Event != "" && a.Event == b.Event
	explicitIdentity := sameID && (sameEvent || (a.Source == b.Source && a.Evidence != "" && a.Evidence == b.Evidence))
	if a.EventID != "" && b.EventID != "" {
		if a.EventID != b.EventID {
			return timelineConflict{}, false
		}
		explicitIdentity = true
	}
	sameSubject := a.Subject != "" && a.Subject == b.Subject && (a.Event == "" || b.Event == "")
	if !explicitIdentity && !sameID && !sameEvent && !sameSubject {
		return timelineConflict{}, false
	}
	aStart, aEnd, aKnown, aBounded := a.bounds()
	bStart, bEnd, bKnown, bBounded := b.bounds()
	if !aKnown || !bKnown || (!aEnd.Before(bStart) && !bEnd.Before(aStart)) {
		return timelineConflict{}, false
	}
	finding := timelineConflict{Severity: "suspected", Basis: "旧记录 ID、事件描述或主题相同，需核对是否为同一次事件", First: a, Second: b}
	if explicitIdentity {
		finding.Basis = "同一事件标识，至少一条近似日期缺少可比较的范围"
		if aBounded && bBounded {
			finding.Severity = "conflict"
			finding.Basis = "同一事件标识，日期或闭区间互不相容"
		}
	}
	return finding, true
}

// Decisions use the existing revision/content-bound audit authorization.
func timelineRulings(entities []map[string]any) map[string]map[string]any {
	audits := map[string]map[string]any{}
	for _, e := range entities {
		if e["kind"] == "audit" && e["auditType"] == "timeline" && e["result"] == "potential_conflict" {
			id, _ := e["id"].(string)
			audits[id] = e
		}
	}
	rulings := map[string]map[string]any{}
	for _, e := range entities {
		if e["kind"] != "audit-decision" || e["decision"] != "confirmed" {
			continue
		}
		target, _ := e["targetId"].(string)
		audit := audits[target]
		first, a := validSemanticSHA(audit["keepTimelineSha256"])
		second, b := validSemanticSHA(audit["otherTimelineSha256"])
		if !a || !b || first == second {
			continue
		}
		if _, _, err := requireBoundSemanticRecord(e, "时间线审计决定"); err != nil {
			continue
		}
		if err := verifyTargetBinding(e, audit, "targetRevision", "targetContentSha256", "时间线审计"); err != nil {
			continue
		}
		key := timelinePair(first, second)
		if prior, exists := rulings[key]; exists && (prior == nil || prior["keepTimelineSha256"] != first) {
			rulings[key] = nil
			continue
		}
		rulings[key] = audit
	}
	return rulings
}

func timelinePair(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + b
}

func timelineFindings(prior, incoming []timelineRecord, entities []map[string]any) []timelineConflict {
	findings := []timelineConflict{}
	byIdentity := map[string][]timelineRecord{}
	index := func(record timelineRecord) []string {
		keys := []string{"id:" + record.ID}
		for _, pair := range [][2]string{{"eventId:", record.EventID}, {"event:", record.Event}, {"subject:", record.Subject}} {
			if pair[1] != "" {
				keys = append(keys, pair[0]+pair[1])
			}
		}
		return keys
	}
	for _, record := range prior {
		for _, key := range index(record) {
			byIdentity[key] = append(byIdentity[key], record)
		}
	}
	checked := map[string]bool{}
	rulings := timelineRulings(entities)
	for _, record := range incoming {
		for _, key := range index(record) {
			for _, old := range byIdentity[key] {
				pair := timelinePair(old.ContentSHA256, record.ContentSHA256)
				if checked[pair] {
					continue
				}
				checked[pair] = true
				finding, ok := compareTimeline(old, record)
				if !ok {
					continue
				}
				if audit := rulings[pair]; audit != nil {
					finding.AuditID, _ = audit["id"].(string)
					finding.Chosen, _ = audit["keepTimelineSha256"].(string)
				}
				findings = append(findings, finding)
			}
			byIdentity[key] = append(byIdentity[key], record)
		}
	}
	return findings
}

func (w *workspace) checkIncomingTimeline(root string, m manifest, memory map[string]any, card map[string]any) ([]timelineConflict, error) {
	raw, _ := card["timeline"].([]any)
	if len(raw) == 0 {
		return nil, nil
	}
	prior := snapshotTimeline(memory)
	archives, err := w.loadArchiveRecords(root, m)
	if err != nil {
		return nil, err
	}
	for _, items := range archives {
		prior = append(prior, timelineRecords(items, true)...)
	}
	var entities []map[string]any
	if m.EntitiesPath != "" {
		hash, err := fileSHA(root, m.EntitiesPath)
		if err != nil || hash != m.ContentHashes[m.EntitiesPath] {
			return nil, fmt.Errorf("时间线审计所需实体索引哈希不一致：%s", m.EntitiesPath)
		}
		entities, err = currentRecords(root, m.EntitiesPath)
		if err != nil {
			return nil, err
		}
	}
	findings := timelineFindings(prior, timelineRecords(raw, false), entities)
	for _, finding := range findings {
		if (finding.Severity == "conflict" && finding.Chosen == "") ||
			(finding.Chosen != "" && finding.Chosen != finding.Second.ContentSHA256) {
			detail, _ := json.Marshal(finding)
			return findings, fmt.Errorf("时间线写入被拒绝：%s", detail)
		}
	}
	return findings, nil
}
