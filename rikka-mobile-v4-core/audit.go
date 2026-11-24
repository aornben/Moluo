package main

import (
	"errors"
	"fmt"
	"strings"
)

// auditCrossEntity checks structural references that span modules using data
// the caller already loaded: task characters and completion evidence, and
// knowledge owners. It only reports contradictions the program can decide;
// knowledge fact IDs remain open references because a belief may cite an
// author-created fact that is not registered as a separate active record.
func auditCrossEntity(contents map[string]string, chapterNumbers map[string]int,
	characters, tasks []map[string]any, memory map[string]any) error {
	charIDs := map[string]bool{}
	for _, c := range characters {
		if cid, _ := c["id"].(string); cid != "" {
			charIDs[cid] = true
		}
	}
	for _, task := range tasks {
		tid, _ := task["id"].(string)
		for _, raw := range charSlice(task, "characterIds") {
			linked, _ := raw.(string)
			if linked == "" || !charIDs[linked] {
				return fmt.Errorf("任务 %s 引用缺失人物：%s", tid, linked)
			}
		}
		if task["status"] != "done" || task["legacyUnverified"] == true {
			continue
		}
		chapterID, _ := task["completedChapterId"].(string)
		quote, _ := task["completionEvidence"].(string)
		if chapterID == "" || chapterNumbers[chapterID] == 0 {
			return fmt.Errorf("任务 %s 的完成章节已不存在：%s", tid, chapterID)
		}
		if quote == "" || !strings.Contains(contents[chapterID], quote) {
			return fmt.Errorf("任务 %s 的完成证据已失效：%s", tid, chapterID)
		}
	}
	for _, raw := range charSlice(memory, "knowledge") {
		item, ok := raw.(map[string]any)
		if !ok {
			return errors.New("认知条目格式无效")
		}
		kid, _ := item["id"].(string)
		charID, _ := item["characterId"].(string)
		if !charIDs[charID] {
			return fmt.Errorf("认知 %s 引用缺失人物：%s", kid, charID)
		}
	}
	return nil
}
