package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type taskArchiveSnapshot struct {
	Path                 string
	SHA256               string
	Kind                 string
	Count                int
	FromChapter          int
	ThroughChapter       int
	LastCompletedChapter int
	Tasks                []map[string]any
}

func loadTaskArchive(root string, descriptor map[string]any) (taskArchiveSnapshot, error) {
	path, _ := descriptor["path"].(string)
	hash, _ := descriptor["sha256"].(string)
	kind, _ := descriptor["kind"].(string)
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if path == "" || clean != path || !strings.HasPrefix(path, "tasks/archives/") || !strings.HasSuffix(path, ".json") {
		return taskArchiveSnapshot{}, errors.New("待办归档路径无效")
	}
	digest, err := hex.DecodeString(hash)
	if err != nil || len(digest) != 32 || hash != strings.ToLower(hash) {
		return taskArchiveSnapshot{}, errors.New("待办归档哈希无效")
	}
	if kind != "deleted" && kind != "done" {
		return taskArchiveSnapshot{}, errors.New("待办归档类型无效")
	}
	count := intValue(descriptor["count"])
	if count < 1 {
		return taskArchiveSnapshot{}, errors.New("待办归档条目数量无效")
	}
	operationID, _ := descriptor["operationId"].(string)
	if operationID != "" && !safeOpID.MatchString(operationID) {
		return taskArchiveSnapshot{}, errors.New("待办归档操作 ID 无效")
	}
	from := intValue(descriptor["fromChapter"])
	through := intValue(descriptor["throughChapter"])
	last := intValue(descriptor["lastCompletedChapter"])
	if kind == "deleted" {
		if count != 1 {
			return taskArchiveSnapshot{}, errors.New("删除待办归档必须只有一条记录")
		}
		if operationID != "" {
			taskID, _ := descriptor["taskId"].(string)
			decision, _ := descriptor["authorDecision"].(string)
			if !safeID.MatchString(taskID) || strings.TrimSpace(decision) == "" {
				return taskArchiveSnapshot{}, errors.New("删除待办归档缺少任务 ID 或作者决定")
			}
		}
	} else {
		if through < 1 || through > 9999 {
			return taskArchiveSnapshot{}, errors.New("已完成待办归档章节范围无效")
		}
		if from > 0 && from > through {
			return taskArchiveSnapshot{}, errors.New("已完成待办归档起止章节无效")
		}
		if last > 0 && (last < from || last > through) {
			return taskArchiveSnapshot{}, errors.New("已完成待办归档实际末章无效")
		}
		if operationID != "" && (from < 1 || last < from) {
			return taskArchiveSnapshot{}, errors.New("已完成待办归档缺少完整章节范围")
		}
	}
	actual, err := fileSHA(root, path)
	if err != nil || actual != hash {
		return taskArchiveSnapshot{}, fmt.Errorf("待办归档缺失或哈希错误：%s", path)
	}
	var tasks []map[string]any
	if err := loadJSON(root, path, &tasks); err != nil {
		return taskArchiveSnapshot{}, err
	}
	if len(tasks) != count {
		return taskArchiveSnapshot{}, errors.New("待办归档条目数量不一致")
	}
	if kind == "deleted" {
		if taskID, _ := descriptor["taskId"].(string); taskID != "" && tasks[0]["id"] != taskID {
			return taskArchiveSnapshot{}, errors.New("删除待办归档任务 ID 不一致")
		}
	}
	return taskArchiveSnapshot{Path: path, SHA256: hash, Kind: kind, Count: count,
		FromChapter: from, ThroughChapter: through, LastCompletedChapter: last, Tasks: tasks}, nil
}

