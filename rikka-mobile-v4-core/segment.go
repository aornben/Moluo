package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func segmentName(index int) string { return fmt.Sprintf("s%04d.txt", index) }

func (w *workspace) stageSegment(id, op, input string, expected, index int) (any, error) {
	if !safeOpID.MatchString(op) || index < 1 || index > 512 || expected < 1 {
		return nil, errors.New("分段操作 ID、序号或版本无效")
	}
	root, h, _, err := w.load(id)
	if err != nil {
		return nil, err
	}
	if h.Revision != expected {
		return nil, fmt.Errorf("分段版本冲突：当前 %d，预期 %d", h.Revision, expected)
	}
	if !w.backupExists(id, h.Revision) {
		return nil, errors.New("当前版本独立备份缺失")
	}
	clean, err := cleanAbs(input)
	if err != nil {
		return nil, err
	}
	base := filepath.Join(w.inbox, id)
	if filepath.Dir(clean) != base || filepath.Base(clean) != fmt.Sprintf("%s-%s", op, segmentName(index)) {
		return nil, errors.New("分段候选必须位于当前作品隔离 inbox 且名称匹配")
	}
	data, err := readFile(base, filepath.Base(clean))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > 65536 || !utf8.Valid(data) {
		return nil, errors.New("分段正文必须是 1～65536 字节的 UTF-8")
	}
	unlock, err := w.lockWork(root)
	if err != nil {
		return nil, err
	}
	defer unlock()
	_, latest, _, err := w.load(id)
	if err != nil {
		return nil, err
	}
	if latest.Revision != expected {
		return nil, errors.New("分段加锁后版本已变化")
	}
	path := fmt.Sprintf("drafts/%s/%s", op, segmentName(index))
	hashPath := path + ".sha256"
	digest := sumBytes(data)
	prior, err := readFile(root, path)
	if err == nil {
		stored, hashErr := readFile(root, hashPath)
		if hashErr != nil || sumBytes(prior) != digest || strings.TrimSpace(string(stored)) != digest {
			return nil, errors.New("分段已存在但内容、哈希或提交状态不一致")
		}
		return map[string]any{"workId": id, "operationId": op, "index": index, "sha256": digest, "idempotent": true}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := newFile(root, path, data); err != nil {
		return nil, err
	}
	if err := newFile(root, hashPath, []byte(digest+"\n")); err != nil {
		return nil, fmt.Errorf("分段已保存但哈希记录失败，停止后续操作：%w", err)
	}
	return map[string]any{"workId": id, "operationId": op, "index": index, "sha256": digest, "idempotent": false}, nil
}

func (w *workspace) collectSegments(id, op string, count int) (string, string, error) {
	if !safeOpID.MatchString(op) || count < 1 || count > 512 {
		return "", "", errors.New("分段数量或操作 ID 无效")
	}
	root, err := w.workRoot(id)
	if err != nil {
		return "", "", err
	}
	var body strings.Builder
	var digests strings.Builder
	for n := 1; n <= count; n++ {
		path := fmt.Sprintf("drafts/%s/%s", op, segmentName(n))
		data, err := readFile(root, path)
		if err != nil {
			return "", "", fmt.Errorf("缺少第 %d 段正文：%w", n, err)
		}
		hash, err := readFile(root, path+".sha256")
		if err != nil {
			return "", "", fmt.Errorf("缺少第 %d 段哈希：%w", n, err)
		}
		actual := sumBytes(data)
		if actual != strings.TrimSpace(string(hash)) || !utf8.Valid(data) {
			return "", "", fmt.Errorf("第 %d 段哈希或编码错误", n)
		}
		if body.Len()+len(data) > 8<<20 {
			return "", "", errors.New("拼接后的单章正文超过 8 MiB")
		}
		body.Write(data)
		digests.WriteString(actual)
	}
	return body.String(), sumBytes([]byte(digests.String())), nil
}
