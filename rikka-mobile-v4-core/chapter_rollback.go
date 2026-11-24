package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

var errHistoricalChapterMissing = errors.New("目标 revision 中不存在该章节")

type chapterRollbackCandidate struct {
	OperationID           string `json:"operationId"`
	ChapterID             string `json:"chapterId"`
	TargetRevision        int    `json:"targetRevision"`
	TargetChapterSHA256   string `json:"targetChapterSha256"`
	ExpectedChapterSHA256 string `json:"expectedChapterSha256"`
	Confirm               bool   `json:"confirm"`
	AuthorDecision        string `json:"authorDecision"`
}

type chapterRollbackAudit struct {
	Format                string `json:"format"`
	OperationID           string `json:"operationId"`
	WorkID                string `json:"workId"`
	ChapterID             string `json:"chapterId"`
	BaseRevision          int    `json:"baseRevision"`
	ExpectedChapterSHA256 string `json:"expectedChapterSha256"`
	TargetRevision        int    `json:"targetRevision"`
	TargetManifest        string `json:"targetManifest"`
	TargetManifestSHA256  string `json:"targetManifestSha256"`
	TargetChapterPath     string `json:"targetChapterPath"`
	TargetChapterSHA256   string `json:"targetChapterSha256"`
	TargetCardPath        string `json:"targetCardPath,omitempty"`
	TargetCardSHA256      string `json:"targetCardSha256,omitempty"`
	AuthorDecision        string `json:"authorDecision"`
	CandidateInputSHA256  string `json:"candidateInputSha256"`
}

type historicalChapter struct {
	Revision       int
	ManifestPath   string
	ManifestSHA256 string
	Entry          entry
	Chapter        chapter
	Card           map[string]any
	CardSHA256     string
}

func rollbackCommitOperationID(requestOperationID string) string {
	return sumBytes([]byte("chapter-rollback:" + requestOperationID))[:32]
}

func readSnapshotFile(backupRoot string, snapshot snapshot, relative string) ([]byte, string, error) {
	if _, err := safePath("/", relative); err != nil {
		return nil, "", errors.New("历史备份包含非法路径")
	}
	hash := snapshot.Files[relative]
	if len(hash) != 64 {
		return nil, "", fmt.Errorf("历史备份缺少文件哈希：%s", relative)
	}
	data, err := readFile(backupRoot, objectPath(hash))
	if err != nil {
		return nil, "", fmt.Errorf("历史备份对象缺失：%s", relative)
	}
	if sumBytes(data) != hash {
		return nil, "", fmt.Errorf("历史备份对象哈希不一致：%s", relative)
	}
	return data, hash, nil
}

func decodeHistoricalChapter(data []byte) (chapter, error) {
	var raw map[string]any
	if err := decodeJSON(data, &raw); err != nil {
		return chapter{}, err
	}
	id, _ := raw["id"].(string)
	title, _ := raw["title"].(string)
	content, ok := raw["content"].(string)
	if !safeID.MatchString(id) || strings.TrimSpace(title) == "" || !ok || strings.TrimSpace(content) == "" {
		return chapter{}, errors.New("历史章节 ID、标题或正文无效")
	}
	status, _ := raw["status"].(string)
	if status == "" {
		status = "final"
	}
	if !validChapterStatus(status) {
		return chapter{}, errors.New("历史章节状态无效")
	}
	volumeID, _ := raw["volumeId"].(string)
	outline, _ := raw["outline"].(string)
	reviewNotes, _ := raw["reviewNotes"].(string)
	return chapter{ID: id, Title: title, Content: content, VolumeID: volumeID,
		Status: status, Outline: outline, ReviewNotes: reviewNotes}, nil
}

