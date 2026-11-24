package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const prepareFormat = "moluo-v4-prepare"
const draftFormat = "moluo-v4-draft"
const reviewFormat = "moluo-v4-review"

type prepareRequirement struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	Text            string `json:"text"`
	Priority        string `json:"priority,omitempty"`
	Hard            bool   `json:"hard"`
	SourceType      string `json:"sourceType"`
	SourceChapterID string `json:"sourceChapterId,omitempty"`
	SourcePath      string `json:"sourcePath,omitempty"`
	SourceSHA256    string `json:"sourceSha256,omitempty"`
	Evidence        string `json:"evidence,omitempty"`
	EvidenceSHA256  string `json:"evidenceSha256,omitempty"`
	MinWords        int    `json:"minWords,omitempty"`
	MaxWords        int    `json:"maxWords,omitempty"`
	TargetWords     int    `json:"targetWords,omitempty"`
	POV             string `json:"pov,omitempty"`
	SemanticReview  bool   `json:"requiresSemanticReview,omitempty"`
}

type prepareContext struct {
	Kind            string `json:"kind"`
	ID              string `json:"id,omitempty"`
	Text            string `json:"text"`
	SourceChapterID string `json:"sourceChapterId,omitempty"`
	SourceSHA256    string `json:"sourceSha256,omitempty"`
}

type prepareCoverage struct {
	FromChapter    int               `json:"fromChapter"`
	ThroughChapter int               `json:"throughChapter"`
	ChaptersRead   []string          `json:"chaptersRead"`
	ArchivesRead   []string          `json:"archivesRead"`
	Files          map[string]string `json:"files"`
	Complete       bool              `json:"complete"`
}

type prepareRecord struct {
	Format         string               `json:"format"`
	PrepareID      string               `json:"prepareId"`
	WorkID         string               `json:"workId"`
	ChapterID      string               `json:"chapterId"`
	ChapterNumber  int                  `json:"chapterNumber"`
	Revision       int                  `json:"revision"`
	ManifestSHA256 string               `json:"manifestSha256"`
	CreatedAt      string               `json:"createdAt"`
	Coverage       prepareCoverage      `json:"coverage"`
	Requirements   []prepareRequirement `json:"requirements"`
	Context        []prepareContext     `json:"context"`
	Unknowns       []string             `json:"unknowns"`
	Conflicts      []string             `json:"conflicts"`
}

type draftCandidate struct {
	OperationID  string         `json:"operationId"`
	PrepareID    string         `json:"prepareId"`
	SegmentCount int            `json:"segmentCount,omitempty"`
	Chapter      chapter        `json:"chapter"`
	Card         map[string]any `json:"card,omitempty"`
}

type workflowDraft struct {
	Format        string         `json:"format"`
	DraftID       string         `json:"draftId"`
	PrepareID     string         `json:"prepareId"`
	PrepareSHA256 string         `json:"prepareSha256"`
	WorkID        string         `json:"workId"`
	ChapterID     string         `json:"chapterId"`
	Revision      int            `json:"revision"`
	Title         string         `json:"title"`
	Content       string         `json:"content"`
	ContentSHA256 string         `json:"contentSha256"`
	Outline       string         `json:"outline,omitempty"`
	ReviewNotes   string         `json:"reviewNotes,omitempty"`
	Card          map[string]any `json:"card,omitempty"`
	CreatedAt     string         `json:"createdAt"`
}

type reviewItem struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	Quote          string `json:"quote,omitempty"`
	Location       string `json:"location,omitempty"`
	Note           string `json:"note,omitempty"`
	DeferToChapter int    `json:"deferToChapter,omitempty"`
}

type reviewCandidate struct {
	OperationID    string       `json:"operationId"`
	PrepareID      string       `json:"prepareId"`
	DraftID        string       `json:"draftId"`
	AuthorDecision string       `json:"authorDecision,omitempty"`
	Requirements   []reviewItem `json:"requirements"`
}

type reviewRecord struct {
	Format             string       `json:"format"`
	ReviewID           string       `json:"reviewId"`
	PrepareID          string       `json:"prepareId"`
	DraftID            string       `json:"draftId"`
	WorkID             string       `json:"workId"`
	ChapterID          string       `json:"chapterId"`
	Revision           int          `json:"revision"`
	PrepareSHA256      string       `json:"prepareSha256"`
	DraftSHA256        string       `json:"draftSha256"`
	DraftContentSHA256 string       `json:"draftContentSha256"`
	AuthorDecision     string       `json:"authorDecision,omitempty"`
	Items              []reviewItem `json:"items"`
	Defects            []string     `json:"defects"`
	Verdict            string       `json:"verdict"`
	CreatedAt          string       `json:"createdAt"`
}

func strValue(value any) string {
	text, _ := value.(string)
	return text
}

