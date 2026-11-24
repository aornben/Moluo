package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type pendingTxn struct {
	OperationID    string `json:"operationId"`
	InputSHA256    string `json:"inputSha256"`
	BaseRevision   int    `json:"baseRevision"`
	TargetRevision int    `json:"targetRevision"`
	TargetManifest string `json:"targetManifest"`
}

// transactionFault is a no-op in production and is replaced only by
// same-package tests after the named filesystem checkpoint has really landed.
var transactionFault = func(string) error { return nil }

func transactionCheckpoint(name string) error { return transactionFault(name) }

func (w *workspace) newOperation(id string) (any, error) {
	if _, _, _, err := w.load(id); err != nil {
		return nil, err
	}
	op, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	base := filepath.Join(w.inbox, id)
	if err := rejectSymlinks(base); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		return nil, err
	}
	return map[string]any{"workId": id, "operationId": op, "inputPath": filepath.Join(base, op+".json")}, nil
}

func (w *workspace) stageFile(id, path string) (draft, string, error) {
	if path == "" {
		return draft{}, "", errors.New("写入需要 --input")
	}
	clean, err := cleanAbs(path)
	if err != nil {
		return draft{}, "", err
	}
	base := filepath.Join(w.inbox, id)
	if filepath.Dir(clean) != base {
		return draft{}, "", errors.New("候选文件必须位于当前作品的隔离 inbox")
	}
	data, err := readFile(base, filepath.Base(clean))
	if err != nil {
		return draft{}, "", err
	}
	var d draft
	if err := decodeJSON(data, &d); err != nil {
		return draft{}, "", err
	}
	if !safeOpID.MatchString(d.OperationID) || filepath.Base(clean) != d.OperationID+".json" {
		return draft{}, "", errors.New("操作 ID 与候选文件名不一致")
	}
	return d, sumBytes(data), nil
}

func (w *workspace) lockWork(root string) (func(), error) {
	lock, err := safePath(root, ".writer.lock")
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil, errors.New("作品正被其他写入占用；不得强制覆盖锁")
	}
	if err != nil {
		return nil, err
	}
	_, err = fmt.Fprintf(f, "pid=%d started=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(lock)
		if err != nil {
			return nil, err
		}
		return nil, closeErr
	}
	return func() { _ = os.Remove(lock) }, nil
}

func (w *workspace) pending(root string, current int) error {
	paths, err := filepath.Glob(filepath.Join(root, "transactions", "*.json"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		var tx pendingTxn
		if err := loadJSON(root, filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator))), &tx); err != nil {
			return err
		}
		if tx.TargetRevision > current {
			return fmt.Errorf("存在未完成事务 %s；恢复前禁止新写入", tx.OperationID)
		}
	}
	return nil
}

func verifyCard(card map[string]any, ch chapter) error {
	if card["chapterId"] != ch.ID {
		return errors.New("证据卡章节 ID 不一致")
	}
	for _, field := range []string{"facts", "knowledgeChanges", "hookChanges", "taskEvidence", "openQuestions", "constraints", "timeline"} {
		items, exists := card[field]
		if !exists {
			continue
		}
		array, ok := items.([]any)
		if !ok {
			return fmt.Errorf("证据卡字段无效：%s", field)
		}
		for _, raw := range array {
			item, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("证据卡条目无效：%s", field)
			}
			quote, ok := item["evidence"].(string)
			if !ok || quote == "" || !strings.Contains(ch.Content, quote) {
				return fmt.Errorf("证据卡正文引文未命中：%s", field)
			}
			key := map[string]string{"facts": "sourceChapterId", "knowledgeChanges": "learnedChapterId", "constraints": "sourceChapterId", "timeline": "sourceChapterId"}[field]
			if key != "" && item[key] != ch.ID {
				return fmt.Errorf("证据卡来源章节不一致：%s", field)
			}
			if field == "hookChanges" && item["status"] == "open" && item["openedChapterId"] != ch.ID {
				return errors.New("伏笔开启章节不一致")
			}
		}
	}
	return nil
}

func appendOperationRecord(d draft, inputHash string, revision int) opRecord {
	record := opRecord{OperationID: d.OperationID, InputSHA256: inputHash,
		Revision: revision, ChapterID: d.Chapter.ID}
	if d.Rollback != nil {
		record.RecordID = d.Chapter.ID
		record.Kind = "chapter-rollback"
		record.RequestOperationID = d.Rollback.OperationID
		record.AuthorDecision = d.Rollback.AuthorDecision
		record.TargetRevision = d.Rollback.TargetRevision
		record.TargetChapterSHA256 = d.Rollback.TargetChapterSHA256
	}
	return record
}

