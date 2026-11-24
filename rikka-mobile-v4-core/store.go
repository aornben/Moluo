package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const formatName = "moluo-mobile-v4"
const maxJSONFile = 16 << 20

var safeID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
var safeWorkID = regexp.MustCompile(`^[a-f0-9]{24}$`)
var safeOpID = regexp.MustCompile(`^[a-f0-9]{32}$`)

// atomicRename is replaceable only by same-package fault-injection tests.
// Production always uses the operating system's same-filesystem rename.
var atomicRename = os.Rename

type workspace struct {
	root, works, backups, inbox string
	// validationRoot is only used internally to run the normal load/doctor
	// pipeline against an isolated recovery copy. It is never populated from
	// CLI input and therefore cannot widen the workspace path boundary.
	validationRoot   string
	validationWorkID string
}
type head struct {
	Format         string `json:"format"`
	SchemaVersion  int    `json:"schemaVersion"`
	Revision       int    `json:"revision"`
	Manifest       string `json:"manifest"`
	ManifestSHA256 string `json:"manifestSha256,omitempty"`
	WorkID         string `json:"workId"`
	OperationID    string `json:"operationId,omitempty"`
}
type page struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Path  string `json:"path"`
}
type entry struct {
	ID       string `json:"id"`
	Number   int    `json:"number"`
	Title    string `json:"title"`
	VolumeID string `json:"volumeId,omitempty"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	CardPath string `json:"cardPath,omitempty"`
}
type manifest struct {
	Format              string            `json:"format"`
	SchemaVersion       int               `json:"schemaVersion"`
	Revision            int               `json:"revision"`
	PreviousManifest    *string           `json:"previousManifest"`
	LegacyRevision      int               `json:"legacyRevision"`
	ImportedLegacy      bool              `json:"importedLegacy,omitempty"`
	LegacySavedAt       any               `json:"legacySavedAt"`
	LegacyEnvelopeExtra map[string]any    `json:"legacyEnvelopeExtra"`
	LegacyDataExtra     map[string]any    `json:"legacyDataExtra"`
	Meta                map[string]any    `json:"meta"`
	WorkID              string            `json:"workId"`
	CharactersPath      string            `json:"charactersPath"`
	EntitiesPath        string            `json:"entitiesPath,omitempty"`
	TasksPath           string            `json:"tasksPath"`
	MemoryPath          string            `json:"memoryPath"`
	MemoryRevision      int               `json:"memoryRevision"`
	ChapterCount        int               `json:"chapterCount"`
	Pages               []page            `json:"pages"`
	MemoryArchives      []map[string]any  `json:"memoryArchives"`
	TaskArchives        []map[string]any  `json:"taskArchives"`
	ContentHashes       map[string]string `json:"contentHashes"`
	ChapterStatus       map[string]string `json:"chapterStatus,omitempty"`
	ChapterOutline      map[string]string `json:"chapterOutline,omitempty"`
	ChapterReviewNotes  map[string]string `json:"chapterReviewNotes,omitempty"`
	ChapterOrder        []string          `json:"chapterOrder,omitempty"`
	OperationID         string            `json:"operationId,omitempty"`
	BlueprintPath       string            `json:"blueprintPath,omitempty"`
	BlueprintSHA256     string            `json:"blueprintSha256,omitempty"`
}
type chapter struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Content     string `json:"content"`
	VolumeID    string `json:"volumeId,omitempty"`
	Status      string `json:"status,omitempty"`
	Outline     string `json:"outline,omitempty"`
	ReviewNotes string `json:"reviewNotes,omitempty"`
}

func loadChapter(root, path string) (chapter, error) {
	var raw map[string]any
	if err := loadJSON(root, path, &raw); err != nil {
		return chapter{}, err
	}
	id, _ := raw["id"].(string)
	title, _ := raw["title"].(string)
	content, _ := raw["content"].(string)
	volumeID, _ := raw["volumeId"].(string)
	if !safeID.MatchString(id) || strings.TrimSpace(title) == "" {
		return chapter{}, errors.New("章节 ID 或标题无效")
	}
	status, _ := raw["status"].(string)
	if status == "" {
		status = "final"
	}
	outline, _ := raw["outline"].(string)
	reviewNotes, _ := raw["reviewNotes"].(string)
	return chapter{ID: id, Title: title, Content: content, VolumeID: volumeID, Status: status,
		Outline: outline, ReviewNotes: reviewNotes}, nil
}

func chapterState(m manifest, id string) string {
	if value := m.ChapterStatus[id]; value != "" {
		return value
	}
	return "final"
}

func validChapterStatus(value string) bool {
	return value == "draft" || value == "review" || value == "final"
}

type draft struct {
	OperationID           string                `json:"operationId"`
	PrepareID             string                `json:"prepareId,omitempty"`
	DraftID               string                `json:"draftId,omitempty"`
	ReviewID              string                `json:"reviewId,omitempty"`
	SegmentCount          int                   `json:"segmentCount,omitempty"`
	Mode                  string                `json:"mode,omitempty"`
	ExpectedChapterSHA256 string                `json:"expectedChapterSha256,omitempty"`
	Confirm               bool                  `json:"confirm,omitempty"`
	Chapter               chapter               `json:"chapter"`
	Card                  map[string]any        `json:"card,omitempty"`
	Rollback              *chapterRollbackAudit `json:"rollback,omitempty"`
}
type opRecord struct {
	OperationID           string `json:"operationId"`
	InputSHA256           string `json:"inputSha256"`
	Revision              int    `json:"revision"`
	ChapterID             string `json:"chapterId"`
	RecordID              string `json:"recordId,omitempty"`
	Kind                  string `json:"kind,omitempty"`
	RequestOperationID    string `json:"requestOperationId,omitempty"`
	AuthorDecision        string `json:"authorDecision,omitempty"`
	TargetRevision        int    `json:"targetRevision,omitempty"`
	TargetChapterSHA256   string `json:"targetChapterSha256,omitempty"`
	ProposalID            string `json:"proposalId,omitempty"`
	ApprovalID            string `json:"approvalId,omitempty"`
	ProposalRevision      int    `json:"proposalRevision,omitempty"`
	ApprovalRevision      int    `json:"approvalRevision,omitempty"`
	ProposalContentSHA256 string `json:"proposalContentSha256,omitempty"`
	ApprovalContentSHA256 string `json:"approvalContentSha256,omitempty"`
	StatePayloadSHA256    string `json:"statePayloadSha256,omitempty"`
}

func rejectSymlinks(path string) error {
	path = filepath.Clean(path)
	volume := filepath.VolumeName(path)
	rest := strings.TrimPrefix(path, volume)
	current := volume + string(filepath.Separator)
	for _, part := range strings.Split(rest, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("拒绝符号链接路径：%s", current)
		}
	}
	return nil
}

func openWorkspace(path string) (*workspace, error) {
	root, err := cleanAbs(path)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "linux" && root != "/workspace" {
		return nil, errors.New("手机端只允许当前绑定的 /workspace")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, errors.New("工作区文件根目录不存在")
	}
	return &workspace{root: root, works: filepath.Join(root, "moluo-v4-works"),
		backups: filepath.Join(root, "moluo-v4-backups"), inbox: filepath.Join(root, "moluo-v4-inbox")}, nil
}

func (w *workspace) workRoot(id string) (string, error) {
	if !safeWorkID.MatchString(id) {
		return "", errors.New("作品 ID 无效")
	}
	if w.validationRoot != "" {
		if id != w.validationWorkID {
			return "", errors.New("校验副本作品 ID 不匹配")
		}
		if err := rejectSymlinks(w.validationRoot); err != nil {
			return "", err
		}
		return w.validationRoot, nil
	}
	path := filepath.Join(w.works, id)
	if err := rejectSymlinks(path); err != nil {
		return "", err
	}
	return path, nil
}

func safePath(root, relative string) (string, error) {
	if relative == "" || strings.Contains(relative, "\\") || strings.HasPrefix(relative, "/") || !filepath.IsLocal(relative) {
		return "", errors.New("相对路径无效")
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := rejectSymlinks(path); err != nil {
		return "", err
	}
	return path, nil
}

func readFile(root, relative string) ([]byte, error) {
	path, err := safePath(root, relative)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxJSONFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxJSONFile {
		return nil, errors.New("文件超过 16 MiB 上限")
	}
	return data, nil
}

func decodeJSON(data []byte, out any) error {
	if !utf8.Valid(data) {
		return errors.New("文件不是有效 UTF-8")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("JSON 后存在多余内容")
	}
	return nil
}

func loadJSON(root, relative string, out any) error {
	data, err := readFile(root, relative)
	if err != nil {
		return err
	}
	return decodeJSON(data, out)
}
func jsonData(value any) ([]byte, error) { return json.Marshal(value) }
func sumBytes(data []byte) string        { v := sha256.Sum256(data); return hex.EncodeToString(v[:]) }
func fileSHA(root, relative string) (string, error) {
	data, err := readFile(root, relative)
	if err != nil {
		return "", err
	}
	return sumBytes(data), nil
}

func newFile(root, relative string, data []byte) error {
	path, err := safePath(root, relative)
	if err != nil {
		return err
	}
	if len(data) > maxJSONFile {
		return errors.New("新文件超过 16 MiB 上限")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := rejectSymlinks(path); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	}
	return nil
}

func atomicFile(root, relative string, data []byte) error {
	path, err := safePath(root, relative)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	token := make([]byte, 8)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	temp := path + ".tmp-" + hex.EncodeToString(token)
	if err := newFile(filepath.Dir(temp), filepath.Base(temp), data); err != nil {
		return err
	}
	if err := atomicRename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	if runtime.GOOS != "windows" {
		if dir, err := os.Open(filepath.Dir(path)); err == nil {
			_ = dir.Sync()
			_ = dir.Close()
		}
	}
	return nil
}

func newJSON(root, relative string, value any) error {
	data, err := jsonData(value)
	if err != nil {
		return err
	}
	return newFile(root, relative, data)
}

func ensureJSON(root, relative string, value any) error {
	wanted, err := jsonData(value)
	if err != nil {
		return err
	}
	current, err := readFile(root, relative)
	if err == nil {
		if string(current) != string(wanted) {
			return errors.New("已有恢复记录与当前版本不一致")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return newFile(root, relative, wanted)
}
func atomicJSON(root, relative string, value any) error {
	data, err := jsonData(value)
	if err != nil {
		return err
	}
	return atomicFile(root, relative, data)
}

func randomHex(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func revTag(n int) string { return fmt.Sprintf("r%06d", n) }

func emptyMemory(revision int) map[string]any {
	m := map[string]any{"sourceRevision": revision, "coverage": "complete"}
	for _, k := range []string{"chapterCards", "activeFacts", "knowledge", "openHooks", "openQuestions", "constraints", "timeline", "history", "archives", "summaries"} {
		m[k] = []any{}
	}
	return m
}

func (w *workspace) initWork(title string) (any, error) {
	if len([]rune(title)) > 160 {
		return nil, errors.New("作品名称过长")
	}
	if err := os.MkdirAll(w.works, 0700); err != nil {
		return nil, err
	}
	id, err := randomHex(12)
	if err != nil {
		return nil, err
	}
	root, err := w.workRoot(id)
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(root, 0700); err != nil {
		return nil, err
	}
	meta := map[string]any{"title": title, "author": "", "synopsis": ""}
	if err := newJSON(root, "work.json", map[string]any{"workId": id, "title": title, "schemaVersion": 4, "createdAt": time.Now().UTC().Format(time.RFC3339)}); err != nil {
		return nil, err
	}
	for _, item := range []struct {
		path  string
		value any
	}{{"characters/r000001.json", []any{}}, {"entities/r000001.json", []any{}}, {"tasks/r000001.json", []any{}}, {"memory/r000001.json", emptyMemory(1)}} {
		if err := newJSON(root, item.path, item.value); err != nil {
			return nil, err
		}
	}
	m := manifest{Format: formatName, SchemaVersion: 4, Revision: 1, LegacyRevision: 0, LegacyEnvelopeExtra: map[string]any{}, LegacyDataExtra: map[string]any{}, Meta: meta, WorkID: id, CharactersPath: "characters/r000001.json", EntitiesPath: "entities/r000001.json", TasksPath: "tasks/r000001.json", MemoryPath: "memory/r000001.json", MemoryRevision: 1, Pages: []page{}, MemoryArchives: []map[string]any{}, TaskArchives: []map[string]any{}, ContentHashes: map[string]string{}}
	for _, p := range []string{m.CharactersPath, m.EntitiesPath, m.TasksPath, m.MemoryPath} {
		h, err := fileSHA(root, p)
		if err != nil {
			return nil, err
		}
		m.ContentHashes[p] = h
	}
	if err := newJSON(root, "manifests/r000001.json", m); err != nil {
		return nil, err
	}
	manifestHash, err := fileSHA(root, "manifests/r000001.json")
	if err != nil {
		return nil, err
	}
	h := head{Format: formatName, SchemaVersion: 4, Revision: 1, Manifest: "manifests/r000001.json", ManifestSHA256: manifestHash, WorkID: id}
	if err := newJSON(root, "HEAD.json", h); err != nil {
		return nil, err
	}
	if err := w.backupWork(id, root, h); err != nil {
		return map[string]any{"workId": id, "revision": 1, "backup": "失败", "detail": err.Error()}, savedBackupError{Cause: err}
	}
	return map[string]any{"workId": id, "revision": 1, "backup": "正常"}, nil
}

func (w *workspace) load(id string) (string, head, manifest, error) {
	root, err := w.workRoot(id)
	if err != nil {
		return "", head{}, manifest{}, err
	}
	var h head
	if err := loadJSON(root, "HEAD.json", &h); err != nil {
		return "", h, manifest{}, err
	}
	if h.Format != formatName || h.SchemaVersion != 4 || h.Revision < 1 || h.WorkID != id {
		return "", h, manifest{}, errors.New("HEAD 格式、版本或作品 ID 不匹配")
	}
	if h.ManifestSHA256 != "" {
		actual, err := fileSHA(root, h.Manifest)
		if err != nil || actual != h.ManifestSHA256 {
			return "", h, manifest{}, errors.New("manifest 哈希与 HEAD 不一致；停止写入")
		}
	}
	var m manifest
	if err := loadJSON(root, h.Manifest, &m); err != nil {
		return "", h, m, err
	}
	if m.Format != formatName || m.SchemaVersion != 4 || m.Revision != h.Revision || m.WorkID != id {
		return "", h, m, errors.New("HEAD 与 manifest 不一致")
	}
	return root, h, m, nil
}

func (w *workspace) status(id string) (any, error) {
	root, h, m, err := w.load(id)
	if err != nil {
		return nil, err
	}
	backupOK := w.backupExists(id, h.Revision)
	tasksHash, err := fileSHA(root, m.TasksPath)
	if err != nil || tasksHash != m.ContentHashes[m.TasksPath] {
		return nil, errors.New("待办索引哈希错误，状态不可用")
	}
	var tasks []map[string]any
	if err := loadJSON(root, m.TasksPath, &tasks); err != nil {
		return nil, err
	}
	pending := 0
	for _, task := range tasks {
		if task["status"] != "done" && task["status"] != "cancelled" {
			pending++
		}
	}
	info, err := os.Stat(filepath.Join(root, "HEAD.json"))
	if err != nil {
		return nil, err
	}
	lastBackup := ""
	if backupOK {
		_, snapshot, err := w.loadSnapshot(id, h.Revision)
		if err != nil {
			return nil, err
		}
		lastBackup = snapshot.CreatedAt
	}
	overdueCount, dueSoonCount := 0, 0
	var memory map[string]any
	if err := loadJSON(root, m.MemoryPath, &memory); err != nil {
		return nil, err
	}
	if constraints, err := memoryItems(memory, "constraints"); err == nil {
		for _, raw := range constraints {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if due, ok := deadlineDue(item); ok {
				days := deadlineDays(due)
				if days < 0 {
					overdueCount++
				} else if days <= 7 {
					dueSoonCount++
				}
			}
		}
	}
	return map[string]any{"workId": id, "workspace": w.root, "title": m.Meta["title"],
		"revision": h.Revision, "chapters": m.ChapterCount, "pendingTasks": pending,
		"memoryArchives": len(m.MemoryArchives), "taskArchives": len(m.TaskArchives),
		"overdueDeadlines": overdueCount, "dueSoonDeadlines": dueSoonCount,
		"lastSavedAt": info.ModTime().UTC().Format(time.RFC3339), "lastBackupAt": lastBackup,
		"backupCurrent": backupOK, "manifestSealed": h.ManifestSHA256 != "",
		"skillLoaded": "未检查", "toolPermissions": "未检查", "healthScope": "HEAD/manifest与当前备份；全量证据请运行doctor",
		"version": buildVersion}, nil
}

func (w *workspace) entries(root string, m manifest) ([]entry, error) {
	all := []entry{}
	for _, p := range m.Pages {
		if p.Start != len(all)+1 || p.End < p.Start || p.End-p.Start >= 50 {
			return nil, errors.New("章节分页范围无效")
		}
		var batch []entry
		if err := loadJSON(root, p.Path, &batch); err != nil {
			return nil, err
		}
		if len(batch) != p.End-p.Start+1 {
			return nil, errors.New("章节页数量不符")
		}
		all = append(all, batch...)
	}
	if len(all) != m.ChapterCount {
		return nil, errors.New("章节总数不符")
	}
	return all, nil
}

func (w *workspace) doctor(id string) (any, error) {
	return w.doctorCore(id, true)
}

func (w *workspace) doctorCore(id string, requireBackup bool) (any, error) {
	root, h, m, err := w.load(id)
	if err != nil {
		return nil, err
	}
	return w.doctorSnapshot(root, h, m, requireBackup)
}

func (w *workspace) doctorSnapshot(root string, h head, m manifest, requireBackup bool) (any, error) {
	if err := w.pending(root, h.Revision); err != nil {
		return nil, err
	}
	if h.OperationID != "" {
		var record opRecord
		if err := loadJSON(root, "operations/"+h.OperationID+".json", &record); err != nil || record.Revision != h.Revision {
			return nil, errors.New("当前 HEAD 缺少一致的操作记录；停止后续写入")
		}
	}
	if m.MemoryRevision != h.Revision {
		return nil, errors.New("记忆版本与 HEAD 不一致")
	}
	var memory map[string]any
	if err := loadJSON(root, m.MemoryPath, &memory); err != nil {
		return nil, err
	}
	if intValue(memory["sourceRevision"]) != h.Revision {
		return nil, errors.New("记忆来源版本过期")
	}
	paths := []string{m.CharactersPath, m.TasksPath, m.MemoryPath}
	if m.EntitiesPath != "" {
		paths = append(paths, m.EntitiesPath)
	}
	for _, p := range m.Pages {
		paths = append(paths, p.Path)
	}
	for _, p := range paths {
		actual, err := fileSHA(root, p)
		if err != nil {
			return nil, err
		}
		if m.ContentHashes[p] != actual {
			return nil, fmt.Errorf("内容哈希不一致：%s", p)
		}
	}
	if len(m.ContentHashes) != len(paths) {
		return nil, errors.New("内容哈希清单不完整")
	}
	if m.BlueprintPath != "" {
		actual, err := fileSHA(root, m.BlueprintPath)
		if err != nil {
			return nil, err
		}
		if actual != m.BlueprintSHA256 {
			return nil, errors.New("蓝图文件哈希不一致")
		}
	}
	all, err := w.entries(root, m)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	contents := map[string]string{}
	chapterNumbers := map[string]int{}
	for n, e := range all {
		if !safeID.MatchString(e.ID) || ids[e.ID] || e.Number != n+1 {
			return nil, errors.New("章节 ID 或编号重复/无效")
		}
		ids[e.ID] = true
		chapterNumbers[e.ID] = e.Number
		actual, err := fileSHA(root, e.Path)
		if err != nil {
			return nil, err
		}
		if actual != e.SHA256 {
			return nil, fmt.Errorf("章节哈希不一致：%s", e.ID)
		}
		ch, err := loadChapter(root, e.Path)
		if err != nil {
			return nil, err
		}
		if ch.ID != e.ID || ch.Title != e.Title || ch.VolumeID != e.VolumeID {
			return nil, errors.New("章节目录与正文不一致")
		}
		contents[e.ID] = ch.Content
		if e.CardPath != "" {
			var card map[string]any
			if err := loadJSON(root, e.CardPath, &card); err != nil {
				return nil, err
			}
			if err := verifyCard(card, ch); err != nil {
				return nil, err
			}
		}
	}
	for chapterID, status := range m.ChapterStatus {
		if !ids[chapterID] || !validChapterStatus(status) {
			return nil, errors.New("章节生命周期状态索引损坏")
		}
	}
	for chapterID := range m.ChapterOutline {
		if !ids[chapterID] {
			return nil, errors.New("章节提纲索引损坏")
		}
	}
	for chapterID := range m.ChapterReviewNotes {
		if !ids[chapterID] {
			return nil, errors.New("章节审校记录索引损坏")
		}
	}
	if len(m.ChapterOrder) > 0 {
		if len(m.ChapterOrder) != len(all) {
			return nil, errors.New("章节叙事顺序未覆盖全部章节")
		}
		orderSeen := map[string]bool{}
		for _, chapterID := range m.ChapterOrder {
			if !ids[chapterID] || orderSeen[chapterID] {
				return nil, errors.New("章节叙事顺序包含未知或重复章节")
			}
			orderSeen[chapterID] = true
		}
	}
	characters, err := currentRecords(root, m.CharactersPath)
	if err != nil {
		return nil, err
	}
	characterIDs, err := ensureCharacters(characters)
	if err != nil {
		return nil, err
	}
	for _, char := range characters {
		if err := validateCharacter(char, contents, characterIDs); err != nil {
			return nil, err
		}
	}
	var tasks []map[string]any
	if err := loadJSON(root, m.TasksPath, &tasks); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, task := range tasks {
		tid, _ := task["id"].(string)
		if !safeID.MatchString(tid) || seen[tid] {
			return nil, errors.New("任务 ID 重复/无效")
		}
		seen[tid] = true
		if err := validateTask(task, characterIDs, contents, m.ImportedLegacy); err != nil {
			return nil, err
		}
	}
	warnings := []string{}
	for _, char := range characters {
		charID, _ := char["id"].(string)
		death, deathChapter, found := currentDeathState(char, chapterNumbers)
		if !found || death["status"] != "dead" || deathChapter == 0 {
			continue
		}
		for _, field := range []string{"injuries", "relationships", "cognition", "knowledge", "beliefs"} {
			for _, raw := range charSlice(char, field) {
				entry, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if entry["timeStatus"] == "flashback" {
					continue
				}
				if n := entryChapter(entry, chapterNumbers); n > deathChapter {
					warnings = append(warnings, fmt.Sprintf("人物 %s 已于第 %d 章死亡，但 %s 在第 %d 章仍有记录；如非倒叙请核对", charID, deathChapter, field, n))
					break
				}
			}
		}
	}
	if len(m.TaskArchives) > 0 {
		seenArchivePaths := map[string]bool{}
		for _, descriptor := range m.TaskArchives {
			archive, err := loadTaskArchive(root, descriptor)
			if err != nil {
				return nil, err
			}
			if seenArchivePaths[archive.Path] {
				return nil, errors.New("待办归档路径重复")
			}
			seenArchivePaths[archive.Path] = true
			if err := validateTaskArchiveRange(archive, chapterNumbers); err != nil {
				return nil, err
			}
			for _, task := range archive.Tasks {
				tid, _ := task["id"].(string)
				if !safeID.MatchString(tid) || seen[tid] {
					return nil, errors.New("活动任务与归档任务或多个归档之间存在重复 ID")
				}
				seen[tid] = true
				if err := validateTask(task, characterIDs, contents, m.ImportedLegacy); err != nil {
					return nil, fmt.Errorf("待办归档证据失效：%w", err)
				}
			}
		}
	}
	if err := validateMemory(memory, all, contents, characterIDs); err != nil {
		return nil, err
	}
	if err := validateContinuity(memory, all, contents, characters); err != nil {
		return nil, err
	}
	if m.EntitiesPath != "" {
		entities, err := currentRecords(root, m.EntitiesPath)
		if err != nil {
			return nil, err
		}
		seenEntities := map[string]bool{}
		volumes := map[string]bool{}
		for _, entity := range entities {
			eid, err := idFromRecord(entity)
			if err != nil || seenEntities[eid] {
				return nil, errors.New("世界实体 ID 无效或重复")
			}
			seenEntities[eid] = true
			if entity["kind"] == "volume" {
				volumes[eid] = true
			}
			if err := validateEntity(entity, contents); err != nil {
				return nil, err
			}
		}
		if err := validateEntityReferences(entities, characterIDs); err != nil {
			return nil, err
		}
		for _, e := range all {
			if e.VolumeID != "" && !volumes[e.VolumeID] {
				return nil, errors.New("章节引用缺失卷 ID")
			}
		}
	} else {
		for _, e := range all {
			if e.VolumeID != "" {
				return nil, errors.New("章节有卷 ID 但缺少卷目录")
			}
		}
	}
	summaries, err := memoryItems(memory, "summaries")
	if err != nil {
		return nil, err
	}
	if len(summaries) != len(m.MemoryArchives) {
		return nil, errors.New("历史摘要与归档数量不一致")
	}
	timeline := snapshotTimeline(memory)
	for n, descriptor := range m.MemoryArchives {
		path, _ := descriptor["path"].(string)
		hash, _ := descriptor["sha256"].(string)
		if path == "" || len(hash) != 64 {
			return nil, errors.New("历史归档路径或哈希无效")
		}
		actual, err := fileSHA(root, path)
		if err != nil || actual != hash {
			return nil, fmt.Errorf("历史归档缺失或哈希错误：%s", path)
		}
		var items []any
		if err := loadJSON(root, path, &items); err != nil {
			return nil, err
		}
		if len(items) != intValue(descriptor["count"]) {
			return nil, errors.New("历史归档条目数量不一致")
		}
		if err := validateHistory(items, contents); err != nil {
			return nil, err
		}
		timeline = append(timeline, timelineRecords(items, true)...)
		first, last := intValue(descriptor["fromChapter"]), intValue(descriptor["throughChapter"])
		if first > 0 || last > 0 {
			if first < 1 || last < first {
				return nil, errors.New("历史归档章节范围无效")
			}
			minimum, maximum := 0, 0
			for _, raw := range items {
				record := raw.(map[string]any)
				chapterNumber := chapterNumbers[historySource(record)]
				if chapterNumber < first || chapterNumber > last {
					return nil, errors.New("历史归档章节范围与证据不一致")
				}
				if minimum == 0 || chapterNumber < minimum {
					minimum = chapterNumber
				}
				if chapterNumber > maximum {
					maximum = chapterNumber
				}
			}
			if descriptor["rangeExact"] == true && (minimum != first || maximum != last) {
				return nil, errors.New("历史归档精确范围索引过期")
			}
		}
		summary, ok := summaries[n].(map[string]any)
		if !ok || summary["path"] != path || summary["sha256"] != hash {
			return nil, errors.New("历史摘要定位信息不一致")
		}
	}
	if err := auditCrossEntity(contents, chapterNumbers, characters, tasks, memory); err != nil {
		return nil, err
	}
	var timelineEntities []map[string]any
	if m.EntitiesPath != "" && len(timeline) > 1 {
		timelineEntities, err = currentRecords(root, m.EntitiesPath)
		if err != nil {
			return nil, err
		}
	}
	findings := timelineFindings(nil, timeline, timelineEntities)
	for _, finding := range findings {
		if finding.Chosen == "" {
			warnings = append(warnings, fmt.Sprintf("时间线 %s：%s / %s；%s", finding.Severity, finding.First.ID, finding.Second.ID, finding.Basis))
		}
	}
	if requireBackup && !w.backupExists(h.WorkID, h.Revision) {
		return map[string]any{"revision": h.Revision, "chapters": len(all), "backup": "缺失", "checked": "所有当前章节与哈希", "warnings": warnings, "timelineConflicts": findings}, savedBackupError{Cause: errors.New("当前版本独立备份缺失")}
	}
	return map[string]any{"revision": h.Revision, "chapters": len(all), "tasks": len(tasks), "backup": "正常", "manifestSealed": h.ManifestSHA256 != "", "checked": "所有当前章节与哈希", "warnings": warnings, "timelineConflicts": findings}, nil
}

func intValue(v any) int {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n || n > float64(int(^uint(0)>>1)) || n < -float64(int(^uint(0)>>1))-1 {
			return -1
		}
		return int(n)
	case int:
		return n
	default:
		return -1
	}
}

func (w *workspace) readChapter(id, chapterID string, cursor, limit int) (any, error) {
	if !safeID.MatchString(chapterID) {
		return nil, errors.New("章节 ID 无效")
	}
	if limit < 1 || limit > 8192 || cursor < 0 {
		return nil, errors.New("分页参数无效；每页最多 8192 字节")
	}
	root, _, m, err := w.load(id)
	if err != nil {
		return nil, err
	}
	all, err := w.entries(root, m)
	if err != nil {
		return nil, err
	}
	for _, e := range all {
		if e.ID == chapterID {
			actual, err := fileSHA(root, e.Path)
			if err != nil {
				return nil, err
			}
			if actual != e.SHA256 {
				return nil, errors.New("章节哈希不一致")
			}
			ch, err := loadChapter(root, e.Path)
			if err != nil {
				return nil, err
			}
			body := []byte(ch.Content)
			if cursor > len(body) || !utf8.Valid(body[:cursor]) {
				return nil, errors.New("正文游标无效")
			}
			end := cursor + limit
			if end > len(body) {
				end = len(body)
			}
			for end > cursor && !utf8.Valid(body[cursor:end]) {
				end--
			}
			if end == cursor && cursor < len(body) {
				return nil, errors.New("分页长度不足一个字符")
			}
			outline := ch.Outline
			if value := m.ChapterOutline[e.ID]; value != "" {
				outline = value
			}
			reviewNotes := ch.ReviewNotes
			if value := m.ChapterReviewNotes[e.ID]; value != "" {
				reviewNotes = value
			}
			return map[string]any{"chapterId": e.ID, "title": ch.Title, "status": chapterState(m, e.ID),
				"outline": outline, "reviewNotes": reviewNotes,
				"text": string(body[cursor:end]), "cursor": cursor, "nextCursor": end,
				"totalBytes": len(body), "complete": end == len(body), "chapterSHA256": e.SHA256}, nil
		}
	}
	return nil, errors.New("章节不存在")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