// historicalChapterAtRevision reads only from the independent content-addressed
// backup. A live historical manifest is never trusted as the rollback source.
func (w *workspace) historicalChapterAtRevision(id, chapterID string, revision int) (historicalChapter, error) {
	if !safeWorkID.MatchString(id) || !safeID.MatchString(chapterID) || revision < 1 {
		return historicalChapter{}, errors.New("历史章节参数无效")
	}
	backupRoot, snap, err := w.loadSnapshot(id, revision)
	if err != nil {
		return historicalChapter{}, fmt.Errorf("历史 revision 的独立备份不可用：%w", err)
	}
	headData, headHash, err := readSnapshotFile(backupRoot, snap, "HEAD.json")
	if err != nil {
		return historicalChapter{}, err
	}
	if headHash != snap.HeadSHA256 {
		return historicalChapter{}, errors.New("历史备份 HEAD 哈希与快照不一致")
	}
	var historicalHead head
	if err := decodeJSON(headData, &historicalHead); err != nil {
		return historicalChapter{}, err
	}
	if historicalHead.Format != formatName || historicalHead.SchemaVersion != 4 || historicalHead.WorkID != id || historicalHead.Revision != revision ||
		historicalHead.Manifest == "" || len(historicalHead.ManifestSHA256) != 64 {
		return historicalChapter{}, errors.New("历史备份 HEAD 格式、作品或 revision 不一致")
	}
	manifestData, manifestHash, err := readSnapshotFile(backupRoot, snap, historicalHead.Manifest)
	if err != nil {
		return historicalChapter{}, err
	}
	if manifestHash != historicalHead.ManifestSHA256 {
		return historicalChapter{}, errors.New("历史 manifest 哈希与 HEAD 不一致")
	}
	var historicalManifest manifest
	if err := decodeJSON(manifestData, &historicalManifest); err != nil {
		return historicalChapter{}, err
	}
	if historicalManifest.Format != formatName || historicalManifest.SchemaVersion != 4 || historicalManifest.WorkID != id || historicalManifest.Revision != revision {
		return historicalChapter{}, errors.New("历史 manifest 格式、作品或 revision 不一致")
	}
	var found *entry
	for _, descriptor := range historicalManifest.Pages {
		pageData, pageHash, err := readSnapshotFile(backupRoot, snap, descriptor.Path)
		if err != nil {
			return historicalChapter{}, err
		}
		if historicalManifest.ContentHashes[descriptor.Path] != pageHash {
			return historicalChapter{}, errors.New("历史目录页哈希与 manifest 不一致")
		}
		var entries []entry
		if err := decodeJSON(pageData, &entries); err != nil {
			return historicalChapter{}, err
		}
		for index := range entries {
			if entries[index].ID != chapterID {
				continue
			}
			if found != nil {
				return historicalChapter{}, errors.New("历史目录中章节 ID 重复")
			}
			copy := entries[index]
			found = &copy
		}
	}
	if found == nil {
		return historicalChapter{}, errHistoricalChapterMissing
	}
	chapterData, chapterHash, err := readSnapshotFile(backupRoot, snap, found.Path)
	if err != nil {
		return historicalChapter{}, err
	}
	if len(found.SHA256) != 64 || found.SHA256 != chapterHash {
		return historicalChapter{}, errors.New("历史章节 SHA 与目录不一致")
	}
	historicalBody, err := decodeHistoricalChapter(chapterData)
	if err != nil {
		return historicalChapter{}, err
	}
	if historicalBody.ID != chapterID || historicalBody.Title != found.Title {
		return historicalChapter{}, errors.New("历史章节内容与目录引用不一致")
	}
	result := historicalChapter{Revision: revision, ManifestPath: historicalHead.Manifest,
		ManifestSHA256: manifestHash, Entry: *found, Chapter: historicalBody}
	if found.CardPath != "" {
		cardData, cardHash, err := readSnapshotFile(backupRoot, snap, found.CardPath)
		if err != nil {
			return historicalChapter{}, err
		}
		var card map[string]any
		if err := decodeJSON(cardData, &card); err != nil {
			return historicalChapter{}, err
		}
		if err := verifyCard(card, historicalBody); err != nil {
			return historicalChapter{}, fmt.Errorf("历史证据卡无效：%w", err)
		}
		result.Card = card
		result.CardSHA256 = cardHash
	}
	return result, nil
}

func (w *workspace) chapterHistory(id, chapterID string, cursor, limit int) (any, error) {
	if cursor < 0 || limit < 1 || limit > 20 {
		return nil, errors.New("章节历史分页参数无效")
	}
	_, h, _, err := w.load(id)
	if err != nil {
		return nil, err
	}
	changes := []any{}
	lastSHA := ""
	for revision := 1; revision <= h.Revision; revision++ {
		historical, err := w.historicalChapterAtRevision(id, chapterID, revision)
		if errors.Is(err, errHistoricalChapterMissing) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("读取章节历史 revision %d 失败：%w", revision, err)
		}
		if historical.Entry.SHA256 == lastSHA {
			continue
		}
		lastSHA = historical.Entry.SHA256
		changes = append(changes, map[string]any{
			"revision": revision, "chapterId": chapterID, "number": historical.Entry.Number,
			"title": historical.Entry.Title, "status": historical.Chapter.Status,
			"sha256": historical.Entry.SHA256, "cardSha256": historical.CardSHA256,
			"manifestSha256": historical.ManifestSHA256,
		})
	}
	if cursor > len(changes) {
		return nil, errors.New("章节历史游标超出范围")
	}
	end := cursor + limit
	if end > len(changes) {
		end = len(changes)
	}
	return map[string]any{"workId": id, "chapterId": chapterID, "revision": h.Revision,
		"items": changes[cursor:end], "total": len(changes), "cursor": cursor,
		"nextCursor": end, "complete": end == len(changes)}, nil
}