// recoverCommittedAppend handles the crash window after HEAD switched but
// before the operation record or backup was persisted. The operation ID on
// HEAD is the durable marker; the chapter bytes are re-read and verified
// before the operation is reported as idempotently committed.
func (w *workspace) recoverCommittedAppend(id, root string, h head, m manifest,
	d draft, inputHash string, expected int) (any, bool, error) {
	if h.OperationID != d.OperationID || h.Revision != expected+1 {
		return nil, false, nil
	}
	all, err := w.entries(root, m)
	if err != nil {
		return nil, true, err
	}
	var matched *entry
	for n := range all {
		if all[n].ID == d.Chapter.ID {
			matched = &all[n]
			break
		}
	}
	if matched == nil {
		return nil, true, errors.New("HEAD 已提交该操作，但章节目录中找不到对应章节")
	}
	ch, err := loadChapter(root, matched.Path)
	if err != nil {
		return nil, true, err
	}
	if ch.Title != d.Chapter.Title || ch.Content != d.Chapter.Content {
		return nil, true, errors.New("HEAD 的操作 ID 与当前候选内容不一致，拒绝伪装幂等")
	}
	opPath := "operations/" + d.OperationID + ".json"
	result := map[string]any{"workId": id, "revision": h.Revision,
		"chapterId": d.Chapter.ID, "idempotent": true}
	if err := newJSON(root, opPath, appendOperationRecord(d, inputHash, h.Revision)); err != nil {
		return result, true, savedBackupError{Cause: errors.New("正文已保存，但操作记录补写失败：" + err.Error())}
	}
	if !w.backupExists(id, h.Revision) {
		if err := w.backupWork(id, root, h); err != nil {
			return result, true, savedBackupError{Cause: errors.New("正文已保存，但独立备份补写失败：" + err.Error())}
		}
	}
	result["backup"] = "正常"
	return result, true, nil
}

func appendCard(memory map[string]any, card map[string]any) error {
	history, ok := memory["history"].([]any)
	if !ok {
		history = []any{}
	}
	for _, spec := range []struct{ source, target string }{{"facts", "activeFacts"}, {"knowledgeChanges", "knowledge"}, {"hookChanges", "openHooks"}, {"openQuestions", "openQuestions"}, {"constraints", "constraints"}, {"timeline", "timeline"}} {
		incoming, ok := card[spec.source].([]any)
		if !ok {
			continue
		}
		current, ok := memory[spec.target].([]any)
		if !ok {
			return fmt.Errorf("记忆字段结构无效：%s", spec.target)
		}
		for _, raw := range incoming {
			item := raw.(map[string]any)
			ident, _ := item["id"].(string)
			if spec.target == "knowledge" {
				ident = fmt.Sprint(item["characterId"], ":", item["factId"])
			}
			if ident == "" {
				return fmt.Errorf("记忆条目缺少 ID：%s", spec.target)
			}
			found := -1
			for i, old := range current {
				prior := old.(map[string]any)
				key, _ := prior["id"].(string)
				if spec.target == "knowledge" {
					key = fmt.Sprint(prior["characterId"], ":", prior["factId"])
				}
				if key == ident {
					found = i
					break
				}
			}
			if spec.target == "openHooks" && item["status"] == "resolved" {
				if item["sourceChapterId"] == nil {
					item["sourceChapterId"] = card["chapterId"]
				}
				if found >= 0 {
					history = append(history, map[string]any{"type": spec.target, "value": current[found]})
					current = append(current[:found], current[found+1:]...)
				}
				history = append(history, map[string]any{"type": spec.target, "value": item})
				continue
			}
			if found >= 0 {
				history = append(history, map[string]any{"type": spec.target, "value": current[found]})
				current[found] = item
			} else {
				current = append(current, item)
			}
		}
		memory[spec.target] = current
	}
	memory["history"] = history
	return nil
}

