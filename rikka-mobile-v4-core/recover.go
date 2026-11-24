package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (w *workspace) recoverPending(id, operationID string, confirmed bool) (any, error) {
	if !confirmed {
		return nil, errors.New("放弃未提交事务需要用户明确确认；先运行 doctor")
	}
	if !safeOpID.MatchString(operationID) {
		return nil, errors.New("待恢复操作 ID 无效")
	}
	root, h, _, err := w.load(id)
	if err != nil {
		return nil, err
	}
	unlock, err := w.lockWork(root)
	if err != nil {
		return nil, err
	}
	defer unlock()
	var tx pendingTxn
	oldPath := "transactions/" + operationID + ".json"
	if err := loadJSON(root, oldPath, &tx); err != nil {
		return nil, err
	}
	if tx.OperationID != operationID || tx.BaseRevision != h.Revision || tx.TargetRevision != h.Revision+1 {
		return nil, errors.New("事务与当前 HEAD 不匹配；禁止自动处理")
	}
	if current, err := readFile(root, "HEAD.json"); err != nil {
		return nil, err
	} else {
		var headNow head
		if err := decodeJSON(current, &headNow); err != nil {
			return nil, err
		}
		if headNow.Revision != h.Revision || headNow.Manifest != h.Manifest {
			return nil, errors.New("恢复期间 HEAD 变化")
		}
	}
	newPath := "transactions/aborted/" + operationID + ".json"
	oldAbs, err := safePath(root, oldPath)
	if err != nil {
		return nil, err
	}
	newAbs, err := safePath(root, newPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(newAbs), 0700); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(newAbs); err == nil {
		return nil, errors.New("已有同名恢复记录，停止处理")
	}
	if err := os.Rename(oldAbs, newAbs); err != nil {
		return nil, err
	}
	return map[string]any{"workId": id, "revision": h.Revision, "abortedOperation": operationID, "retainedCandidateFiles": true}, nil
}

func (w *workspace) unlockStale(id string, confirmed bool) (any, error) {
	if !confirmed {
		return nil, errors.New("释放异常写入锁需要用户明确确认进程已停止")
	}
	root, err := w.workRoot(id)
	if err != nil {
		return nil, err
	}
	path, err := safePath(root, ".writer.lock")
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	line := strings.SplitN(string(data), "\n", 2)[0]
	if !strings.HasPrefix(line, "pid=") {
		return nil, errors.New("协作锁格式不明，不自动移除")
	}
	pid, err := strconv.Atoi(strings.TrimPrefix(strings.SplitN(line, " ", 2)[0], "pid="))
	if err != nil || pid < 1 {
		return nil, errors.New("协作锁 PID 无效")
	}
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err == nil {
		return nil, errors.New("写入进程仍在运行，不允许解锁")
	}
	if err := os.Remove(path); err != nil {
		return nil, err
	}
	return map[string]any{"workId": id, "releasedStaleLock": true, "priorPid": pid}, nil
}
