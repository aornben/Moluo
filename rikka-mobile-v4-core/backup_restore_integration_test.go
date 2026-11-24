package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func newIndependentRestoreFixture(t *testing.T) (*workspace, string, int) {
	t.Helper()
	fixture := newMetricsFixture(t)
	_, h, _, err := fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	// newMetricsFixture deliberately reseals a memory archive in place because
	// its own metrics tests only need read-only fixtures. Rebuild that temporary
	// fixture's current snapshot before exercising the normal mutation path.
	backupRoot, _ := fixture.w.backupRoot(fixture.id)
	if err := os.Remove(filepath.Join(backupRoot, filepath.FromSlash(snapshotPath(h.Revision)))); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.w.backup(fixture.id); err != nil {
		t.Fatal(err)
	}
	blueprint := testRecordStage(t, fixture.w, fixture.id, strings.Repeat("9", 32), "blueprint-set", map[string]any{
		"plan":      map[string]any{"theme": "虚构独立恢复主题", "mainPlot": "验证完整备份恢复"},
		"candidate": map[string]any{"openQuestion": "虚构候选问题"},
		"canon":     map[string]any{"setting": "虚构正史设定"},
	})
	if _, err := fixture.w.mutateFromFile(fixture.id, blueprint, h.Revision); err != nil {
		t.Fatal(err)
	}
	_, h, _, err = fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.w.doctor(fixture.id); err != nil {
		t.Fatal(err)
	}
	return fixture.w, fixture.id, h.Revision
}

func copyDirectoryForTest(t *testing.T, source, target string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("test fixture unexpectedly contains a symlink")
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0600)
	}); err != nil {
		t.Fatal(err)
	}
}

func readZipEntries(t *testing.T, path string) map[string][]byte {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	entries := map[string][]byte{}
	for _, file := range reader.File {
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(stream)
		_ = stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		entries[file.Name] = data
	}
	return entries
}

