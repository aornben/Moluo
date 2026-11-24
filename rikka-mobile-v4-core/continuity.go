package main

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

func parseISODate(value any) (time.Time, error) {
	text, ok := value.(string)
	if !ok || len(text) != 10 {
		return time.Time{}, errors.New("日期必须是 YYYY-MM-DD")
	}
	date, err := time.Parse("2006-01-02", text)
	if err != nil || date.Format("2006-01-02") != text {
		return time.Time{}, errors.New("日期无效")
	}
	return date, nil
}
func integer(value any) (int, bool) {
	n, ok := value.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) || n > 1000000 || n < 0 {
		return 0, false
	}
	return int(n), true
}

// deadlineDue resolves a deadline constraint's effective due date. Relative
// deadlines are computed from their anchor; records without any anchor are
// not deadlines and return false.
func deadlineDue(item map[string]any) (time.Time, bool) {
	if kind, _ := item["kind"].(string); kind != "deadline" {
		return time.Time{}, false
	}
	if item["dueDate"] != nil {
		due, err := parseISODate(item["dueDate"])
		if err != nil {
			return time.Time{}, false
		}
		return due, true
	}
	if item["anchorDate"] == nil {
		return time.Time{}, false
	}
	days, ok := integer(item["relativeDays"])
	if !ok {
		return time.Time{}, false
	}
	anchor, err := parseISODate(item["anchorDate"])
	if err != nil {
		return time.Time{}, false
	}
	return anchor.AddDate(0, 0, days), true
}

// deadlineDays is the whole-day difference between local today and the due
// date; a negative value means the deadline is already overdue.
func deadlineDays(due time.Time) int {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dueDay := time.Date(due.Year(), due.Month(), due.Day(), 0, 0, 0, 0, now.Location())
	return int(dueDay.Sub(today).Hours() / 24)
}