type finalCandidate struct {
	OperationID           string         `json:"operationId"`
	PrepareID             string         `json:"prepareId"`
	DraftID               string         `json:"draftId"`
	ReviewID              string         `json:"reviewId"`
	Mode                  string         `json:"mode,omitempty"`
	ExpectedChapterSHA256 string         `json:"expectedChapterSha256,omitempty"`
	Confirm               bool           `json:"confirm,omitempty"`
	Chapter               chapter        `json:"chapter"`
	Card                  map[string]any `json:"card,omitempty"`
}

func truncateUTF8(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(text[:end]) {
		end--
	}
	return text[:end]
}

func clampWords(value int) int {
	if value < 0 {
		return 0
	}
	if value > 1000000 {
		return 1000000
	}
	return value
}

// countHanzi counts Han-script runes; it is the same unit as the million-hanzi
// stress fixture, not UTF-8 bytes or punctuation characters.
func countHanzi(text string) int {
	n := 0
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			n++
		}
	}
	return n
}

func (w *workspace) preparePath(id, prepareID string) string {
	return "prepares/" + prepareID + ".json"
}

func (w *workspace) loadPrepare(id, prepareID string) (string, prepareRecord, string, error) {
	if !safeOpID.MatchString(prepareID) {
		return "", prepareRecord{}, "", errors.New("prepareId 无效")
	}
	root, err := w.workRoot(id)
	if err != nil {
		return "", prepareRecord{}, "", err
	}
	path := w.preparePath(id, prepareID)
	var record prepareRecord
	if err := loadJSON(root, path, &record); err != nil {
		return "", record, "", fmt.Errorf("prepare 记录不存在或损坏：%w", err)
	}
	if record.Format != prepareFormat || record.PrepareID != prepareID || record.WorkID != id {
		return "", record, "", errors.New("prepare 记录格式或作品不一致")
	}
	hash, err := fileSHA(root, path)
	if err != nil {
		return "", record, "", err
	}
	return root, record, hash, nil
}

