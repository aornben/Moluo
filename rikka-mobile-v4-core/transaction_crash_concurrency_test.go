package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func withTransactionFault(t *testing.T, point string) {
	t.Helper()
	original := transactionFault
	transactionFault = func(actual string) error {
		if actual == point {
			return errors.New("injected process interruption at " + point)
		}
		return nil
	}
	t.Cleanup(func() { transactionFault = original })
}

func assertCurrentHead(t *testing.T, w *workspace, id string, revision int, headSHA string) {
	t.Helper()
	root, h, _, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	if h.Revision != revision {
		t.Fatalf("HEAD revision = %d, want %d", h.Revision, revision)
	}
	if headSHA != "" {
		actual, err := fileSHA(root, "HEAD.json")
		if err != nil || actual != headSHA {
			t.Fatalf("旧 HEAD 未保持不变：%s %v", actual, err)
		}
	}
}

func TestAppendCrashCheckpointsRestartAndRecovery(t *testing.T) {
	points := []string{"append:data-written", "append:manifest-written", "append:before-head-switch", "append:head-switched"}
	freshDigits := []string{"7", "8", "9", "e"}
	for index, point := range points {
		t.Run(strings.ReplaceAll(point, ":", "-"), func(t *testing.T) {
			w := testWorkspace(t)
			id := testInit(t, w)
			root, _, _, _ := w.load(id)
			oldHead, _ := fileSHA(root, "HEAD.json")
			op := strings.Repeat(string(rune('a'+index)), 32)
			content := "虚构中断正文：" + point
			input := testStage(t, w, id, op, "crash_ch", content)
			withTransactionFault(t, point)
			if _, err := w.appendFromFile(id, input, 1); err == nil || !strings.Contains(err.Error(), "injected process interruption") {
				t.Fatalf("故障点没有中断真实提交：%v", err)
			}
			transactionFault = func(string) error { return nil }

			reopened, err := openWorkspace(w.root)
			if err != nil {
				t.Fatal(err)
			}
			if point == "append:head-switched" {
				assertCurrentHead(t, reopened, id, 2, "")
				result, err := reopened.appendFromFile(id, input, 1)
				if err != nil {
					t.Fatalf("HEAD 已提交后的同 operationId 恢复失败：%v", err)
				}
				if result.(map[string]any)["idempotent"] != true {
					t.Fatalf("HEAD 已提交后的重试不是幂等结果：%v", result)
				}
				page, err := reopened.readChapter(id, "crash_ch", 0, 8192)
				if err != nil || page.(map[string]any)["text"] != content {
					t.Fatalf("恢复后的正文不一致：%v %v", page, err)
				}
				changed := testStage(t, reopened, id, op, "crash_ch", "冒用相同 operationId 的不同正文")
				if _, err := reopened.appendFromFile(id, changed, 1); err == nil || !strings.Contains(err.Error(), "不同候选") {
					t.Fatalf("不同请求冒用 operationId 未被拒绝：%v", err)
				}
			} else {
				assertCurrentHead(t, reopened, id, 1, oldHead)
				if _, err := reopened.doctor(id); err == nil || !strings.Contains(err.Error(), "未完成事务") {
					t.Fatalf("中断事务未阻止健康写入：%v", err)
				}
				if _, err := reopened.recoverPending(id, op, true); err != nil {
					t.Fatalf("明确确认后放弃未提交事务失败：%v", err)
				}
				if _, err := reopened.doctor(id); err != nil {
					t.Fatalf("放弃中断事务后 doctor 失败：%v", err)
				}
				chapters, err := reopened.query(id, "chapters", "", "", 0, 20, 0)
				if err != nil || chapters.(map[string]any)["total"] != 0 {
					t.Fatalf("HEAD 前中断错误暴露了未提交正文：%v %v", chapters, err)
				}
				tasks, err := reopened.query(id, "tasks", "", "", 0, 20, 0)
				if err != nil || tasks.(map[string]any)["total"] != 0 {
					t.Fatalf("HEAD 前中断改变了任务：%v %v", tasks, err)
				}
				freshOp := strings.Repeat(freshDigits[index], 32)
				fresh := testStage(t, reopened, id, freshOp, "after_crash", "中断恢复后正常写入。")
				if _, err := reopened.appendFromFile(id, fresh, 1); err != nil {
					t.Fatalf("中断恢复后不能继续写入：%v", err)
				}
			}
			if _, err := reopened.doctor(id); err != nil {
				t.Fatalf("故障恢复后的 doctor 失败：%v", err)
			}
		})
	}
}

