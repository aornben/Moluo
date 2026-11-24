package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func recoveryCopyForTest(t *testing.T, w *workspace, id string, revision int) string {
	t.Helper()
	result, err := w.restoreCopy(id, revision)
	if err != nil {
		t.Fatal(err)
	}
	return result.(map[string]any)["recoveryPath"].(string)
}

func appendRecoveryChapter(t *testing.T, w *workspace, id string) {
	t.Helper()
	op := "12121212121212121212121212121212"
	input := testStage(t, w, id, op, "recovery_ch_1", "虚构恢复章节。")
	if _, err := w.appendFromFile(id, input, 1); err != nil {
		t.Fatal(err)
	}
}

func TestAdoptRecoveryReplacesDamagedLiveAndIsIdempotent(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	appendRecoveryChapter(t, w, id)
	recovery := recoveryCopyForTest(t, w, id, 2)
	active, _ := w.workRoot(id)
	if err := os.WriteFile(filepath.Join(active, "HEAD.json"), []byte("{damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	op := "34343434343434343434343434343434"
	if _, err := w.adoptRecovery(id, recovery, op, 2, false); err == nil || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("缺少明确授权未被拒绝：%v", err)
	}
	result, err := w.adoptRecovery(id, recovery, op, 2, true)
	if err != nil {
		t.Fatal(err)
	}
	data := result.(map[string]any)
	if data["activated"] != true || data["idempotent"] != false || data["revision"] != 2 {
		t.Fatalf("激活结果不正确：%v", data)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatalf("激活后 doctor 失败：%v", err)
	}
	previous := data["previousPath"].(string)
	oldHead, err := os.ReadFile(filepath.Join(previous, "HEAD.json"))
	if err != nil || string(oldHead) != "{damaged" {
		t.Fatalf("损坏现场未被原样保护：%q %v", oldHead, err)
	}
	if _, err := os.Stat(recovery); err != nil {
		t.Fatalf("独立恢复副本被移动或删除：%v", err)
	}
	again, err := w.adoptRecovery(id, recovery, op, 2, true)
	if err != nil {
		t.Fatal(err)
	}
	if again.(map[string]any)["idempotent"] != true {
		t.Fatalf("重复操作未返回幂等结果：%v", again)
	}
	root, _ := adoptionRecordRoot(w, id)
	record, found, err := loadAdoptionRecord(root, op)
	if err != nil || !found || record.State != "completed" {
		t.Fatalf("恢复审计记录不完整：%+v %v", record, err)
	}
	otherRecovery := recoveryCopyForTest(t, w, id, 2)
	if _, err := w.adoptRecovery(id, otherRecovery, op, 2, true); err == nil || !strings.Contains(err.Error(), "不同") {
		t.Fatalf("相同 operationId 冒充另一请求未被拒绝：%v", err)
	}
}

func TestAdoptRecoveryRejectsUntrustedOrDamagedCopies(t *testing.T) {
	t.Run("damaged-file", func(t *testing.T) {
		w := testWorkspace(t)
		id := testInit(t, w)
		recovery := recoveryCopyForTest(t, w, id, 1)
		_, h, m, err := func() (string, head, manifest, error) {
			validator := *w
			validator.validationRoot, validator.validationWorkID = recovery, id
			return validator.load(id)
		}()
		if err != nil || h.Revision != 1 {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(recovery, filepath.FromSlash(m.TasksPath)), []byte("[]\n"), 0600); err != nil {
			t.Fatal(err)
		}
		active, _ := w.workRoot(id)
		before := existingHeadSHA(active)
		_, err = w.adoptRecovery(id, recovery, "45454545454545454545454545454545", 1, true)
		if err == nil || !strings.Contains(err.Error(), "哈希") {
			t.Fatalf("篡改副本未被拒绝：%v", err)
		}
		if existingHeadSHA(active) != before {
			t.Fatal("拒绝损坏副本时主作品发生变化")
		}
	})

	t.Run("extra-file-and-outside-path", func(t *testing.T) {
		w := testWorkspace(t)
		id := testInit(t, w)
		recovery := recoveryCopyForTest(t, w, id, 1)
		if err := os.WriteFile(filepath.Join(recovery, "unexpected.json"), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := w.adoptRecovery(id, recovery, "56565656565656565656565656565656", 1, true); err == nil || !strings.Contains(err.Error(), "清单外") {
			t.Fatalf("额外文件未被拒绝：%v", err)
		}
		outside := filepath.Join(w.root, "outside-recovery")
		if err := os.Mkdir(outside, 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := w.adoptRecovery(id, outside, "67676767676767676767676767676767", 1, true); err == nil || !strings.Contains(err.Error(), "直接子目录") {
			t.Fatalf("工作区边界外目录未被拒绝：%v", err)
		}
	})

	t.Run("revision-mismatch", func(t *testing.T) {
		w := testWorkspace(t)
		id := testInit(t, w)
		recovery := recoveryCopyForTest(t, w, id, 1)
		if _, err := w.adoptRecovery(id, recovery, "78787878787878787878787878787878", 2, true); err == nil || !strings.Contains(err.Error(), "目录名") {
			t.Fatalf("版本不匹配未被拒绝：%v", err)
		}
	})

	t.Run("cross-file-reference", func(t *testing.T) {
		w := testWorkspace(t)
		id := testInit(t, w)
		recovery := recoveryCopyForTest(t, w, id, 1)
		validator := *w
		validator.validationRoot, validator.validationWorkID = recovery, id
		_, h, m, err := validator.load(id)
		if err != nil {
			t.Fatal(err)
		}
		// Build an internally hash-consistent but semantically broken isolated
		// fixture. This proves adoption also runs the real cross-file doctor;
		// matching SHA-256 values alone are not accepted as sufficient.
		m.TasksPath = "tasks/missing.json"
		if err := atomicJSON(recovery, h.Manifest, m); err != nil {
			t.Fatal(err)
		}
		h.ManifestSHA256, err = fileSHA(recovery, h.Manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := atomicJSON(recovery, "HEAD.json", h); err != nil {
			t.Fatal(err)
		}
		backupRoot, snap, err := w.loadSnapshot(id, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, relative := range []string{h.Manifest, "HEAD.json"} {
			data, err := readFile(recovery, relative)
			if err != nil {
				t.Fatal(err)
			}
			hash := sumBytes(data)
			if _, err := readFile(backupRoot, objectPath(hash)); errors.Is(err, os.ErrNotExist) {
				if err := newFile(backupRoot, objectPath(hash), data); err != nil {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			snap.Files[relative] = hash
			if relative == "HEAD.json" {
				snap.HeadSHA256 = hash
			}
		}
		if err := atomicJSON(backupRoot, snapshotPath(1), snap); err != nil {
			t.Fatal(err)
		}
		_, err = w.adoptRecovery(id, recovery, "79797979797979797979797979797979", 1, true)
		if err == nil || !strings.Contains(err.Error(), "跨文件完整性") {
			t.Fatalf("哈希一致但跨文件引用损坏的副本未被拒绝：%v", err)
		}
	})
}

func TestAdoptRecoveryRollbackAndRetryAfterRenameFailure(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	appendRecoveryChapter(t, w, id)
	recovery := recoveryCopyForTest(t, w, id, 1)
	active, _ := w.workRoot(id)
	before := existingHeadSHA(active)
	originalRename := adoptRename
	t.Cleanup(func() { adoptRename = originalRename })
	adoptRename = func(oldPath, newPath string) error {
		if strings.HasPrefix(filepath.Base(oldPath), ".adopt-") {
			return errors.New("injected stage rename failure")
		}
		return os.Rename(oldPath, newPath)
	}
	op := "89898989898989898989898989898989"
	if _, err := w.adoptRecovery(id, recovery, op, 1, true); err == nil || !strings.Contains(err.Error(), "已回滚") {
		t.Fatalf("注入失败未触发自动回滚：%v", err)
	}
	if existingHeadSHA(active) != before {
		t.Fatal("激活失败后当前作品未恢复到原状态")
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatalf("回滚后 doctor 失败：%v", err)
	}
	adoptRename = os.Rename
	result, err := w.adoptRecovery(id, recovery, op, 1, true)
	if err != nil {
		t.Fatalf("同一操作故障解除后重试失败：%v", err)
	}
	if result.(map[string]any)["revision"] != 1 {
		t.Fatalf("重试后版本错误：%v", result)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatalf("重试激活后 doctor 失败：%v", err)
	}
}

func TestAdoptRecoveryResumesInterruptedProtectedState(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	appendRecoveryChapter(t, w, id)
	recovery := recoveryCopyForTest(t, w, id, 1)
	active, _ := w.workRoot(id)
	originalRename := adoptRename
	t.Cleanup(func() { adoptRename = originalRename })
	calls := 0
	adoptRename = func(oldPath, newPath string) error {
		calls++
		if calls >= 2 {
			return errors.New("injected interruption")
		}
		return os.Rename(oldPath, newPath)
	}
	op := "90909090909090909090909090909090"
	if _, err := w.adoptRecovery(id, recovery, op, 1, true); err == nil || !strings.Contains(err.Error(), "回滚失败") {
		t.Fatalf("双重故障未保留可恢复状态：%v", err)
	}
	if _, err := os.Stat(active); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("故障注入后预期激活点暂时缺失：%v", err)
	}
	recordRoot, _ := adoptionRecordRoot(w, id)
	record, found, err := loadAdoptionRecord(recordRoot, op)
	if err != nil || !found {
		t.Fatalf("中断日志缺失：%v", err)
	}
	// Simulate a hard process stop: defers would not have removed the two
	// internal writer locks, and the last journal state write may not land.
	record.State = "prepared"
	if err := atomicJSON(recordRoot, op+".json", record); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{record.StagePath, record.PreviousPath} {
		if err := os.WriteFile(filepath.Join(root, ".writer.lock"), []byte("pid=999999 started=2026-11-23T00:00:00Z\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	adoptRename = os.Rename
	result, err := w.adoptRecovery(id, recovery, op, 1, true)
	if err != nil {
		t.Fatalf("中断状态重试失败：%v", err)
	}
	if result.(map[string]any)["activated"] != true {
		t.Fatalf("中断重试未完成激活：%v", result)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatalf("中断重试后 doctor 失败：%v", err)
	}
}
