package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type savedBackupError struct {
	Cause error
	Data  any
}

func (e savedBackupError) Error() string { return e.Cause.Error() }
func (e savedBackupError) Unwrap() error { return e.Cause }

type snapshot struct {
	Format     string            `json:"format"`
	WorkID     string            `json:"workId"`
	Revision   int               `json:"revision"`
	HeadSHA256 string            `json:"headSha256"`
	Files      map[string]string `json:"files"`
	CreatedAt  string            `json:"createdAt"`
}

func (w *workspace) backupRoot(id string) (string, error) {
	if !safeWorkID.MatchString(id) {
		return "", errors.New("作品 ID 无效")
	}
	path := filepath.Join(w.backups, id)
	if err := rejectSymlinks(path); err != nil {
		return "", err
	}
	return path, nil
}

func snapshotPath(revision int) string { return "snapshots/" + revTag(revision) + ".json" }
func objectPath(hash string) string    { return "objects/" + hash[:2] + "/" + hash }

func (w *workspace) loadSnapshot(id string, revision int) (string, snapshot, error) {
	root, err := w.backupRoot(id)
	if err != nil {
		return "", snapshot{}, err
	}
	var s snapshot
	if err := loadJSON(root, snapshotPath(revision), &s); err != nil {
		return "", s, err
	}
	if s.Format != "moluo-v4-snapshot" || s.WorkID != id || s.Revision != revision || len(s.Files) == 0 {
		return "", s, errors.New("独立备份清单格式、作品或版本不符")
	}
	if s.Files["HEAD.json"] != s.HeadSHA256 {
		return "", s, errors.New("备份 HEAD 哈希不一致")
	}
	return root, s, nil
}

func (w *workspace) verifyBackup(id string, revision int, checkCurrent bool) error {
	workRoot := ""
	if checkCurrent {
		var err error
		workRoot, err = w.workRoot(id)
		if err != nil {
			return err
		}
	}
	return w.verifyBackupAgainst(id, revision, workRoot)
}

func (w *workspace) verifyBackupAgainst(id string, revision int, workRoot string) error {
	backupRoot, s, err := w.loadSnapshot(id, revision)
	if err != nil {
		return err
	}
	for relative, hash := range s.Files {
		if len(hash) != 64 {
			return errors.New("备份对象哈希格式无效")
		}
		if _, err := safePath("/", relative); err != nil {
			return errors.New("备份路径非法")
		}
		data, err := readFile(backupRoot, objectPath(hash))
		if err != nil {
			return fmt.Errorf("备份对象缺失：%s", relative)
		}
		if sumBytes(data) != hash {
			return fmt.Errorf("备份对象损坏：%s", relative)
		}
	}
	if workRoot != "" {
		actual, err := fileSHA(workRoot, "HEAD.json")
		if err != nil {
			return err
		}
		if actual != s.HeadSHA256 {
			return errors.New("备份 HEAD 与当前版本不一致")
		}
	}
	return nil
}

func (w *workspace) backupExists(id string, revision int) bool {
	return w.verifyBackup(id, revision, true) == nil
}