func TestManifestWriteAndHeadRenameFailuresRemainAtomic(t *testing.T) {
	t.Run("manifest-write", func(t *testing.T) {
		w := testWorkspace(t)
		id := testInit(t, w)
		root, _, _, _ := w.load(id)
		oldHead, _ := fileSHA(root, "HEAD.json")
		op := strings.Repeat("1", 32)
		blockedManifest := filepath.Join(root, "manifests", revTag(2)+"-"+op+".json")
		if err := os.Mkdir(blockedManifest, 0700); err != nil {
			t.Fatal(err)
		}
		input := testStage(t, w, id, op, "manifest_fail", "manifest 写入失败前的虚构正文。")
		if _, err := w.appendFromFile(id, input, 1); err == nil {
			t.Fatal("manifest 实际文件系统写入故障未中断提交")
		}
		assertCurrentHead(t, w, id, 1, oldHead)
		if _, err := w.recoverPending(id, op, true); err != nil {
			t.Fatal(err)
		}
		if _, err := w.doctor(id); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("head-atomic-rename", func(t *testing.T) {
		w := testWorkspace(t)
		id := testInit(t, w)
		root, _, _, _ := w.load(id)
		oldHead, _ := fileSHA(root, "HEAD.json")
		op := strings.Repeat("2", 32)
		input := testStage(t, w, id, op, "head_fail", "HEAD 原子切换失败前的虚构正文。")
		original := atomicRename
		atomicRename = func(oldPath, newPath string) error {
			if filepath.Base(newPath) == "HEAD.json" {
				return errors.New("injected atomic HEAD rename failure")
			}
			return os.Rename(oldPath, newPath)
		}
		t.Cleanup(func() { atomicRename = original })
		if _, err := w.appendFromFile(id, input, 1); err == nil || !strings.Contains(err.Error(), "HEAD rename") {
			t.Fatalf("HEAD rename 故障没有返回：%v", err)
		}
		atomicRename = os.Rename
		assertCurrentHead(t, w, id, 1, oldHead)
		temps, _ := filepath.Glob(filepath.Join(root, "HEAD.json.tmp-*"))
		if len(temps) != 0 {
			t.Fatalf("HEAD 原子切换失败留下临时文件：%v", temps)
		}
		if _, err := w.recoverPending(id, op, true); err != nil {
			t.Fatal(err)
		}
		if _, err := w.doctor(id); err != nil {
			t.Fatal(err)
		}
	})
}

func TestMutationAfterHeadCrashRecoversTaskAndRejectsOperationReuse(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	op := strings.Repeat("3", 32)
	input := testRecordStage(t, w, id, op, "task-add", map[string]any{
		"id": "task_crash", "title": "中断事务任务", "plannedChapter": float64(1), "priority": "normal", "status": "todo",
	})
	withTransactionFault(t, "mutation:head-switched")
	if _, err := w.mutateFromFile(id, input, 1); err == nil {
		t.Fatal("mutation HEAD 后中断没有返回错误")
	}
	transactionFault = func(string) error { return nil }
	reopened, err := openWorkspace(w.root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := reopened.mutateFromFile(id, input, 1)
	if err != nil || result.(map[string]any)["idempotent"] != true {
		t.Fatalf("任务提交中断后的幂等恢复失败：%v %v", result, err)
	}
	query, err := reopened.query(id, "tasks", "中断事务任务", "", 0, 20, 0)
	if err != nil || query.(map[string]any)["total"] != 1 {
		t.Fatalf("恢复后的任务不一致：%v %v", query, err)
	}
	different := testRecordStage(t, reopened, id, op, "task-add", map[string]any{
		"id": "task_other", "title": "冒用操作 ID", "plannedChapter": float64(1), "priority": "normal", "status": "todo",
	})
	if _, err := reopened.mutateFromFile(id, different, 1); err == nil {
		t.Fatal("不同任务冒用同一 operationId 被接受")
	}
	if _, err := reopened.doctor(id); err != nil {
		t.Fatal(err)
	}
}

func TestTransactionProcessHelper(t *testing.T) {
	if os.Getenv("MOLUO_TX_HELPER") != "1" {
		return
	}
	workspaceRoot := os.Getenv("MOLUO_TX_WORKSPACE")
	id := os.Getenv("MOLUO_TX_WORK")
	mode := os.Getenv("MOLUO_TX_MODE")
	w, err := openWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	root, err := w.workRoot(id)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "leave-lock" {
		if _, err := w.lockWork(root); err != nil {
			t.Fatal(err)
		}
		return
	}
	barrier := os.Getenv("MOLUO_TX_BARRIER")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(barrier); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("waiting for subprocess barrier timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := w.appendFromFile(id, os.Getenv("MOLUO_TX_INPUT"), 1); err != nil {
		t.Fatal(err)
	}
}

func transactionHelperCommand(w *workspace, id, input, barrier, mode string) (*exec.Cmd, *bytes.Buffer) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestTransactionProcessHelper$")
	cmd.Env = append(os.Environ(),
		"MOLUO_TX_HELPER=1", "MOLUO_TX_WORKSPACE="+w.root, "MOLUO_TX_WORK="+id,
		"MOLUO_TX_INPUT="+input, "MOLUO_TX_BARRIER="+barrier, "MOLUO_TX_MODE="+mode)
	buffer := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = buffer, buffer
	return cmd, buffer
}

func TestTwoProcessesSerializeWritesAndStaleLockNeedsConfirmation(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	first := testStage(t, w, id, strings.Repeat("4", 32), "process_ch_1", strings.Repeat("甲", 10000))
	second := testStage(t, w, id, strings.Repeat("5", 32), "process_ch_2", strings.Repeat("乙", 10000))
	barrier := filepath.Join(w.root, "process-barrier")
	cmd1, output1 := transactionHelperCommand(w, id, first, barrier, "write")
	cmd2, output2 := transactionHelperCommand(w, id, second, barrier, "write")
	if err := cmd1.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmd2.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(barrier, []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	errorsByProcess := []error{cmd1.Wait(), cmd2.Wait()}
	outputs := []string{output1.String(), output2.String()}
	successes := 0
	for index, err := range errorsByProcess {
		if err == nil {
			successes++
			continue
		}
		if !strings.Contains(outputs[index], "正被其他写入占用") && !strings.Contains(outputs[index], "版本冲突") {
			t.Fatalf("并发失败不是锁或 revision 冲突：%v %s", err, outputs[index])
		}
	}
	if successes != 1 {
		t.Fatalf("两个真实进程应恰好一个提交成功：errors=%v output1=%s output2=%s", errorsByProcess, output1.String(), output2.String())
	}
	_, h, m, err := w.load(id)
	if err != nil || h.Revision != 2 || m.ChapterCount != 1 {
		t.Fatalf("多进程写入后的 HEAD/章节不一致：%+v %+v %v", h, m, err)
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatalf("多进程冲突后的 doctor 失败：%v", err)
	}

	lockCmd, lockOutput := transactionHelperCommand(w, id, "", "", "leave-lock")
	if err := lockCmd.Run(); err != nil {
		t.Fatalf("锁残留子进程失败：%v %s", err, lockOutput.String())
	}
	root, _ := w.workRoot(id)
	if _, err := os.Stat(filepath.Join(root, ".writer.lock")); err != nil {
		t.Fatalf("子进程没有留下真实写入锁：%v", err)
	}
	reopened, err := openWorkspace(w.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.unlockStale(id, false); err == nil {
		t.Fatal("未经明确确认清除了子进程残留锁")
	}
	if _, err := reopened.unlockStale(id, true); err != nil {
		t.Fatalf("确认进程结束后不能清除残留锁：%v", err)
	}
	if _, err := reopened.doctor(id); err != nil {
		t.Fatalf("清除残留锁后 doctor 失败：%v", err)
	}
}

func TestRecoveryProtectionDirectoryIsNeverOverwritten(t *testing.T) {
	w := testWorkspace(t)
	id := testInit(t, w)
	appendRecoveryChapter(t, w, id)
	recovery := recoveryCopyForTest(t, w, id, 1)
	previousBase := filepath.Join(w.root, "moluo-v4-recovery-previous", id)
	existing := filepath.Join(previousBase, "existing-protection")
	if err := os.MkdirAll(existing, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(existing, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("must remain"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := w.adoptRecovery(id, recovery, strings.Repeat("6", 32), 1, true)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "must remain" {
		t.Fatalf("已有恢复保护目录被覆盖：%q %v", data, err)
	}
	if result.(map[string]any)["previousPath"] == existing {
		t.Fatal("新恢复错误复用了已有保护目录")
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatal(err)
	}
}