func (w *workspace) rollbackChapterFromFile(id, input string, expected int) (any, error) {
	if !safeWorkID.MatchString(id) || expected < 1 {
		return nil, errors.New("章节回退作品或 revision 参数无效")
	}
	clean, err := cleanAbs(input)
	if err != nil {
		return nil, err
	}
	base := filepath.Join(w.inbox, id)
	if filepath.Dir(clean) != base {
		return nil, errors.New("章节回退候选必须位于当前作品的隔离 inbox")
	}
	data, err := readFile(base, filepath.Base(clean))
	if err != nil {
		return nil, err
	}
	var candidate chapterRollbackCandidate
	if err := decodeJSON(data, &candidate); err != nil {
		return nil, err
	}
	if !safeOpID.MatchString(candidate.OperationID) || !safeID.MatchString(candidate.ChapterID) {
		return nil, errors.New("章节回退 operationId 或 chapterId 无效")
	}
	if !candidate.Confirm || strings.TrimSpace(candidate.AuthorDecision) == "" {
		return nil, errors.New("章节回退需要明确 confirm 和作者决定记录")
	}
	if candidate.TargetRevision < 1 || candidate.TargetRevision >= expected {
		return nil, errors.New("回退目标 revision 必须早于预期当前 revision")
	}
	if len(candidate.TargetChapterSHA256) != 64 || len(candidate.ExpectedChapterSHA256) != 64 ||
		candidate.TargetChapterSHA256 == candidate.ExpectedChapterSHA256 {
		return nil, errors.New("章节回退需要不同且有效的目标/当前 SHA-256")
	}
	root, h, currentManifest, err := w.load(id)
	if err != nil {
		return nil, err
	}
	commitOperationID := rollbackCommitOperationID(candidate.OperationID)
	committedRetry := h.Revision == expected+1 && h.OperationID == commitOperationID
	if h.Revision != expected && !committedRetry {
		return nil, fmt.Errorf("章节回退版本冲突：当前 %d，预期 %d", h.Revision, expected)
	}
	currentAtExpected, err := w.historicalChapterAtRevision(id, candidate.ChapterID, expected)
	if err != nil {
		return nil, fmt.Errorf("预期当前章节的独立备份不可用：%w", err)
	}
	if currentAtExpected.Entry.SHA256 != candidate.ExpectedChapterSHA256 {
		return nil, errors.New("预期当前章节 SHA-256 不一致，拒绝回退")
	}
	target, err := w.historicalChapterAtRevision(id, candidate.ChapterID, candidate.TargetRevision)
	if err != nil {
		return nil, fmt.Errorf("回退目标不可用：%w", err)
	}
	if target.Entry.SHA256 != candidate.TargetChapterSHA256 {
		return nil, errors.New("回退目标章节 SHA-256 不一致")
	}
	if !committedRetry {
		if _, err := w.doctor(id); err != nil {
			return nil, fmt.Errorf("章节回退前完整性检查失败：%w", err)
		}
		entries, err := w.entries(root, currentManifest)
		if err != nil {
			return nil, err
		}
		found := false
		for _, item := range entries {
			if item.ID == candidate.ChapterID {
				found = true
				if item.SHA256 != candidate.ExpectedChapterSHA256 {
					return nil, errors.New("当前章节已经变化，拒绝回退")
				}
			}
		}
		if !found {
			return nil, errors.New("当前作品中不存在待回退章节")
		}
	}
	audit := chapterRollbackAudit{
		Format: "moluo-v4-chapter-rollback", OperationID: candidate.OperationID,
		WorkID: id, ChapterID: candidate.ChapterID, BaseRevision: expected,
		ExpectedChapterSHA256: candidate.ExpectedChapterSHA256,
		TargetRevision:        candidate.TargetRevision, TargetManifest: target.ManifestPath,
		TargetManifestSHA256: target.ManifestSHA256, TargetChapterPath: target.Entry.Path,
		TargetChapterSHA256: target.Entry.SHA256, TargetCardPath: target.Entry.CardPath,
		TargetCardSHA256: target.CardSHA256, AuthorDecision: strings.TrimSpace(candidate.AuthorDecision),
		CandidateInputSHA256: sumBytes(data),
	}
	if err := ensureJSON(root, "rollback-requests/"+candidate.OperationID+".json", audit); err != nil {
		return nil, fmt.Errorf("同一章节回退 operationId 与既有授权请求不一致：%w", err)
	}
	replacement := draft{OperationID: commitOperationID, Mode: "replace", Confirm: true,
		ExpectedChapterSHA256: candidate.ExpectedChapterSHA256, Chapter: target.Chapter,
		Card: target.Card, Rollback: &audit}
	replacementPath := commitOperationID + ".json"
	if filepath.Base(clean) == replacementPath {
		return nil, errors.New("章节回退请求文件名与内部提交 ID 冲突")
	}
	if err := ensureJSON(base, replacementPath, replacement); err != nil {
		return nil, fmt.Errorf("章节回退候选与既有操作不一致：%w", err)
	}
	result, err := w.appendFromFile(id, filepath.Join(base, replacementPath), expected)
	if resultMap, ok := result.(map[string]any); ok {
		resultMap["requestOperationId"] = candidate.OperationID
		resultMap["commitOperationId"] = commitOperationID
		resultMap["rollbackFromRevision"] = expected
		resultMap["rollbackToRevision"] = candidate.TargetRevision
		resultMap["authorDecisionRecorded"] = true
	}
	return result, err
}