func (w *workspace) backupWork(id, workRoot string, h head) error {
	backupRoot, err := w.backupRoot(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(backupRoot, 0700); err != nil {
		return err
	}
	if err := rejectSymlinks(backupRoot); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(backupRoot, filepath.FromSlash(snapshotPath(h.Revision)))); err == nil {
		return w.verifyBackupAgainst(id, h.Revision, workRoot)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	files := map[string]string{}
	err = filepath.WalkDir(workRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("作品内出现符号链接：%s", path)
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(workRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".writer.lock" || strings.Contains(rel, ".tmp-") {
			return nil
		}
		data, err := readFile(workRoot, rel)
		if err != nil {
			return err
		}
		hash := sumBytes(data)
		files[rel] = hash
		object := objectPath(hash)
		if existing, err := readFile(backupRoot, object); err == nil {
			if sumBytes(existing) != hash {
				return fmt.Errorf("已有备份对象损坏：%s", object)
			}
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return newFile(backupRoot, object, data)
	})
	if err != nil {
		return err
	}
	if files["HEAD.json"] == "" {
		return errors.New("作品 HEAD 未进入备份")
	}
	current, err := fileSHA(workRoot, "HEAD.json")
	if err != nil {
		return err
	}
	if current != files["HEAD.json"] {
		return errors.New("备份期间 HEAD 发生变化")
	}
	s := snapshot{Format: "moluo-v4-snapshot", WorkID: id, Revision: h.Revision, HeadSHA256: current, Files: files, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := newJSON(backupRoot, snapshotPath(h.Revision), s); err != nil {
		return err
	}
	return w.verifyBackupAgainst(id, h.Revision, workRoot)
}

func (w *workspace) backup(id string) (any, error) {
	root, h, _, err := w.load(id)
	if err != nil {
		return nil, err
	}
	unlock, err := w.lockWork(root)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if _, err := w.doctorCore(id, false); err != nil {
		return nil, fmt.Errorf("主数据不完整，拒绝以损坏内容补备份：%w", err)
	}
	if err := w.backupWork(id, root, h); err != nil {
		return nil, err
	}
	return map[string]any{"workId": id, "revision": h.Revision, "backup": "正常"}, nil
}

func (w *workspace) restore(id string, revision int) (any, error) {
	if revision < 1 {
		return nil, errors.New("恢复需要 --revision，且必须大于零")
	}
	if err := w.verifyBackup(id, revision, false); err != nil {
		return nil, err
	}
	backupRoot, s, err := w.loadSnapshot(id, revision)
	if err != nil {
		return nil, err
	}
	target, err := w.workRoot(id)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(target); err == nil {
		return nil, errors.New("目标作品目录已存在；恢复只允许写入空位置，不能覆盖")
	}
	if err := os.MkdirAll(w.works, 0700); err != nil {
		return nil, err
	}
	token, err := randomHex(8)
	if err != nil {
		return nil, err
	}
	stage := filepath.Join(w.works, ".restore-"+id+"-"+token)
	if err := os.Mkdir(stage, 0700); err != nil {
		return nil, err
	}
	stageActive := true
	defer func() {
		if stageActive {
			_ = os.RemoveAll(stage)
		}
	}()
	for _, relative := range sortedKeys(s.Files) {
		hash := s.Files[relative]
		data, err := readFile(backupRoot, objectPath(hash))
		if err != nil {
			return nil, err
		}
		if sumBytes(data) != hash {
			return nil, fmt.Errorf("备份对象哈希错误：%s", relative)
		}
		if err := newFile(stage, relative, data); err != nil {
			return nil, err
		}
	}
	restored, err := fileSHA(stage, "HEAD.json")
	if err != nil {
		return nil, err
	}
	if restored != s.HeadSHA256 {
		return nil, errors.New("恢复后 HEAD 哈希错误")
	}
	validator := *w
	validator.validationRoot = stage
	validator.validationWorkID = id
	if _, err := validator.doctorCore(id, false); err != nil {
		return nil, fmt.Errorf("恢复数据完整性检查失败：%w", err)
	}
	if _, err := os.Lstat(target); err == nil {
		return nil, errors.New("恢复期间作品目录被其他进程创建")
	}
	if err := rejectSymlinks(target); err != nil {
		return nil, err
	}
	if err := os.Rename(stage, target); err != nil {
		return nil, err
	}
	stageActive = false
	_, h, _, err := w.load(id)
	if err != nil {
		return nil, err
	}
	if h.Revision != revision {
		return nil, errors.New("恢复后版本不一致")
	}
	return map[string]any{"workId": id, "revision": revision, "restored": "正常", "files": len(s.Files)}, nil
}

// restoreCopy creates a separately named recovery copy even when the live
// work is damaged. It never renames, removes, or overwrites the live work.
func (w *workspace) restoreCopy(id string, revision int) (any, error) {
	if revision < 1 {
		return nil, errors.New("恢复副本需要有效 --revision")
	}
	if err := w.verifyBackup(id, revision, false); err != nil {
		return nil, err
	}
	backupRoot, s, err := w.loadSnapshot(id, revision)
	if err != nil {
		return nil, err
	}
	recoveries := filepath.Join(w.root, "moluo-v4-recoveries")
	if err := rejectSymlinks(recoveries); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(recoveries, 0700); err != nil {
		return nil, err
	}
	token, err := randomHex(8)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(recoveries, id+"-"+revTag(revision)+"-"+token)
	if err := os.Mkdir(target, 0700); err != nil {
		return nil, err
	}
	for _, relative := range sortedKeys(s.Files) {
		hash := s.Files[relative]
		data, err := readFile(backupRoot, objectPath(hash))
		if err != nil {
			return nil, err
		}
		if sumBytes(data) != hash {
			return nil, errors.New("恢复副本对象哈希错误")
		}
		if err := newFile(target, relative, data); err != nil {
			return nil, err
		}
	}
	actual, err := fileSHA(target, "HEAD.json")
	if err != nil || actual != s.HeadSHA256 {
		return nil, errors.New("恢复副本 HEAD 哈希错误")
	}
	var h head
	if err := loadJSON(target, "HEAD.json", &h); err != nil {
		return nil, err
	}
	if h.WorkID != id || h.Revision != revision {
		return nil, errors.New("恢复副本作品 ID 或版本错误")
	}
	return map[string]any{"workId": id, "revision": revision, "recoveryPath": target, "files": len(s.Files), "headSHA256": actual, "liveWorkUntouched": true}, nil
}
