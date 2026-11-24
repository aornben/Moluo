package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const recoveryAdoptionFormat = "moluo-v4-recovery-adoption"

// adoptRename is replaceable only by same-package fault-injection tests.
var adoptRename = os.Rename

type recoveryAdoption struct {
	Format             string `json:"format"`
	OperationID        string `json:"operationId"`
	WorkID             string `json:"workId"`
	Revision           int    `json:"revision"`
	RecoveryPath       string `json:"recoveryPath"`
	RecoveryHeadSHA256 string `json:"recoveryHeadSha256"`
	PreviousPath       string `json:"previousPath,omitempty"`
	PreviousHeadSHA256 string `json:"previousHeadSha256,omitempty"`
	StagePath          string `json:"stagePath,omitempty"`
	State              string `json:"state"`
	CreatedAt          string `json:"createdAt"`
	CompletedAt        string `json:"completedAt,omitempty"`
}

func (w *workspace) recoveryRoot() (string, error) {
	root := filepath.Join(w.root, "moluo-v4-recoveries")
	if err := rejectSymlinks(root); err != nil {
		return "", err
	}
	return root, nil
}

func pathDirectlyUnder(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && filepath.Dir(rel) == "." && filepath.IsLocal(rel)
}

// verifyRecoveryContents treats the independent backup snapshot as the trust
// anchor. Every path and byte in the recovery copy must exactly match that
// snapshot, then the ordinary doctor pipeline validates all cross-file links.
func (w *workspace) verifyRecoveryContents(root, id string, revision int) (snapshot, string, error) {
	if err := rejectSymlinks(root); err != nil {
		return snapshot{}, "", err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return snapshot{}, "", errors.New("恢复副本目录不存在或不是目录")
	}
	if err := w.verifyBackup(id, revision, false); err != nil {
		return snapshot{}, "", fmt.Errorf("恢复副本缺少可信独立备份：%w", err)
	}
	_, snap, err := w.loadSnapshot(id, revision)
	if err != nil {
		return snapshot{}, "", err
	}
	seen := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("恢复副本含符号链接：%s", path)
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		expected, ok := snap.Files[rel]
		if !ok {
			return fmt.Errorf("恢复副本含备份清单外文件：%s", rel)
		}
		actual, err := fileSHA(root, rel)
		if err != nil {
			return err
		}
		if actual != expected {
			return fmt.Errorf("恢复副本文件哈希不一致：%s", rel)
		}
		seen[rel] = true
		return nil
	})
	if err != nil {
		return snapshot{}, "", err
	}
	if len(seen) != len(snap.Files) {
		return snapshot{}, "", errors.New("恢复副本文件数量与备份清单不一致")
	}
	for relative := range snap.Files {
		if !seen[relative] {
			return snapshot{}, "", fmt.Errorf("恢复副本缺少文件：%s", relative)
		}
	}
	headSHA, err := fileSHA(root, "HEAD.json")
	if err != nil || headSHA != snap.HeadSHA256 {
		return snapshot{}, "", errors.New("恢复副本 HEAD 哈希与备份不一致")
	}
	validator := *w
	validator.validationRoot = root
	validator.validationWorkID = id
	if _, err := validator.doctorCore(id, false); err != nil {
		return snapshot{}, "", fmt.Errorf("恢复副本跨文件完整性检查失败：%w", err)
	}
	return snap, headSHA, nil
}

func (w *workspace) verifyRecoveryCopy(path, id string, revision int) (string, snapshot, string, error) {
	clean, err := cleanAbs(path)
	if err != nil {
		return "", snapshot{}, "", err
	}
	recoveries, err := w.recoveryRoot()
	if err != nil {
		return "", snapshot{}, "", err
	}
	if !pathDirectlyUnder(recoveries, clean) {
		return "", snapshot{}, "", errors.New("恢复副本必须是当前工作区 moluo-v4-recoveries 的直接子目录")
	}
	wantedPrefix := id + "-" + revTag(revision) + "-"
	if !strings.HasPrefix(filepath.Base(clean), wantedPrefix) {
		return "", snapshot{}, "", errors.New("恢复副本目录名与作品或版本不匹配")
	}
	snap, headSHA, err := w.verifyRecoveryContents(clean, id, revision)
	return clean, snap, headSHA, err
}