func (w *workspace) prepareChapter(id, chapterID string, chapterNumber, expected int) (any, error) {
	if !safeWorkID.MatchString(id) || !safeID.MatchString(chapterID) || expected < 1 || chapterNumber < 0 {
		return nil, errors.New("prepare 参数无效")
	}
	root, h, m, err := w.load(id)
	if err != nil {
		return nil, err
	}
	if h.Revision != expected {
		return nil, fmt.Errorf("prepare 版本冲突：当前 %d，预期 %d", h.Revision, expected)
	}
	if !w.backupExists(id, h.Revision) {
		return nil, errors.New("当前版本独立备份缺失；先执行 backup")
	}
	if err := w.pending(root, h.Revision); err != nil {
		return nil, err
	}
	if _, err := w.doctor(id); err != nil {
		return nil, fmt.Errorf("prepare 前完整性检查失败：%w", err)
	}
	all, err := w.entries(root, m)
	if err != nil {
		return nil, err
	}
	targetNumber := chapterNumber
	for _, item := range all {
		if item.ID == chapterID {
			if targetNumber > 0 && targetNumber != item.Number {
				return nil, errors.New("prepare 目标章节号与已有章节不一致")
			}
			targetNumber = item.Number
			break
		}
	}
	if targetNumber == 0 {
		targetNumber = len(all) + 1
	}
	if targetNumber < 1 || targetNumber > len(all)+1 {
		return nil, errors.New("prepare 目标章节号超出可写范围")
	}

	coverage := prepareCoverage{
		FromChapter: 1, ThroughChapter: len(all),
		ChaptersRead: []string{}, ArchivesRead: []string{},
		Files: map[string]string{}, Complete: true,
	}
	requirements := []prepareRequirement{}
	context := []prepareContext{}
	unknowns := []string{}
	conflicts := []string{}
	// The immediately preceding chapter(s) are read in full and hash-checked;
	// only an excerpt is embedded in the task sheet.
	start := targetNumber - 2
	if start < 1 {
		start = 1
	}
	for n := start; n < targetNumber; n++ {
		if n > len(all) {
			break
		}
		item := all[n-1]
		full, err := w.readAllChapter(id, item.ID)
		if err != nil {
			return nil, fmt.Errorf("读取前章 %s 失败：%w", item.ID, err)
		}
		coverage.ChaptersRead = append(coverage.ChaptersRead, item.ID)
		coverage.Files[item.Path] = item.SHA256
		context = append(context, prepareContext{Kind: "previous_chapter", ID: item.ID,
			Text: truncateUTF8(full, 6000), SourceChapterID: item.ID, SourceSHA256: item.SHA256})
	}

	// Tasks and memory are read structurally, not only listed.
	tasks, err := currentRecords(root, m.TasksPath)
	if err != nil {
		return nil, err
	}
	coverage.Files[m.TasksPath] = m.ContentHashes[m.TasksPath]
	for _, task := range tasks {
		status, _ := task["status"].(string)
		if status == "done" || status == "cancelled" {
			continue
		}
		planned := intValue(task["plannedChapter"])
		if planned < 1 || planned > targetNumber {
			continue
		}
		tid, _ := task["id"].(string)
		title, _ := task["title"].(string)
		requirements = append(requirements, prepareRequirement{
			ID: "task:" + tid, Kind: "task", Text: title, Priority: strValue(task["priority"]),
			Hard: true, SourceType: "author", SourcePath: m.TasksPath,
			SourceSHA256: m.ContentHashes[m.TasksPath],
		})
	}

	blueprintHash := ""
	if m.BlueprintPath != "" {
		var blueprint map[string]any
		if err := loadJSON(root, m.BlueprintPath, &blueprint); err != nil {
			return nil, err
		}
		blueprintHash = m.BlueprintSHA256
		coverage.Files[m.BlueprintPath] = blueprintHash
		foundOutline := false
		for _, layerName := range []string{"plan", "canon", "candidate"} {
			layer, ok := blueprint[layerName].(map[string]any)
			if !ok {
				continue
			}
			rawItems, ok := layer["chapterOutline"].([]any)
			if !ok {
				continue
			}
			for _, raw := range rawItems {
				item, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				number := intValue(item["chapterNumber"])
				if number == 0 {
					number = intValue(item["number"])
				}
				outlineID, _ := item["id"].(string)
				if number != targetNumber && outlineID != chapterID {
					continue
				}
				foundOutline = true
				summary := strValue(item["summary"])
				if summary == "" {
					summary = strValue(item["text"])
				}
				if pov, _ := item["pov"].(string); pov != "" {
					summary = fmt.Sprintf("%s（视角：%s）", summary, pov)
				}
				requirements = append(requirements, prepareRequirement{
					ID: "outline:" + outlineID, Kind: "outline", Text: summary,
					Priority: "high", Hard: layerName != "candidate", SourceType: "blueprint:" + layerName,
					SourcePath: m.BlueprintPath, SourceSHA256: blueprintHash,
					POV: strValue(item["pov"]), SemanticReview: strValue(item["pov"]) != "",
				})
			}
		}
		for _, layerName := range []string{"plan", "canon", "candidate"} {
			layer, ok := blueprint[layerName].(map[string]any)
			if !ok {
				continue
			}
			rawLengths, ok := layer["chapterLengths"].([]any)
			if !ok {
				continue
			}
			for _, raw := range rawLengths {
				item, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				number := intValue(item["chapterNumber"])
				if number == 0 {
					number = intValue(item["number"])
				}
				lengthID, _ := item["id"].(string)
				if number != targetNumber && lengthID != chapterID {
					continue
				}
				minWords := clampWords(intValue(item["minWords"]))
				maxWords := clampWords(intValue(item["maxWords"]))
				targetWords := clampWords(intValue(item["targetWords"]))
				text := "本章篇幅约束"
				if targetWords > 0 {
					text = fmt.Sprintf("篇幅目标 %d 字", targetWords)
				}
				parts := []string{}
				if minWords > 0 {
					parts = append(parts, fmt.Sprintf("下限 %d", minWords))
				}
				if maxWords > 0 {
					parts = append(parts, fmt.Sprintf("上限 %d", maxWords))
				}
				if len(parts) > 0 {
					text += "（" + strings.Join(parts, "，") + "）"
				}
				requirements = append(requirements, prepareRequirement{
					ID: "length:" + lengthID, Kind: "length", Text: text,
					Priority: "high", Hard: layerName != "candidate", SourceType: "blueprint:" + layerName,
					SourcePath: m.BlueprintPath, SourceSHA256: blueprintHash,
					MinWords: minWords, MaxWords: maxWords, TargetWords: targetWords,
				})
			}
		}
		if !foundOutline {
			unknowns = append(unknowns, "章纲中没有找到本章提纲；不能把模型猜测当作硬要求")
		}
	} else {
		unknowns = append(unknowns, "作品没有蓝图；本章没有章纲约束")
	}

	var memory map[string]any
	if err := loadJSON(root, m.MemoryPath, &memory); err != nil {
		return nil, err
	}
	coverage.Files[m.MemoryPath] = m.ContentHashes[m.MemoryPath]
	for _, spec := range []struct {
		field, kind string
		hard        bool
	}{
		{"openHooks", "hook", true},
		{"constraints", "constraint", true},
		{"activeFacts", "fact", true},
		{"openQuestions", "question", false},
	} {
		items, err := memoryItems(memory, spec.field)
		if err != nil {
			return nil, err
		}
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			source := strValue(item["sourceChapterId"])
			if source == "" {
				source = strValue(item["openedChapterId"])
			}
			quote := strValue(item["evidence"])
			entrySHA := ""
			for _, e := range all {
				if e.ID == source {
					entrySHA = e.SHA256
					break
				}
			}
			id := strValue(item["id"])
			if id == "" {
				id = fmt.Sprint(spec.field, "-", len(requirements)+1)
			}
			text := strValue(item["text"])
			if text == "" {
				text = strValue(item["subject"])
			}
			if text == "" {
				text = strValue(item["title"])
			}
			if text == "" {
				text = id
			}
			if spec.field == "constraints" {
				if due, ok := deadlineDue(item); ok {
					days := deadlineDays(due)
					switch {
					case days < 0:
						text = fmt.Sprintf("【逾期 %d 天】%s（截止 %s）", -days, text, due.Format("2006-01-02"))
					case days <= 7:
						text = fmt.Sprintf("【剩余 %d 天】%s（截止 %s）", days, text, due.Format("2006-01-02"))
					}
				}
			}
			if source == "" || quote == "" || entrySHA == "" {
				unknowns = append(unknowns, spec.field+" 条目缺少可回查原文证据："+id)
				coverage.Complete = false
				continue
			}
			req := prepareRequirement{
				ID: spec.field + ":" + id, Kind: spec.kind, Text: text,
				Priority: "high", Hard: spec.hard && spec.field != "activeFacts",
				SourceType: "memory", SourceChapterID: source, SourcePath: m.MemoryPath,
				SourceSHA256: m.ContentHashes[m.MemoryPath], Evidence: quote, EvidenceSHA256: entrySHA,
			}
			if spec.field == "activeFacts" {
				context = append(context, prepareContext{Kind: "fact", ID: id, Text: text,
					SourceChapterID: source, SourceSHA256: entrySHA})
			} else {
				requirements = append(requirements, req)
			}
		}
	}

	// All historical archives are read and hash-checked. This is the coverage
	// record used to prevent a false "only active memory was checked" claim.
	for _, descriptor := range m.MemoryArchives {
		path := strValue(descriptor["path"])
		hash := strValue(descriptor["sha256"])
		if path == "" {
			return nil, errors.New("记忆归档描述缺少路径")
		}
		actual, err := fileSHA(root, path)
		if err != nil || actual != hash {
			return nil, fmt.Errorf("记忆归档损坏：%s", path)
		}
		var archived []any
		if err := loadJSON(root, path, &archived); err != nil {
			return nil, err
		}
		coverage.ArchivesRead = append(coverage.ArchivesRead, path)
		coverage.Files[path] = hash
		for _, raw := range archived {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			value, _ := entry["value"].(map[string]any)
			source := strValue(value["sourceChapterId"])
			if source == "" {
				source = strValue(value["learnedChapterId"])
			}
			if source == "" {
				source = strValue(value["openedChapterId"])
			}
			chapterNo := 0
			for _, item := range all {
				if item.ID == source {
					chapterNo = item.Number
					break
				}
			}
			if chapterNo == 0 || chapterNo > targetNumber {
				continue
			}
			text := strValue(value["text"])
			if text == "" {
				text = strValue(value["subject"])
			}
			if text == "" {
				text = strValue(value["title"])
			}
			if text != "" {
				context = append(context, prepareContext{Kind: "archive:" + strValue(entry["type"]),
					ID: strValue(value["id"]), Text: text, SourceChapterID: source,
					SourceSHA256: hash})
			}
		}
	}
	if len(m.MemoryArchives) > 0 {
		context = append(context, prepareContext{Kind: "archive_coverage",
			Text: fmt.Sprintf("已读取并校验 %d 个历史归档；详见 coverage", len(m.MemoryArchives))})
	}

	characters, err := currentRecords(root, m.CharactersPath)
	if err != nil {
		return nil, err
	}
	coverage.Files[m.CharactersPath] = m.ContentHashes[m.CharactersPath]
	for _, char := range characters {
		id, _ := char["id"].(string)
		name, _ := char["name"].(string)
		context = append(context, prepareContext{Kind: "character", ID: id,
			Text: name, SourceSHA256: m.ContentHashes[m.CharactersPath]})
		for _, field := range []string{"cognition", "knowledge", "beliefs"} {
			items, _ := char[field].([]any)
			for _, raw := range items {
				entry, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				text := strValue(entry["belief"])
				if text == "" {
					text = strValue(entry["text"])
				}
				if text == "" {
					text = strValue(entry["factId"])
				}
				if text != "" {
					context = append(context, prepareContext{Kind: "character_knowledge", ID: id,
						Text: text, SourceChapterID: strValue(entry["learnedChapterId"]),
						SourceSHA256: m.ContentHashes[m.CharactersPath]})
				}
			}
		}
	}
	if m.EntitiesPath != "" {
		entities, err := currentRecords(root, m.EntitiesPath)
		if err != nil {
			return nil, err
		}
		coverage.Files[m.EntitiesPath] = m.ContentHashes[m.EntitiesPath]
		for _, entity := range entities {
			id, _ := entity["id"].(string)
			name, _ := entity["name"].(string)
			context = append(context, prepareContext{Kind: "entity", ID: id,
				Text: name, SourceSHA256: m.ContentHashes[m.EntitiesPath]})
		}
	}

	sort.SliceStable(requirements, func(i, j int) bool { return requirements[i].ID < requirements[j].ID })
	prepareID, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	record := prepareRecord{
		Format: prepareFormat, PrepareID: prepareID, WorkID: id, ChapterID: chapterID,
		ChapterNumber: targetNumber, Revision: h.Revision, ManifestSHA256: h.ManifestSHA256,
		CreatedAt: time.Now().UTC().Format(time.RFC3339), Coverage: coverage,
		Requirements: requirements, Context: context, Unknowns: unknowns, Conflicts: conflicts,
	}
	path := w.preparePath(id, prepareID)
	if err := newJSON(root, path, record); err != nil {
		return nil, err
	}
	hash, err := fileSHA(root, path)
	if err != nil {
		return nil, err
	}
	requirementBytes, err := json.Marshal(requirements)
	if err != nil {
		return nil, err
	}
	returnRequirements := requirements
	requirementsComplete := true
	if len(requirementBytes) > 32768 {
		// Never silently truncate a large task sheet; page it explicitly.
		returnRequirements = []prepareRequirement{}
		requirementsComplete = false
	}
	return map[string]any{
		"prepareId": prepareID, "preparePath": path, "prepareSha256": hash,
		"workId": id, "chapterId": chapterID, "chapterNumber": targetNumber,
		"revision": h.Revision, "requirements": returnRequirements,
		"requirementsCount": len(requirements), "requirementsComplete": requirementsComplete,
		"requirementsOutputBytes": len(requirementBytes),
		"contextCount":            len(context), "contextBytes": contextBytes(context),
		"unknowns": unknowns, "conflicts": conflicts, "coverage": coverage,
		"manifestSealed": h.ManifestSHA256 != "",
	}, nil
}