func validateTaskArchiveRange(archive taskArchiveSnapshot, chapterNumbers map[string]int) error {
	if archive.Kind != "done" {
		return nil
	}
	first, last := 0, 0
	for _, task := range archive.Tasks {
		if task["status"] != "done" {
			return errors.New("已完成待办归档包含未完成任务")
		}
		chapterID, _ := task["completedChapterId"].(string)
		number := chapterNumbers[chapterID]
		if number < 1 || number > archive.ThroughChapter {
			return errors.New("已完成待办归档包含章节范围外任务")
		}
		if first == 0 || number < first {
			first = number
		}
		if number > last {
			last = number
		}
	}
	if archive.FromChapter > 0 && archive.FromChapter != first {
		return errors.New("已完成待办归档起始章节与内容不一致")
	}
	if archive.LastCompletedChapter > 0 && archive.LastCompletedChapter != last {
		return errors.New("已完成待办归档实际末章与内容不一致")
	}
	return nil
}

func (w *workspace) taskValidationContext(root string, m manifest) (map[string]bool, map[string]string, map[string]int, error) {
	characters, err := currentRecords(root, m.CharactersPath)
	if err != nil {
		return nil, nil, nil, err
	}
	characterIDs, err := ensureCharacters(characters)
	if err != nil {
		return nil, nil, nil, err
	}
	entries, err := w.entries(root, m)
	if err != nil {
		return nil, nil, nil, err
	}
	contents := make(map[string]string, len(entries))
	numbers := make(map[string]int, len(entries))
	for _, entry := range entries {
		chapter, err := loadChapter(root, entry.Path)
		if err != nil {
			return nil, nil, nil, err
		}
		contents[entry.ID] = chapter.Content
		numbers[entry.ID] = entry.Number
	}
	return characterIDs, contents, numbers, nil
}

func expectedTaskArchivePath(kind string, revision int, operationID string, through int) string {
	tag := revTag(revision) + "-" + operationID
	if kind == "task-delete" {
		return "tasks/archives/deleted-" + tag + ".json"
	}
	return fmt.Sprintf("tasks/archives/a%06d-%s.json", through, tag)
}

func (w *workspace) verifyCommittedTaskArchive(root string, revision int, m manifest, draft recordDraft) (string, error) {
	recordID := "tasks-archive"
	through := intValue(draft.Record["throughChapter"])
	if draft.Kind == "task-delete" {
		var err error
		recordID, err = idFromRecord(draft.Record)
		if err != nil {
			return "", err
		}
	}
	expectedPath := expectedTaskArchivePath(draft.Kind, revision, draft.OperationID, through)
	var descriptor map[string]any
	for _, candidate := range m.TaskArchives {
		if candidate["path"] == expectedPath {
			if descriptor != nil {
				return "", errors.New("本次待办操作存在重复归档描述")
			}
			descriptor = candidate
		}
	}
	if descriptor == nil {
		return "", errors.New("HEAD 已提交该待办操作，但找不到本次归档描述")
	}
	archive, err := loadTaskArchive(root, descriptor)
	if err != nil {
		return "", err
	}
	operationID, _ := descriptor["operationId"].(string)
	if operationID != "" && operationID != draft.OperationID {
		return "", errors.New("待办归档操作 ID 与提交不一致")
	}
	characterIDs, contents, numbers, err := w.taskValidationContext(root, m)
	if err != nil {
		return "", err
	}
	if err := validateTaskArchiveRange(archive, numbers); err != nil {
		return "", err
	}
	active, err := currentRecords(root, m.TasksPath)
	if err != nil {
		return "", err
	}
	activeIDs := map[string]bool{}
	for _, task := range active {
		id, _ := task["id"].(string)
		activeIDs[id] = true
	}
	matchedDelete := false
	for _, task := range archive.Tasks {
		id, _ := task["id"].(string)
		if activeIDs[id] {
			return "", errors.New("归档任务仍存在于活动任务中")
		}
		if err := validateTask(task, characterIDs, contents, m.ImportedLegacy); err != nil {
			return "", fmt.Errorf("待办归档证据失效：%w", err)
		}
		if draft.Kind == "task-delete" && id == recordID {
			matchedDelete = true
		}
	}
	if draft.Kind == "task-delete" {
		if archive.Kind != "deleted" || !matchedDelete {
			return "", errors.New("删除操作的归档记录与目标任务不一致")
		}
	} else if archive.Kind != "done" || archive.ThroughChapter != through {
		return "", errors.New("批量归档记录与目标章节范围不一致")
	}
	return recordID, nil
}