func validateContinuity(memory map[string]any, entries []entry, contents map[string]string, characters []map[string]any) error {
	order := map[string]int{}
	for _, e := range entries {
		order[e.ID] = e.Number
	}
	birth := map[string]time.Time{}
	for _, ch := range characters {
		id, _ := ch["id"].(string)
		if value := ch["birthDate"]; value != nil {
			date, err := parseISODate(value)
			if err != nil {
				return fmt.Errorf("人物生日无效：%s：%w", id, err)
			}
			birth[id] = date
		}
	}
	constraints, err := memoryItems(memory, "constraints")
	if err != nil {
		return err
	}
	type constraint struct {
		id, kind, subject, currency, value, supersedes string
		chapter                                        int
	}
	all := []constraint{}
	known := map[string]bool{}
	for _, raw := range constraints {
		item, ok := raw.(map[string]any)
		if !ok {
			return errors.New("时间金额约束结构无效")
		}
		id, _ := item["id"].(string)
		kind, _ := item["kind"].(string)
		subject, _ := item["subject"].(string)
		source, _ := item["sourceChapterId"].(string)
		if !safeID.MatchString(id) || known[id] || strings.TrimSpace(subject) == "" || order[source] == 0 {
			return errors.New("时间金额约束 ID、主题或来源无效")
		}
		known[id] = true
		quote, _ := item["evidence"].(string)
		if quote == "" || !strings.Contains(contents[source], quote) {
			return errors.New("时间金额约束原文证据不一致")
		}
		c := constraint{id: id, kind: kind, subject: strings.TrimSpace(subject), chapter: order[source]}
		c.supersedes, _ = item["supersedes"].(string)
		switch kind {
		case "deadline":
			var due time.Time
			if item["dueDate"] != nil {
				due, err = parseISODate(item["dueDate"])
				if err != nil {
					return fmt.Errorf("截止日期无效：%w", err)
				}
			}
			if item["relativeDays"] != nil || item["anchorDate"] != nil || item["dueDate"] == nil {
				days, ok := integer(item["relativeDays"])
				if !ok || days < 1 || days > 36500 {
					return errors.New("相对期限天数无效")
				}
				anchor, err := parseISODate(item["anchorDate"])
				if err != nil {
					return fmt.Errorf("相对期限起始日期无效：%w", err)
				}
				computed := anchor.AddDate(0, 0, days)
				if !due.IsZero() && !due.Equal(computed) {
					return errors.New("相对期限与绝对截止日期矛盾")
				}
				due = computed
			}
			c.value = due.Format("2006-01-02")
		case "amount":
			amount, ok := item["value"].(float64)
			if !ok || math.IsNaN(amount) || math.IsInf(amount, 0) || amount <= 0 {
				return errors.New("金额必须是正数")
			}
			currency, _ := item["currency"].(string)
			if strings.TrimSpace(currency) == "" {
				return errors.New("金额缺少币种")
			}
			c.currency = currency
			c.value = fmt.Sprintf("%.6f", amount)
		default:
			return errors.New("约束类型仅支持 deadline 或 amount")
		}
		all = append(all, c)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].chapter < all[j].chapter })
	active := map[string]map[string]string{}
	for _, c := range all {
		key := c.kind + "/" + c.subject + "/" + c.currency
		if active[key] == nil {
			active[key] = map[string]string{}
		}
		if c.supersedes != "" {
			if _, ok := active[key][c.supersedes]; !ok {
				return errors.New("约束 supersedes 未指向同主题的先前记录")
			}
			delete(active[key], c.supersedes)
		}
		for _, prior := range active[key] {
			if prior != c.value {
				return fmt.Errorf("%s 矛盾：%s", c.kind, c.subject)
			}
		}
		active[key][c.id] = c.value
	}
	timeline, err := memoryItems(memory, "timeline")
	if err != nil {
		return err
	}
	for _, raw := range timeline {
		item, ok := raw.(map[string]any)
		if !ok {
			return errors.New("时间线事件结构无效")
		}
		id, _ := item["id"].(string)
		source, _ := item["sourceChapterId"].(string)
		quote, _ := item["evidence"].(string)
		if !safeID.MatchString(id) || order[source] == 0 || quote == "" || !strings.Contains(contents[source], quote) {
			return errors.New("时间线事件缺少有效原文证据")
		}
		timeStatus, _ := item["timeStatus"].(string)
		if timeStatus == "" {
			timeStatus, _ = item["certainty"].(string)
		}
		switch timeStatus {
		case "", "known", "flashback", "approximate":
		case "unknown":
			// Missing anchors must stay explicitly unknown rather than guessed.
			if item["startDate"] != nil || item["date"] != nil || item["endDate"] != nil || item["ageYears"] != nil {
				return errors.New("未知时间事件不得伪造日期或年龄")
			}
			continue
		default:
			return errors.New("时间线时间状态无效")
		}
		startValue := item["startDate"]
		if startValue == nil {
			startValue = item["date"]
		}
		start, err := parseISODate(startValue)
		if err != nil {
			return fmt.Errorf("时间线开始日期无效：%w", err)
		}
		if item["endDate"] != nil {
			end, err := parseISODate(item["endDate"])
			if err != nil || end.Before(start) {
				return errors.New("时间线结束日期早于开始或无效")
			}
		}
		if item["ageYears"] != nil {
			charID, _ := item["characterId"].(string)
			dob, ok := birth[charID]
			if !ok {
				return errors.New("年龄事件缺少人物生日")
			}
			age, valid := integer(item["ageYears"])
			if !valid {
				return errors.New("事件年龄无效")
			}
			expected := start.Year() - dob.Year()
			if start.Month() < dob.Month() || (start.Month() == dob.Month() && start.Day() < dob.Day()) {
				expected--
			}
			if age != expected {
				return errors.New("事件年龄与人物生日、日期矛盾")
			}
		}
	}
	return nil
}