func contextBytes(items []prepareContext) int {
	total := 0
	for _, item := range items {
		total += len(item.Text)
	}
	return total
}

func (w *workspace) readPrepare(id, prepareID string, cursor, limit int) (any, error) {
	return w.readPrepareSection(id, prepareID, "context", cursor, limit)
}

func (w *workspace) readPrepareSection(id, prepareID, section string, cursor, limit int) (any, error) {
	if cursor < 0 || limit < 1 || limit > 20 || (section != "context" && section != "requirements") {
		return nil, errors.New("prepare 分页参数无效")
	}
	_, prepare, _, err := w.loadPrepare(id, prepareID)
	if err != nil {
		return nil, err
	}
	var items []any
	if section == "context" {
		for _, item := range prepare.Context {
			items = append(items, item)
		}
	} else {
		for _, item := range prepare.Requirements {
			items = append(items, item)
		}
	}
	if cursor > len(items) {
		return nil, errors.New("prepare 游标超出范围")
	}
	end := cursor + limit
	if end > len(items) {
		end = len(items)
	}
	page := items[cursor:end]
	data, err := json.Marshal(page)
	if err != nil {
		return nil, err
	}
	for len(data) > 32768 && len(page) > 1 {
		page = page[:len(page)-1]
		end = cursor + len(page)
		data, err = json.Marshal(page)
		if err != nil {
			return nil, err
		}
	}
	if len(data) > 32768 {
		return nil, errors.New("单条 prepare 结果超过 32KiB")
	}
	return map[string]any{"prepareId": prepareID, "section": section, "items": page,
		"cursor": cursor, "nextCursor": end, "complete": end == len(items),
		"outputBytes": len(data)}, nil
}