func copyRecoveryFiles(source, target string, files map[string]string) error {
	if err := os.Mkdir(target, 0700); err != nil {
		return err
	}
	for _, relative := range sortedKeys(files) {
		data, err := readFile(source, relative)
		if err != nil {
			return err
		}
		if sumBytes(data) != files[relative] {
			return fmt.Errorf("复制期间恢复副本发生变化：%s", relative)
		}
		if err := newFile(target, relative, data); err != nil {
			return err
		}
	}
	return nil
}

func adoptionRecordRoot(w *workspace, id string) (string, error) {
	if !safeWorkID.MatchString(id) {
		return "", errors.New("作品 ID 无效")
	}
	root := filepath.Join(w.root, "moluo-v4-recovery-adoptions", id)
	if err := rejectSymlinks(root); err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	return root, nil
}

func loadAdoptionRecord(root, operationID string) (recoveryAdoption, bool, error) {
	var record recoveryAdoption
	err := loadJSON(root, operationID+".json", &record)
	if errors.Is(err, os.ErrNotExist) {
		return record, false, nil
	}
	if err != nil {
		return record, false, err
	}
	return record, true, nil
}

func validateAdoptionRecord(record recoveryAdoption, operationID, id, recoveryPath string, revision int, headSHA string) error {
	if record.Format != recoveryAdoptionFormat || record.OperationID != operationID || record.WorkID != id ||
		record.Revision != revision || record.RecoveryPath != recoveryPath || record.RecoveryHeadSHA256 != headSHA {
		return errors.New("同一 operationId 已用于不同的恢复激活请求")
	}
	return nil
}

func existingHeadSHA(root string) string {
	hash, err := fileSHA(root, "HEAD.json")
	if err != nil {
		return ""
	}
	return hash
}

// clearInterruptedWriterLock only handles the program's own lock inside a
// journaled recovery stage/activated directory. It never touches the AI
// collaboration lock. On Android/Linux a still-live PID is always refused;
// other platforms retain the existing unlock command's explicit-confirm
// semantics (adopt-recovery itself already requires --confirm).
func clearInterruptedWriterLock(root string) error {
	path, err := safePath(root, ".writer.lock")
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	first := strings.SplitN(string(data), "\n", 2)[0]
	field := strings.SplitN(first, " ", 2)[0]
	if !strings.HasPrefix(field, "pid=") {
		return errors.New("中断恢复锁格式不明，拒绝自动处理")
	}
	pid, err := strconv.Atoi(strings.TrimPrefix(field, "pid="))
	if err != nil || pid < 1 {
		return errors.New("中断恢复锁 PID 无效")
	}
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err == nil {
		return errors.New("恢复激活进程仍在运行，不允许并发接管")
	}
	return os.Remove(path)
}

func (w *workspace) completeAdoption(recordRoot string, record *recoveryAdoption) (any, error) {
	active, err := w.workRoot(record.WorkID)
	if err != nil {
		return nil, err
	}
	validator := *w
	validator.validationRoot = active
	validator.validationWorkID = record.WorkID
	if _, err := validator.doctorCore(record.WorkID, false); err != nil {
		return nil, fmt.Errorf("激活后完整性回读失败：%w", err)
	}
	record.State = "completed"
	record.CompletedAt = time.Now().UTC().Format(time.RFC3339)
	if err := atomicJSON(recordRoot, record.OperationID+".json", record); err != nil {
		return nil, fmt.Errorf("恢复副本已激活，但审计记录写入失败：%w", err)
	}
	return map[string]any{
		"workId": record.WorkID, "revision": record.Revision, "operationId": record.OperationID,
		"activated": true, "idempotent": false, "recoveryPath": record.RecoveryPath,
		"previousPath": record.PreviousPath, "headSHA256": record.RecoveryHeadSHA256,
	}, nil
}