func writeZipEntries(t *testing.T, path string, entries map[string][]byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(entries[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func copyExportIntoWorkspace(t *testing.T, target *workspace, data []byte, name string) string {
	t.Helper()
	dir := filepath.Join(target.root, "moluo-v4-imports")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertRestoredWork(t *testing.T, w *workspace, id string, revision int, wanted map[string]string) {
	t.Helper()
	root, h, m, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	if h.Revision != revision || m.Revision != revision || h.ManifestSHA256 == "" {
		t.Fatalf("恢复后的 revision 或 manifest 封存不一致：%+v %+v", h, m)
	}
	if got := snapshotWorkFiles(t, root); !reflect.DeepEqual(got, wanted) {
		t.Fatal("恢复后的逐文件 SHA-256 与原作品不一致")
	}
	if _, err := w.doctor(id); err != nil {
		t.Fatalf("恢复后的 doctor 失败：%v", err)
	}
	for _, kind := range []string{"chapters", "characters", "entities", "tasks", "memory", "blueprint"} {
		result, err := w.query(id, kind, "", "", 0, 20, 0)
		if err != nil || result == nil {
			t.Fatalf("恢复后的 %s 查询失败：%v", kind, err)
		}
	}
	if !w.backupExists(id, revision) {
		t.Fatal("恢复后的独立备份没有通过回读验证")
	}
}

func TestIndependentBackupRestoresCompleteWorkWithoutSource(t *testing.T) {
	source, id, revision := newIndependentRestoreFixture(t)
	sourceRoot, _ := source.workRoot(id)
	wanted := snapshotWorkFiles(t, sourceRoot)
	_, sourceHead, sourceManifest, err := source.load(id)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := source.exportWork(id)
	if err != nil {
		t.Fatal(err)
	}
	exportResult := exported.(map[string]any)
	exportPath := exportResult["path"].(string)
	exportData, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(exportData)
	if hex.EncodeToString(digest[:]) != exportResult["sha256"] {
		t.Fatal("导出 ZIP 的独立 SHA-256 与回报不一致")
	}

	backupTarget := testWorkspace(t)
	sourceBackup, _ := source.backupRoot(id)
	targetBackup, _ := backupTarget.backupRoot(id)
	copyDirectoryForTest(t, sourceBackup, targetBackup)
	importTarget := testWorkspace(t)
	importPath := copyExportIntoWorkspace(t, importTarget, exportData, "independent.zip")

	// 删除的只是 t.TempDir() 中的虚构源工作区；两个恢复目标此后不能
	// 读取源作品、源备份或源导出文件。
	if err := os.RemoveAll(source.root); err != nil {
		t.Fatal(err)
	}
	if _, err := backupTarget.restore(id, revision); err != nil {
		t.Fatalf("全新工作区从独立快照恢复失败：%v", err)
	}
	assertRestoredWork(t, backupTarget, id, revision, wanted)
	if _, err := importTarget.importWork(importPath); err != nil {
		t.Fatalf("全新工作区从独立 ZIP 恢复失败：%v", err)
	}
	assertRestoredWork(t, importTarget, id, revision, wanted)

	restoredRoot, restoredHead, restoredManifest, err := backupTarget.load(id)
	if err != nil {
		t.Fatal(err)
	}
	if restoredHead.ManifestSHA256 != sourceHead.ManifestSHA256 || restoredManifest.TasksPath != sourceManifest.TasksPath ||
		restoredManifest.MemoryPath != sourceManifest.MemoryPath || restoredManifest.BlueprintPath != sourceManifest.BlueprintPath {
		t.Fatal("恢复后的 HEAD、任务、记忆或蓝图引用与源版本不一致")
	}
	if _, err := loadTaskArchive(restoredRoot, restoredManifest.TaskArchives[0]); err != nil {
		t.Fatalf("恢复后的任务归档无法回读：%v", err)
	}
	if len(restoredManifest.MemoryArchives) == 0 {
		t.Fatal("恢复后的记忆归档缺失")
	}

	continued := testStage(t, backupTarget, id, strings.Repeat("e", 32), "ch_after_restore", "恢复后继续写入的虚构章节。")
	if _, err := backupTarget.appendFromFile(id, continued, revision); err != nil {
		t.Fatalf("恢复后的作品不能继续正常写入：%v", err)
	}
	if _, err := backupTarget.doctor(id); err != nil {
		t.Fatalf("恢复后继续写入导致 doctor 失败：%v", err)
	}
	if _, err := backupTarget.restore(id, revision); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("重复恢复没有拒绝目标冲突：%v", err)
	}
	if _, err := importTarget.importWork(importPath); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("重复导入没有拒绝目标冲突：%v", err)
	}
}

func makeSemanticallyBrokenExport(t *testing.T, sourcePath, targetPath string, incompatibleVersion bool) {
	t.Helper()
	entries := readZipEntries(t, sourcePath)
	var exportInfo exportManifest
	if err := json.Unmarshal(entries["EXPORT_MANIFEST.json"], &exportInfo); err != nil {
		t.Fatal(err)
	}
	var h head
	if err := json.Unmarshal(entries["HEAD.json"], &h); err != nil {
		t.Fatal(err)
	}
	if incompatibleVersion {
		h.SchemaVersion = 999
	} else {
		var m manifest
		if err := json.Unmarshal(entries[h.Manifest], &m); err != nil {
			t.Fatal(err)
		}
		m.TasksPath = "tasks/missing.json"
		entries[h.Manifest], _ = json.Marshal(m)
		h.ManifestSHA256 = sumBytes(entries[h.Manifest])
		exportInfo.Files[h.Manifest] = h.ManifestSHA256
	}
	entries["HEAD.json"], _ = json.Marshal(h)
	exportInfo.Files["HEAD.json"] = sumBytes(entries["HEAD.json"])
	entries["EXPORT_MANIFEST.json"], _ = json.Marshal(exportInfo)
	writeZipEntries(t, targetPath, entries)
}

func assertImportFailureIsAtomic(t *testing.T, w *workspace, id, input string) error {
	t.Helper()
	_, err := w.importWork(input)
	if err == nil {
		t.Fatal("损坏导入包被接受")
	}
	root, _ := w.workRoot(id)
	if _, statErr := os.Lstat(root); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("失败导入留下正式作品目录：%v", statErr)
	}
	stages, _ := filepath.Glob(filepath.Join(w.works, ".import-"+id+"-*"))
	if len(stages) != 0 {
		t.Fatalf("失败导入留下暂存目录：%v", stages)
	}
	return err
}

func TestIndependentImportRejectsDamageAndInvalidStructureAtomically(t *testing.T) {
	source, id, _ := newIndependentRestoreFixture(t)
	exported, err := source.exportWork(id)
	if err != nil {
		t.Fatal(err)
	}
	sourcePath := exported.(map[string]any)["path"].(string)

	t.Run("missing-file", func(t *testing.T) {
		w := testWorkspace(t)
		path := copyExportIntoWorkspace(t, w, []byte{}, "missing.zip")
		entries := readZipEntries(t, sourcePath)
		var info exportManifest
		_ = json.Unmarshal(entries["EXPORT_MANIFEST.json"], &info)
		for name := range info.Files {
			if name != "HEAD.json" && name != "work.json" {
				delete(entries, name)
				break
			}
		}
		writeZipEntries(t, path, entries)
		assertImportFailureIsAtomic(t, w, id, path)
	})

	t.Run("hash-tamper", func(t *testing.T) {
		w := testWorkspace(t)
		path := copyExportIntoWorkspace(t, w, []byte{}, "tampered.zip")
		entries := readZipEntries(t, sourcePath)
		var info exportManifest
		_ = json.Unmarshal(entries["EXPORT_MANIFEST.json"], &info)
		for name := range info.Files {
			if strings.HasPrefix(name, "chapters/") {
				entries[name] = append(entries[name], []byte("篡改")...)
				break
			}
		}
		writeZipEntries(t, path, entries)
		assertImportFailureIsAtomic(t, w, id, path)
	})

	t.Run("cross-file-reference", func(t *testing.T) {
		w := testWorkspace(t)
		path := copyExportIntoWorkspace(t, w, []byte{}, "cross-reference.zip")
		makeSemanticallyBrokenExport(t, sourcePath, path, false)
		err := assertImportFailureIsAtomic(t, w, id, path)
		if !strings.Contains(err.Error(), "完整性") {
			t.Fatalf("跨文件引用损坏没有明确完整性错误：%v", err)
		}
	})

	t.Run("incompatible-version", func(t *testing.T) {
		w := testWorkspace(t)
		path := copyExportIntoWorkspace(t, w, []byte{}, "incompatible.zip")
		makeSemanticallyBrokenExport(t, sourcePath, path, true)
		err := assertImportFailureIsAtomic(t, w, id, path)
		if !strings.Contains(err.Error(), "完整性") {
			t.Fatalf("不兼容版本没有明确完整性错误：%v", err)
		}
	})

	t.Run("illegal-path", func(t *testing.T) {
		w := testWorkspace(t)
		path := copyExportIntoWorkspace(t, w, []byte{}, "illegal.zip")
		entries := readZipEntries(t, sourcePath)
		entries["../escape.json"] = []byte("{}")
		writeZipEntries(t, path, entries)
		assertImportFailureIsAtomic(t, w, id, path)
		if _, err := os.Stat(filepath.Join(w.root, "escape.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("非法 ZIP 路径写出了导入目录")
		}
	})
}

func assertRestoreFailureIsAtomic(t *testing.T, w *workspace, id string, revision int) error {
	t.Helper()
	_, err := w.restore(id, revision)
	if err == nil {
		t.Fatal("损坏独立备份被恢复")
	}
	root, _ := w.workRoot(id)
	if _, statErr := os.Lstat(root); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("失败恢复留下正式作品目录：%v", statErr)
	}
	stages, _ := filepath.Glob(filepath.Join(w.works, ".restore-"+id+"-*"))
	if len(stages) != 0 {
		t.Fatalf("失败恢复留下暂存目录：%v", stages)
	}
	return err
}

func TestIndependentSnapshotRestoreRejectsDamageAtomically(t *testing.T) {
	for _, scenario := range []string{"missing-object", "tampered-object", "illegal-path"} {
		t.Run(scenario, func(t *testing.T) {
			source, id, revision := newIndependentRestoreFixture(t)
			target := testWorkspace(t)
			sourceBackup, _ := source.backupRoot(id)
			targetBackup, _ := target.backupRoot(id)
			copyDirectoryForTest(t, sourceBackup, targetBackup)
			_, snap, err := target.loadSnapshot(id, revision)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing-object":
				if err := os.Remove(filepath.Join(targetBackup, filepath.FromSlash(objectPath(snap.HeadSHA256)))); err != nil {
					t.Fatal(err)
				}
			case "tampered-object":
				if err := os.WriteFile(filepath.Join(targetBackup, filepath.FromSlash(objectPath(snap.HeadSHA256))), []byte("tampered"), 0600); err != nil {
					t.Fatal(err)
				}
			case "illegal-path":
				snap.Files["../escape.json"] = strings.Repeat("0", 64)
				if err := atomicJSON(targetBackup, snapshotPath(revision), snap); err != nil {
					t.Fatal(err)
				}
			}
			assertRestoreFailureIsAtomic(t, target, id, revision)
		})
	}
}