func previewChapterContinuity(root string, all []entry, existing map[string]any, d draft, replaceIndex int) error {
	encoded, err := jsonData(existing)
	if err != nil {
		return err
	}
	var memory map[string]any
	if err := decodeJSON(encoded, &memory); err != nil {
		return err
	}
	if replaceIndex >= 0 {
		rebuilt := emptyMemory(1)
		for n, e := range all {
			var card map[string]any
			if n == replaceIndex {
				card = d.Card
			} else if e.CardPath != "" {
				if err := loadJSON(root, e.CardPath, &card); err != nil {
					return err
				}
			}
			if card != nil {
				if err := appendCard(rebuilt, card); err != nil {
					return err
				}
			}
		}
		memory = rebuilt
	} else if d.Card != nil {
		if err := appendCard(memory, d.Card); err != nil {
			return err
		}
	}
	preview := append([]entry{}, all...)
	if replaceIndex < 0 {
		preview = append(preview, entry{ID: d.Chapter.ID, Number: len(all) + 1, Title: d.Chapter.Title})
	}
	contents := map[string]string{}
	for _, e := range all {
		ch, err := loadChapter(root, e.Path)
		if err != nil {
			return err
		}
		contents[e.ID] = ch.Content
	}
	contents[d.Chapter.ID] = d.Chapter.Content
	var characters []map[string]any
	rootHead, err := readFile(root, "HEAD.json")
	if err != nil {
		return err
	}
	var h head
	if err := decodeJSON(rootHead, &h); err != nil {
		return err
	}
	var m manifest
	if err := loadJSON(root, h.Manifest, &m); err != nil {
		return err
	}
	if err := loadJSON(root, m.CharactersPath, &characters); err != nil {
		return err
	}
	return validateContinuity(memory, preview, contents, characters)
}