func (w *workspace) readAllChapter(id, chapterID string) (string, error) {
	cursor := 0
	var out strings.Builder
	for {
		page, err := w.readChapter(id, chapterID, cursor, 8192)
		if err != nil {
			return "", err
		}
		value, ok := page.(map[string]any)
		if !ok {
			return "", errors.New("章节目录返回格式无效")
		}
		text, _ := value["text"].(string)
		out.WriteString(text)
		next := intValue(value["nextCursor"])
		if complete, _ := value["complete"].(bool); complete || next <= cursor {
			break
		}
		cursor = next
		if out.Len() > 8<<20 {
			return "", errors.New("单章正文超过 prepare 读取上限")
		}
	}
	return out.String(), nil
}

func (w *workspace) draftWorkflow(id, input string, expected int) (any, error) {
	root, h, _, err := w.load(id)
	if err != nil {
		return nil, err
	}
	if h.Revision != expected {
		return nil, fmt.Errorf("草稿版本冲突：当前 %d，预期 %d", h.Revision, expected)
	}
	clean, err := cleanAbs(input)
	if err != nil {
		return nil, err
	}
	base := filepath.Join(w.inbox, id)
	if filepath.Dir(clean) != base {
		return nil, errors.New("草稿候选必须位于当前作品隔离 inbox")
	}
	data, err := readFile(base, filepath.Base(clean))
	if err != nil {
		return nil, err
	}
	var candidate draftCandidate
	if err := decodeJSON(data, &candidate); err != nil {
		return nil, err
	}
	if !safeOpID.MatchString(candidate.OperationID) || candidate.PrepareID == "" {
		return nil, errors.New("草稿需要有效 operationId 和 prepareId")
	}
	_, prepare, prepareHash, err := w.loadPrepare(id, candidate.PrepareID)
	if err != nil {
		return nil, err
	}
	if prepare.WorkID != id || prepare.Revision != expected || prepare.ChapterID != candidate.Chapter.ID {
		return nil, errors.New("草稿与 prepare 的作品、章节或版本不一致")
	}
	if strings.TrimSpace(candidate.Chapter.Title) == "" {
		return nil, errors.New("草稿标题为空")
	}
	content := candidate.Chapter.Content
	if candidate.SegmentCount > 0 {
		if content != "" {
			return nil, errors.New("分段草稿不得同时携带全文")
		}
		body, _, err := w.collectSegments(id, candidate.OperationID, candidate.SegmentCount)
		if err != nil {
			return nil, err
		}
		content = body
	}
	if strings.TrimSpace(content) == "" {
		return nil, errors.New("草稿正文为空")
	}
	if len([]byte(content)) > 8<<20 {
		return nil, errors.New("草稿正文超过 8 MiB")
	}
	draft := workflowDraft{
		Format: draftFormat, DraftID: candidate.OperationID, PrepareID: candidate.PrepareID,
		PrepareSHA256: prepareHash, WorkID: id, ChapterID: candidate.Chapter.ID,
		Revision: expected, Title: candidate.Chapter.Title, Content: content,
		ContentSHA256: sumBytes([]byte(content)),
		Outline:       candidate.Chapter.Outline, ReviewNotes: candidate.Chapter.ReviewNotes,
		Card: candidate.Card, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	path := "drafts/" + candidate.OperationID + ".json"
	if existing, err := readFile(root, path); err == nil {
		var old workflowDraft
		if err := decodeJSON(existing, &old); err != nil {
			return nil, err
		}
		if old.ContentSHA256 != draft.ContentSHA256 || old.PrepareID != draft.PrepareID ||
			old.ChapterID != draft.ChapterID {
			return nil, errors.New("同一草稿 ID 对应不同内容")
		}
		return map[string]any{"draftId": old.DraftID, "draftPath": path,
			"contentSha256": old.ContentSHA256, "idempotent": true}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := newJSON(root, path, draft); err != nil {
		return nil, err
	}
	return map[string]any{"draftId": draft.DraftID, "draftPath": path,
		"contentSha256": draft.ContentSHA256, "prepareId": draft.PrepareID, "idempotent": false}, nil
}

func (w *workspace) loadWorkflowDraft(id, draftID string) (workflowDraft, string, error) {
	if !safeOpID.MatchString(draftID) {
		return workflowDraft{}, "", errors.New("draftId 无效")
	}
	root, err := w.workRoot(id)
	if err != nil {
		return workflowDraft{}, "", err
	}
	path := "drafts/" + draftID + ".json"
	var draft workflowDraft
	if err := loadJSON(root, path, &draft); err != nil {
		return draft, "", err
	}
	if draft.Format != draftFormat || draft.DraftID != draftID || draft.WorkID != id {
		return draft, "", errors.New("草稿记录格式或作品不一致")
	}
	hash, err := fileSHA(root, path)
	return draft, hash, err
}

func (w *workspace) reviewWorkflow(id, input string, expected int) (any, error) {
	root, h, m, err := w.load(id)
	if err != nil {
		return nil, err
	}
	if h.Revision != expected {
		return nil, fmt.Errorf("审稿版本冲突：当前 %d，预期 %d", h.Revision, expected)
	}
	clean, err := cleanAbs(input)
	if err != nil {
		return nil, err
	}
	base := filepath.Join(w.inbox, id)
	if filepath.Dir(clean) != base {
		return nil, errors.New("审稿候选必须位于当前作品的隔离 inbox")
	}
	data, err := readFile(base, filepath.Base(clean))
	if err != nil {
		return nil, err
	}
	var candidate reviewCandidate
	if err := decodeJSON(data, &candidate); err != nil {
		return nil, err
	}
	if !safeOpID.MatchString(candidate.OperationID) {
		return nil, errors.New("审稿 operationId 无效")
	}
	_, prepare, prepareHash, err := w.loadPrepare(id, candidate.PrepareID)
	if err != nil {
		return nil, err
	}
	draft, draftHash, err := w.loadWorkflowDraft(id, candidate.DraftID)
	if err != nil {
		return nil, err
	}
	if prepare.Revision != expected || draft.Revision != expected ||
		prepare.ChapterID != draft.ChapterID || prepare.WorkID != id {
		return nil, errors.New("prepare/草稿/HEAD 版本不一致")
	}
	if draft.PrepareID != prepare.PrepareID || draft.PrepareSHA256 != prepareHash {
		return nil, errors.New("草稿没有绑定当前 prepare")
	}
	if draft.ContentSHA256 != sumBytes([]byte(draft.Content)) {
		return nil, errors.New("草稿正文哈希损坏")
	}
	want := map[string]prepareRequirement{}
	for _, req := range prepare.Requirements {
		want[req.ID] = req
	}
	seen := map[string]bool{}
	defects := []string{}
	var timelineConflicts []timelineConflict
	if raw, _ := draft.Card["timeline"].([]any); len(raw) != 0 {
		var memory map[string]any
		if err := loadJSON(root, m.MemoryPath, &memory); err != nil {
			return nil, err
		}
		timelineConflicts, err = w.checkIncomingTimeline(root, m, memory, draft.Card)
		if err != nil {
			defects = append(defects, err.Error())
		}
	}
	items := []reviewItem{}
	for _, item := range candidate.Requirements {
		req, ok := want[item.ID]
		if !ok {
			defects = append(defects, "未知要求 ID："+item.ID)
			continue
		}
		if seen[item.ID] {
			defects = append(defects, "要求重复："+item.ID)
			continue
		}
		seen[item.ID] = true
		switch item.Status {
		case "satisfied":
			if req.Kind == "length" {
				n := countHanzi(draft.Content)
				if req.MinWords > 0 && n < req.MinWords {
					defects = append(defects, fmt.Sprintf("篇幅低于下限：%s（%d < %d 字）", item.ID, n, req.MinWords))
				}
				if req.MaxWords > 0 && n > req.MaxWords {
					defects = append(defects, fmt.Sprintf("篇幅超过上限：%s（%d > %d 字）", item.ID, n, req.MaxWords))
				}
			} else {
				if strings.TrimSpace(item.Quote) == "" || !strings.Contains(draft.Content, item.Quote) {
					defects = append(defects, "要求未在草稿中找到逐字证据："+item.ID)
				}
				// POV is a semantic property. The local program deliberately does not
				// infer it from pronouns; a model/author review explanation is required
				// and persisted so a quote alone can never masquerade as POV validation.
				if req.SemanticReview && strings.TrimSpace(item.Note) == "" {
					defects = append(defects, "POV 需要模型语义审稿或作者核查说明："+item.ID)
				}
			}
		case "waived", "deferred", "not_applicable":
			if strings.TrimSpace(candidate.AuthorDecision) == "" {
				defects = append(defects, "豁免/延期/不适用必须提供作者决定："+item.ID)
			}
			if item.Status == "deferred" && item.DeferToChapter <= prepare.ChapterNumber {
				defects = append(defects, "延期目标章节必须晚于本章："+item.ID)
			}
		default:
			defects = append(defects, "审稿状态无效："+item.ID)
		}
		if req.Hard && item.Status == "not_applicable" {
			defects = append(defects, "硬要求不能无依据标记不适用："+item.ID)
		}
		items = append(items, item)
	}
	for id := range want {
		if !seen[id] {
			defects = append(defects, "缺少要求处理记录："+id)
		}
	}
	verdict := "pass"
	if len(defects) > 0 {
		verdict = "fail"
	}
	reviewID := candidate.OperationID
	path := "reviews/" + reviewID + ".json"
	if existing, err := readFile(root, path); err == nil {
		var old reviewRecord
		if err := decodeJSON(existing, &old); err != nil {
			return nil, err
		}
		if old.PrepareSHA256 != prepareHash || old.DraftSHA256 != draftHash {
			return nil, errors.New("同一审稿 ID 对应不同 prepare/草稿")
		}
		return map[string]any{"reviewId": reviewID, "verdict": old.Verdict,
			"defects": old.Defects, "timelineConflicts": timelineConflicts, "idempotent": true}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	record := reviewRecord{
		Format: reviewFormat, ReviewID: reviewID, PrepareID: prepare.PrepareID,
		DraftID: draft.DraftID, WorkID: id, ChapterID: draft.ChapterID,
		Revision: expected, PrepareSHA256: prepareHash, DraftSHA256: draftHash,
		DraftContentSHA256: draft.ContentSHA256,
		AuthorDecision:     candidate.AuthorDecision, Items: items, Defects: defects,
		Verdict: verdict, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := newJSON(root, path, record); err != nil {
		return nil, err
	}
	return map[string]any{"reviewId": reviewID, "verdict": verdict, "defects": defects,
		"reviewPath": path, "prepareId": prepare.PrepareID, "draftId": draft.DraftID,
		"timelineConflicts": timelineConflicts, "idempotent": false}, nil
}

func (w *workspace) loadReview(id, reviewID string) (reviewRecord, string, error) {
	if !safeOpID.MatchString(reviewID) {
		return reviewRecord{}, "", errors.New("reviewId 无效")
	}
	root, err := w.workRoot(id)
	if err != nil {
		return reviewRecord{}, "", err
	}
	path := "reviews/" + reviewID + ".json"
	var review reviewRecord
	if err := loadJSON(root, path, &review); err != nil {
		return review, "", err
	}
	if review.Format != reviewFormat || review.ReviewID != reviewID || review.WorkID != id {
		return review, "", errors.New("审稿记录格式或作品不一致")
	}
	hash, err := fileSHA(root, path)
	return review, hash, err
}

func (w *workspace) finalizeWorkflow(id, input string, expected int) (any, error) {
	_, h, _, err := w.load(id)
	if err != nil {
		return nil, err
	}
	clean, err := cleanAbs(input)
	if err != nil {
		return nil, err
	}
	base := filepath.Join(w.inbox, id)
	if filepath.Dir(clean) != base {
		return nil, errors.New("定稿候选必须位于当前作品隔离 inbox")
	}
	data, err := readFile(base, filepath.Base(clean))
	if err != nil {
		return nil, err
	}
	var candidate finalCandidate
	if err := decodeJSON(data, &candidate); err != nil {
		return nil, err
	}
	if !safeOpID.MatchString(candidate.OperationID) {
		return nil, errors.New("定稿 operationId 无效")
	}
	_, prepare, prepareHash, err := w.loadPrepare(id, candidate.PrepareID)
	if err != nil {
		return nil, err
	}
	draftRecord, draftHash, err := w.loadWorkflowDraft(id, candidate.DraftID)
	if err != nil {
		return nil, err
	}
	review, _, err := w.loadReview(id, candidate.ReviewID)
	if err != nil {
		return nil, err
	}
	if prepare.Revision != expected || draftRecord.Revision != expected || review.Revision != expected {
		return nil, errors.New("prepare/草稿/审稿/HEAD 版本不一致，不能定稿")
	}
	if !prepare.Coverage.Complete {
		return nil, errors.New("prepare 检索覆盖不完整，禁止安全定稿")
	}
	if draftRecord.PrepareID != prepare.PrepareID || draftRecord.PrepareSHA256 != prepareHash {
		return nil, errors.New("草稿绑定 prepare 不一致")
	}
	if draftRecord.ContentSHA256 != sumBytes([]byte(draftRecord.Content)) {
		return nil, errors.New("草稿正文哈希损坏")
	}
	if review.PrepareID != prepare.PrepareID || review.DraftID != draftRecord.DraftID ||
		review.PrepareSHA256 != prepareHash || review.DraftSHA256 != draftHash ||
		review.DraftContentSHA256 != draftRecord.ContentSHA256 {
		return nil, errors.New("审稿记录与 prepare/草稿不一致")
	}
	if review.Verdict != "pass" || len(review.Defects) > 0 {
		return nil, errors.New("审稿未通过，禁止安全定稿")
	}
	if candidate.Chapter.ID != draftRecord.ChapterID ||
		(candidate.Chapter.Content != "" && candidate.Chapter.Content != draftRecord.Content) ||
		(candidate.Chapter.Title != "" && candidate.Chapter.Title != draftRecord.Title) {
		return nil, errors.New("定稿正文/标题必须与已审草稿完全一致")
	}
	if candidate.Chapter.Status != "" && candidate.Chapter.Status != "final" {
		return nil, errors.New("定稿候选状态必须为 final")
	}
	// Deterministic final operation ID makes a retry after a crash/backup error
	// hit the same operation record instead of creating a second revision.
	finalOp := sumBytes([]byte("finalize:" + candidate.OperationID))[:32]
	if h.Revision != expected {
		if h.Revision != expected+1 || h.OperationID != finalOp {
			return nil, fmt.Errorf("定稿版本冲突：当前 %d，预期 %d", h.Revision, expected)
		}
	}
	finalDraft := draft{OperationID: finalOp, PrepareID: candidate.PrepareID,
		DraftID: candidate.DraftID, ReviewID: candidate.ReviewID,
		Mode: candidate.Mode, ExpectedChapterSHA256: candidate.ExpectedChapterSHA256,
		Confirm: candidate.Confirm,
		Chapter: chapter{ID: draftRecord.ChapterID, Title: draftRecord.Title, Content: draftRecord.Content,
			Status: "final", Outline: draftRecord.Outline, ReviewNotes: draftRecord.ReviewNotes},
		Card: candidate.Card}
	if finalDraft.Card == nil {
		finalDraft.Card = draftRecord.Card
	}
	finalPath := filepath.Join(base, finalOp+".json")
	finalBytes, err := json.Marshal(finalDraft)
	if err != nil {
		return nil, err
	}
	if existing, err := readFile(base, finalOp+".json"); err == nil {
		if sumBytes(existing) != sumBytes(finalBytes) {
			return nil, errors.New("同一最终操作 ID 对应不同定稿内容")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	} else if err := newFile(base, finalOp+".json", finalBytes); err != nil {
		return nil, err
	}
	result, err := w.appendFromFile(id, finalPath, expected)
	if err != nil {
		return result, err
	}
	// Readback the actual committed chapter, not the candidate buffer.
	readback, err := w.readAllChapter(id, candidate.Chapter.ID)
	if err != nil {
		return result, fmt.Errorf("正文已提交但回读失败：%w", err)
	}
	if readback != draftRecord.Content {
		return result, errors.New("正文已提交但回读内容不一致")
	}
	if !w.backupExists(id, intValue(result.(map[string]any)["revision"])) {
		return result, savedBackupError{Cause: errors.New("正文已提交但备份不可验证")}
	}
	if m, ok := result.(map[string]any); ok {
		m["readback"] = "正文一致"
		m["workflow"] = "prepare-draft-review-final"
	}
	return result, nil
}