// adoptRecovery activates an independently verified recovery copy. It never
// deletes the source copy or the previous live directory. The two directory
// renames are journaled outside the live work so an interrupted attempt can be
// retried with the same operation ID.
func (w *workspace) adoptRecovery(id, input, operationID string, revision int, confirmed bool) (any, error) {
	if !confirmed {
		return nil, errors.New("激活恢复副本需要用户明确 --confirm")
	}
	if !safeWorkID.MatchString(id) || !safeOpID.MatchString(operationID) {
		return nil, errors.New("作品 ID 或恢复操作 ID 无效")
	}
	if revision < 1 {
		return nil, errors.New("激活恢复副本需要有效 --revision")
	}
	recoveryPath, snap, recoveryHeadSHA, err := w.verifyRecoveryCopy(input, id, revision)
	if err != nil {
		return nil, err
	}
	recordRoot, err := adoptionRecordRoot(w, id)
	if err != nil {
		return nil, err
	}
	record, exists, err := loadAdoptionRecord(recordRoot, operationID)
	if err != nil {
		return nil, err
	}
	if exists {
		if err := validateAdoptionRecord(record, operationID, id, recoveryPath, revision, recoveryHeadSHA); err != nil {
			return nil, err
		}
		active, _ := w.workRoot(id)
		if record.State == "completed" {
			if existingHeadSHA(active) != recoveryHeadSHA {
				return nil, errors.New("恢复激活记录已完成，但当前 HEAD 已变化；拒绝伪装成幂等成功")
			}
			if err := clearInterruptedWriterLock(active); err != nil {
				return nil, err
			}
			if record.PreviousPath != "" {
				if err := clearInterruptedWriterLock(record.PreviousPath); err != nil {
					return nil, err
				}
			}
			validator := *w
			validator.validationRoot, validator.validationWorkID = active, id
			if _, err := validator.doctorCore(id, false); err != nil {
				return nil, fmt.Errorf("已激活作品复核失败：%w", err)
			}
			return map[string]any{"workId": id, "revision": revision, "operationId": operationID,
				"activated": true, "idempotent": true, "recoveryPath": recoveryPath,
				"previousPath": record.PreviousPath, "headSHA256": recoveryHeadSHA}, nil
		}
		// Crash after the stage was renamed but before the journal was finalized.
		if existingHeadSHA(active) == recoveryHeadSHA {
			if err := clearInterruptedWriterLock(active); err != nil {
				return nil, err
			}
			unlock, err := w.lockWork(active)
			if err != nil {
				return nil, err
			}
			defer unlock()
			result, err := w.completeAdoption(recordRoot, &record)
			if err != nil {
				return nil, err
			}
			result.(map[string]any)["idempotent"] = true
			return result, nil
		}
		// An interrupted attempt that left the current directory protected can
		// finish by moving its already verified stage into place.
		if record.State == "prepared" {
			_, activeErr := os.Lstat(active)
			_, previousErr := os.Lstat(record.PreviousPath)
			_, stageErr := os.Lstat(record.StagePath)
			if errors.Is(activeErr, os.ErrNotExist) && previousErr == nil && stageErr == nil {
				record.State = "current-protected"
				if err := atomicJSON(recordRoot, operationID+".json", record); err != nil {
					return nil, err
				}
			}
		}
		if record.State == "current-protected" {
			if _, err := os.Lstat(active); errors.Is(err, os.ErrNotExist) {
				if record.PreviousPath != "" {
					if err := clearInterruptedWriterLock(record.PreviousPath); err != nil {
						return nil, err
					}
				}
				if err := clearInterruptedWriterLock(record.StagePath); err != nil {
					return nil, err
				}
				if _, _, err := w.verifyRecoveryContents(record.StagePath, id, revision); err != nil {
					return nil, fmt.Errorf("中断恢复的暂存副本无效：%w", err)
				}
				if _, err := w.lockWork(record.StagePath); err != nil {
					return nil, err
				}
				if err := adoptRename(record.StagePath, active); err != nil {
					_ = os.Remove(filepath.Join(record.StagePath, ".writer.lock"))
					return nil, fmt.Errorf("继续激活恢复副本失败：%w", err)
				}
				defer os.Remove(filepath.Join(active, ".writer.lock"))
				return w.completeAdoption(recordRoot, &record)
			}
		}
	}

	active, err := w.workRoot(id)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(w.works, 0700); err != nil {
		return nil, err
	}
	token, err := randomHex(8)
	if err != nil {
		return nil, err
	}
	stage := filepath.Join(w.works, ".adopt-"+id+"-"+operationID+"-"+token)
	if err := copyRecoveryFiles(recoveryPath, stage, snap.Files); err != nil {
		_ = os.RemoveAll(stage)
		return nil, err
	}
	if _, _, err := w.verifyRecoveryContents(stage, id, revision); err != nil {
		_ = os.RemoveAll(stage)
		return nil, err
	}

	previousBase := filepath.Join(w.root, "moluo-v4-recovery-previous", id)
	if err := rejectSymlinks(previousBase); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(previousBase, 0700); err != nil {
		return nil, err
	}
	previous := filepath.Join(previousBase, operationID+"-"+token)
	record = recoveryAdoption{Format: recoveryAdoptionFormat, OperationID: operationID, WorkID: id,
		Revision: revision, RecoveryPath: recoveryPath, RecoveryHeadSHA256: recoveryHeadSHA,
		PreviousPath: previous, PreviousHeadSHA256: existingHeadSHA(active), StagePath: stage,
		State: "prepared", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if exists {
		// A prior attempt that rolled back is retried with a fresh stage while
		// retaining the same immutable request identity.
		record.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	var recordErr error
	if exists {
		recordErr = atomicJSON(recordRoot, operationID+".json", record)
	} else {
		recordErr = newJSON(recordRoot, operationID+".json", record)
	}
	if recordErr != nil {
		return nil, recordErr
	}

	currentExists := false
	var unlockCurrent func()
	if info, err := os.Lstat(active); err == nil {
		if !info.IsDir() {
			return nil, errors.New("当前作品路径不是目录")
		}
		currentExists = true
		unlockCurrent, err = w.lockWork(active)
		if err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	currentMoved := false
	stageMoved := false
	defer func() {
		if currentExists {
			if currentMoved {
				_ = os.Remove(filepath.Join(previous, ".writer.lock"))
			} else if unlockCurrent != nil {
				unlockCurrent()
			}
		}
		if stageMoved {
			_ = os.Remove(filepath.Join(active, ".writer.lock"))
		} else {
			_ = os.Remove(filepath.Join(stage, ".writer.lock"))
		}
	}()
	if _, err := w.lockWork(stage); err != nil {
		return nil, err
	}

	if currentExists {
		if err := adoptRename(active, previous); err != nil {
			return nil, fmt.Errorf("保护当前作品失败，未激活恢复副本：%w", err)
		}
		currentMoved = true
		record.State = "current-protected"
		if err := atomicJSON(recordRoot, operationID+".json", record); err != nil {
			if rollbackErr := adoptRename(previous, active); rollbackErr == nil {
				currentMoved = false
			}
			return nil, fmt.Errorf("记录当前作品保护状态失败：%w", err)
		}
	}
	if err := adoptRename(stage, active); err != nil {
		if currentMoved {
			if rollbackErr := adoptRename(previous, active); rollbackErr != nil {
				return nil, fmt.Errorf("激活失败且当前作品自动回滚失败；保留恢复日志等待同一操作重试：激活=%v，回滚=%v", err, rollbackErr)
			}
			currentMoved = false
		}
		record.State = "rolled-back"
		_ = atomicJSON(recordRoot, operationID+".json", record)
		return nil, fmt.Errorf("激活恢复副本失败，当前作品已回滚：%w", err)
	}
	stageMoved = true
	result, err := w.completeAdoption(recordRoot, &record)
	if err != nil {
		return nil, err
	}
	return result, nil
}