func (w *workspace) appendFromFile(id, input string, expected int) (any, error) {
	root, h, m, err := w.load(id)
	if err != nil {
		return nil, err
	}
	d, inputHash, err := w.stageFile(id, input)
	if err != nil {
		return nil, err
	}
	if d.SegmentCount > 0 {
		if d.Chapter.Content != "" {
			return nil, errors.New("分段章节元数据不得再含完整正文")
		}
		body, digest, err := w.collectSegments(id, d.OperationID, d.SegmentCount)
		if err != nil {
			return nil, err
		}
		d.Chapter.Content = body
		inputHash = sumBytes([]byte(inputHash + ":" + digest))
	}
	opPath := "operations/" + d.OperationID + ".json"
	var previous opRecord
	if err := loadJSON(root, opPath, &previous); err == nil {
		if previous.InputSHA256 != inputHash {
			return nil, errors.New("同一操作 ID 对应不同候选内容")
		}
		if !w.backupExists(id, h.Revision) {
			if err := w.backupWork(id, root, h); err != nil {
				return map[string]any{"workId": id, "revision": previous.Revision,
						"chapterId": previous.ChapterID, "idempotent": true, "backupCurrent": false},
					savedBackupError{Cause: errors.New("已提交操作仍缺独立备份：" + err.Error())}
			}
		}
		return map[string]any{"workId": id, "revision": previous.Revision, "chapterId": previous.ChapterID, "idempotent": true, "backupCurrent": w.backupExists(id, h.Revision)}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if result, recovered, err := w.recoverCommittedAppend(id, root, h, m, d, inputHash, expected); recovered {
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
	if !safeID.MatchString(d.Chapter.ID) || strings.TrimSpace(d.Chapter.Title) == "" || strings.TrimSpace(d.Chapter.Content) == "" {
		return nil, errors.New("章节 ID、标题或正文无效")
	}
	if d.Mode != "" && d.Mode != "append" && d.Mode != "replace" {
		return nil, errors.New("章节操作模式无效")
	}
	if d.Mode == "replace" && (!d.Confirm || len(d.ExpectedChapterSHA256) != 64) {
		return nil, errors.New("替换旧章需要确认和原章 SHA-256")
	}
	if len([]byte(d.Chapter.Content)) > 8<<20 {
		return nil, errors.New("单章正文超过 8 MiB 上限")
	}
	if d.Card != nil {
		if err := verifyCard(d.Card, d.Chapter); err != nil {
			return nil, err
		}
	}
	if d.Chapter.Status == "" {
		d.Chapter.Status = "final"
	}
	if !validChapterStatus(d.Chapter.Status) {
		return nil, errors.New("章节状态必须是 draft、review 或 final")
	}
	unlock, err := w.lockWork(root)
	if err != nil {
		return nil, err
	}
	defer unlock()
	root, current, old, err := w.load(id)
	if err != nil {
		return nil, err
	}
	if current.Revision != expected {
		return nil, errors.New("加锁后版本已变化")
	}
	if _, err := w.doctor(id); err != nil {
		return nil, err
	}
	if d.Chapter.VolumeID != "" {
		if !safeID.MatchString(d.Chapter.VolumeID) || old.EntitiesPath == "" {
			return nil, errors.New("章节卷 ID 无效或卷不存在")
		}
		entities, err := currentRecords(root, old.EntitiesPath)
		if err != nil {
			return nil, err
		}
		found := false
		for _, entity := range entities {
			if entity["id"] == d.Chapter.VolumeID && entity["kind"] == "volume" {
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("章节引用未知卷 ID")
		}
	}
	if err := w.pending(root, current.Revision); err != nil {
		return nil, err
	}
	all, err := w.entries(root, old)
	if err != nil {
		return nil, err
	}
	replaceIndex := -1
	for index, e := range all {
		if e.ID == d.Chapter.ID {
			replaceIndex = index
			break
		}
	}
	if d.Mode == "replace" {
		if replaceIndex < 0 {
			return nil, errors.New("待替换章节不存在")
		}
		if all[replaceIndex].SHA256 != d.ExpectedChapterSHA256 {
			return nil, errors.New("原章 SHA-256 已变化，拒绝覆盖")
		}
		if all[replaceIndex].CardPath != "" && d.Card == nil {
			return nil, errors.New("原章已有证据卡，替换必须提交新证据卡")
		}
		var tasks []map[string]any
		if err := loadJSON(root, old.TasksPath, &tasks); err != nil {
			return nil, err
		}
		var characters []map[string]any
		if err := loadJSON(root, old.CharactersPath, &characters); err != nil {
			return nil, err
		}
		charIDs, err := ensureCharacters(characters)
		if err != nil {
			return nil, err
		}
		contents := map[string]string{}
		chapterNumbers := map[string]int{}
		for _, e := range all {
			ch, err := loadChapter(root, e.Path)
			if err != nil {
				return nil, err
			}
			contents[e.ID] = ch.Content
			chapterNumbers[e.ID] = e.Number
		}
		contents[d.Chapter.ID] = d.Chapter.Content
		for _, descriptor := range old.TaskArchives {
			archive, err := loadTaskArchive(root, descriptor)
			if err != nil {
				return nil, err
			}
			if err := validateTaskArchiveRange(archive, chapterNumbers); err != nil {
				return nil, err
			}
			for _, task := range archive.Tasks {
				if err := validateTask(task, charIDs, contents, old.ImportedLegacy); err != nil {
					return nil, fmt.Errorf("替换后待办归档证据将失效：%w", err)
				}
			}
		}
		for _, task := range tasks {
			if err := validateTask(task, charIDs, contents, old.ImportedLegacy); err != nil {
				return nil, fmt.Errorf("替换后待办证据将失效：%w", err)
			}
		}
	} else if replaceIndex >= 0 {
		return nil, errors.New("章节 ID 重复；替换旧章必须明确 mode=replace")
	}
	var memory map[string]any
	if err := loadJSON(root, old.MemoryPath, &memory); err != nil {
		return nil, err
	}
	if d.Mode == "replace" {
		if err := w.verifyArchiveEvidence(root, old, d.Chapter.ID, d.Chapter.Content); err != nil {
			return nil, fmt.Errorf("替换后归档记忆证据将失效：%w", err)
		}
	}
	if err := previewChapterContinuity(root, all, memory, d, replaceIndex); err != nil {
		return nil, err
	}
	timelineConflicts, err := w.checkIncomingTimeline(root, old, memory, d.Card)
	if err != nil {
		return nil, err
	}
	next := current.Revision + 1
	tag := revTag(next) + "-" + d.OperationID
	chapterPath := "chapters/" + d.Chapter.ID + "-" + tag + ".json"
	pagePath := fmt.Sprintf("pages/p%04d-%s.json", len(old.Pages)+1, tag)
	if replaceIndex >= 0 {
		pagePath = fmt.Sprintf("pages/p%04d-%s.json", replaceIndex/50+1, tag)
	} else if old.ChapterCount%50 != 0 {
		pagePath = fmt.Sprintf("pages/p%04d-%s.json", len(old.Pages), tag)
	}
	memoryPath := "memory/" + tag + ".json"
	manifestPath := "manifests/" + tag + ".json"
	cardPath := ""
	if d.Card != nil {
		cardPath = "memory/cards/" + d.Chapter.ID + "-" + tag + ".json"
	}
	tx := pendingTxn{OperationID: d.OperationID, InputSHA256: inputHash, BaseRevision: current.Revision, TargetRevision: next, TargetManifest: manifestPath}
	if err := newJSON(root, "transactions/"+d.OperationID+".json", tx); err != nil {
		return nil, err
	}
	if err := transactionCheckpoint("append:transaction-created"); err != nil {
		return nil, err
	}
	if err := newJSON(root, chapterPath, d.Chapter); err != nil {
		return nil, err
	}
	if cardPath != "" {
		if err := newJSON(root, cardPath, d.Card); err != nil {
			return nil, err
		}
	}
	if err := transactionCheckpoint("append:data-written"); err != nil {
		return nil, err
	}
	if replaceIndex >= 0 {
		archives, err := memoryItems(memory, "archives")
		if err != nil {
			return nil, err
		}
		summaries, err := memoryItems(memory, "summaries")
		if err != nil {
			return nil, err
		}
		if len(old.MemoryArchives) != len(summaries) {
			return nil, errors.New("记忆归档与摘要数量不一致，拒绝替换")
		}
		oldArchives := archives
		oldSummaries := summaries
		archiveRecords, err := w.loadArchiveRecords(root, old)
		if err != nil {
			return nil, err
		}
		rebuilt := emptyMemory(next)
		if memory["coverage"] == "partial" {
			rebuilt["coverage"] = "partial"
		}
		for index, e := range all {
			path := e.CardPath
			if index == replaceIndex {
				path = cardPath
			}
			if path == "" {
				rebuilt["coverage"] = "partial"
				continue
			}
			var card map[string]any
			if err := loadJSON(root, path, &card); err != nil {
				return nil, err
			}
			if err := appendCard(rebuilt, card); err != nil {
				return nil, err
			}
			cards := rebuilt["chapterCards"].([]any)
			rebuilt["chapterCards"] = append(cards, map[string]any{"chapterId": e.ID, "path": path})
		}
		if err := filterArchivedRecords(rebuilt, archiveRecords); err != nil {
			return nil, err
		}
		rebuilt["archives"] = oldArchives
		rebuilt["summaries"] = oldSummaries
		memory = rebuilt
	} else if cardPath != "" {
		if err := appendCard(memory, d.Card); err != nil {
			return nil, err
		}
		cards := memory["chapterCards"].([]any)
		memory["chapterCards"] = append(cards, map[string]any{"chapterId": d.Chapter.ID, "path": cardPath})
	} else {
		memory["coverage"] = "partial"
	}
	memory["sourceRevision"] = next
	entryNumber := old.ChapterCount + 1
	if replaceIndex >= 0 {
		entryNumber = all[replaceIndex].Number
	}
	if old.ChapterStatus == nil {
		old.ChapterStatus = map[string]string{}
	}
	if old.ChapterOutline == nil {
		old.ChapterOutline = map[string]string{}
	}
	if old.ChapterReviewNotes == nil {
		old.ChapterReviewNotes = map[string]string{}
	}
	old.ChapterStatus[d.Chapter.ID] = d.Chapter.Status
	if d.Chapter.Outline != "" {
		old.ChapterOutline[d.Chapter.ID] = d.Chapter.Outline
	}
	if d.Chapter.ReviewNotes != "" {
		old.ChapterReviewNotes[d.Chapter.ID] = d.Chapter.ReviewNotes
	}
	if old.ChapterOrder == nil {
		for _, item := range all {
			old.ChapterOrder = append(old.ChapterOrder, item.ID)
		}
	}
	if replaceIndex < 0 {
		old.ChapterOrder = append(old.ChapterOrder, d.Chapter.ID)
	}
	newEntry := entry{ID: d.Chapter.ID, Number: entryNumber, Title: d.Chapter.Title, VolumeID: d.Chapter.VolumeID, Path: chapterPath, CardPath: cardPath}
	newEntry.SHA256, err = fileSHA(root, chapterPath)
	if err != nil {
		return nil, err
	}
	var batch []entry
	if replaceIndex >= 0 {
		pageIndex := replaceIndex / 50
		oldPage := old.Pages[pageIndex]
		if err := loadJSON(root, oldPage.Path, &batch); err != nil {
			return nil, err
		}
		batch[replaceIndex%50] = newEntry
		old.Pages[pageIndex].Path = pagePath
	} else if old.ChapterCount%50 != 0 {
		last := old.Pages[len(old.Pages)-1]
		if err := loadJSON(root, last.Path, &batch); err != nil {
			return nil, err
		}
		old.Pages[len(old.Pages)-1] = page{Start: last.Start, End: newEntry.Number, Path: pagePath}
	}
	if replaceIndex < 0 && old.ChapterCount%50 == 0 {
		old.Pages = append(old.Pages, page{Start: newEntry.Number, End: newEntry.Number, Path: pagePath})
	}
	if replaceIndex < 0 {
		batch = append(batch, newEntry)
	}
	if err := newJSON(root, pagePath, batch); err != nil {
		return nil, err
	}
	if replaceIndex < 0 {
		if err := archiveHistory(root, &old, memory, append(all, newEntry), newEntry.Number, tag); err != nil {
			return nil, err
		}
	}
	preflightEntries := append([]entry{}, all...)
	if replaceIndex >= 0 {
		preflightEntries[replaceIndex] = newEntry
	} else {
		preflightEntries = append(preflightEntries, newEntry)
	}
	preflightText := map[string]string{}
	for _, e := range preflightEntries {
		ch, err := loadChapter(root, e.Path)
		if err != nil {
			return nil, err
		}
		preflightText[e.ID] = ch.Content
	}
	var preflightCharacters []map[string]any
	if err := loadJSON(root, old.CharactersPath, &preflightCharacters); err != nil {
		return nil, err
	}
	characterIDs, err := ensureCharacters(preflightCharacters)
	if err != nil {
		return nil, err
	}
	if err := validateMemory(memory, preflightEntries, preflightText, characterIDs); err != nil {
		return nil, err
	}
	if err := validateContinuity(memory, preflightEntries, preflightText, preflightCharacters); err != nil {
		return nil, err
	}
	if err := newJSON(root, memoryPath, memory); err != nil {
		return nil, err
	}
	oldManifestPath := current.Manifest
	old.PreviousManifest = &oldManifestPath
	old.Revision = next
	old.MemoryRevision = next
	old.MemoryPath = memoryPath
	if replaceIndex < 0 {
		old.ChapterCount++
	}
	old.OperationID = d.OperationID
	old.ContentHashes = map[string]string{}
	for _, path := range []string{old.CharactersPath, old.TasksPath, old.MemoryPath} {
		hash, err := fileSHA(root, path)
		if err != nil {
			return nil, err
		}
		old.ContentHashes[path] = hash
	}
	if old.EntitiesPath != "" {
		hash, err := fileSHA(root, old.EntitiesPath)
		if err != nil {
			return nil, err
		}
		old.ContentHashes[old.EntitiesPath] = hash
	}
	for _, p := range old.Pages {
		hash, err := fileSHA(root, p.Path)
		if err != nil {
			return nil, err
		}
		old.ContentHashes[p.Path] = hash
	}
	if err := newJSON(root, manifestPath, old); err != nil {
		return nil, err
	}
	if err := transactionCheckpoint("append:manifest-written"); err != nil {
		return nil, err
	}
	if err := ensureJSON(root, "recovery/head-"+revTag(current.Revision)+".json", current); err != nil {
		return nil, err
	}
	if err := atomicJSON(root, "HEAD.previous.json", current); err != nil {
		return nil, err
	}
	still, err := readFile(root, "HEAD.json")
	if err != nil {
		return nil, err
	}
	var check head
	if err := json.Unmarshal(still, &check); err != nil {
		return nil, err
	}
	if check.Revision != current.Revision || check.Manifest != current.Manifest {
		return nil, errors.New("切换 HEAD 前发生版本冲突")
	}
	manifestHash, err := fileSHA(root, manifestPath)
	if err != nil {
		return nil, err
	}
	newHead := head{Format: formatName, SchemaVersion: 4, Revision: next, Manifest: manifestPath, ManifestSHA256: manifestHash, WorkID: id, OperationID: d.OperationID}
	if err := transactionCheckpoint("append:before-head-switch"); err != nil {
		return nil, err
	}
	if err := atomicJSON(root, "HEAD.json", newHead); err != nil {
		return nil, err
	}
	if err := transactionCheckpoint("append:head-switched"); err != nil {
		return nil, err
	}
	result := map[string]any{"workId": id, "revision": next, "chapterId": d.Chapter.ID, "idempotent": false}
	if len(timelineConflicts) != 0 {
		result["timelineConflicts"] = timelineConflicts
	}
	if err := newJSON(root, opPath, appendOperationRecord(d, inputHash, next)); err != nil {
		return result, savedBackupError{Cause: errors.New("正文已保存，但操作记录写入失败：" + err.Error())}
	}
	if err := w.backupWork(id, root, newHead); err != nil {
		return result, savedBackupError{Cause: errors.New("正文已保存、备份失败：" + err.Error())}
	}
	result["backup"] = "正常"
	return result, nil
}
