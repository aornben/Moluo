package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxExportBytes int64 = 512 << 20
const maxExportFiles = 100000

type exportManifest struct {
	Format    string            `json:"format"`
	WorkID    string            `json:"workId"`
	Revision  int               `json:"revision"`
	Files     map[string]string `json:"files"`
	CreatedAt string            `json:"createdAt"`
}

func (w *workspace) exportWork(id string) (any, error) {
	root, h, _, err := w.load(id)
	if err != nil {
		return nil, err
	}
	unlock, err := w.lockWork(root)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if _, err := w.doctor(id); err != nil {
		return nil, err
	}
	files := map[string]string{}
	var total int64
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("作品内存在符号链接，停止导出")
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".writer.lock" || strings.Contains(rel, ".tmp-") {
			return nil
		}
		data, err := readFile(root, rel)
		if err != nil {
			return err
		}
		total += int64(len(data))
		if total > maxExportBytes || len(files) >= maxExportFiles {
			return errors.New("导出文件数或总容量超过安全上限")
		}
		files[rel] = sumBytes(data)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if files["HEAD.json"] == "" || files["work.json"] == "" {
		return nil, errors.New("导出缺少关键文件")
	}
	exportDir := filepath.Join(w.root, "moluo-v4-exports")
	if err := os.MkdirAll(exportDir, 0700); err != nil {
		return nil, err
	}
	name := id + "-" + revTag(h.Revision) + ".zip"
	path, err := safePath(exportDir, name)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = os.Remove(path)
		}
	}()
	archive := zip.NewWriter(f)
	for _, relative := range sortedKeys(files) {
		data, err := readFile(root, relative)
		if err != nil {
			_ = archive.Close()
			_ = f.Close()
			return nil, err
		}
		if sumBytes(data) != files[relative] {
			_ = archive.Close()
			_ = f.Close()
			return nil, errors.New("导出期间作品文件变化")
		}
		writer, err := archive.Create(relative)
		if err != nil {
			_ = archive.Close()
			_ = f.Close()
			return nil, err
		}
		if _, err := writer.Write(data); err != nil {
			_ = archive.Close()
			_ = f.Close()
			return nil, err
		}
	}
	manifest := exportManifest{Format: "moluo-v4-export", WorkID: id, Revision: h.Revision, Files: files, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		_ = archive.Close()
		_ = f.Close()
		return nil, err
	}
	writer, err := archive.Create("EXPORT_MANIFEST.json")
	if err != nil {
		_ = archive.Close()
		_ = f.Close()
		return nil, err
	}
	if _, err := writer.Write(manifestData); err != nil {
		_ = archive.Close()
		_ = f.Close()
		return nil, err
	}
	if err := archive.Close(); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if _, err := inspectExport(path); err != nil {
		return nil, err
	}
	failed = false
	zipFile, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	digest := sha256.New()
	_, err = io.Copy(digest, zipFile)
	_ = zipFile.Close()
	if err != nil {
		return nil, err
	}
	hash := hex.EncodeToString(digest.Sum(nil))
	return map[string]any{"workId": id, "revision": h.Revision, "path": path, "sha256": hash, "files": len(files), "uncompressedBytes": total}, nil
}

func safeZipName(name string) bool {
	if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return false
	}
	if !filepath.IsLocal(filepath.FromSlash(name)) {
		return false
	}
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(name))) == name
}

func inspectExport(path string) (exportManifest, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return exportManifest{}, err
	}
	defer archive.Close()
	if len(archive.File) < 2 || len(archive.File) > maxExportFiles+1 {
		return exportManifest{}, errors.New("导出包文件数量无效")
	}
	actual := map[string]string{}
	seen := map[string]bool{}
	var manifest exportManifest
	var total int64
	for _, file := range archive.File {
		if file.FileInfo().IsDir() || file.Mode()&os.ModeSymlink != 0 || !safeZipName(file.Name) {
			return manifest, errors.New("导出包包含非法路径、目录或符号链接")
		}
		if seen[file.Name] {
			return manifest, errors.New("导出包包含重复文件名")
		}
		seen[file.Name] = true
		if file.UncompressedSize64 > maxJSONFile {
			return manifest, errors.New("导出包单文件超过 16 MiB")
		}
		total += int64(file.UncompressedSize64)
		if total > maxExportBytes {
			return manifest, errors.New("导出包解压后超过 512 MiB")
		}
		reader, err := file.Open()
		if err != nil {
			return manifest, err
		}
		data, err := io.ReadAll(io.LimitReader(reader, maxJSONFile+1))
		closeErr := reader.Close()
		if err != nil {
			return manifest, err
		}
		if closeErr != nil {
			return manifest, closeErr
		}
		if len(data) > maxJSONFile || uint64(len(data)) != file.UncompressedSize64 {
			return manifest, errors.New("导出包文件长度无效")
		}
		if file.Name == "EXPORT_MANIFEST.json" {
			if err := decodeJSON(data, &manifest); err != nil {
				return manifest, err
			}
		} else {
			actual[file.Name] = sumBytes(data)
		}
	}
	if manifest.Format != "moluo-v4-export" || !safeWorkID.MatchString(manifest.WorkID) || manifest.Revision < 1 || len(manifest.Files) != len(actual) {
		return manifest, errors.New("导出清单格式或作品 ID 无效")
	}
	for name, hash := range manifest.Files {
		if actual[name] != hash {
			return manifest, fmt.Errorf("导出文件内容哈希不一致：%s", name)
		}
	}
	if actual["HEAD.json"] == "" || actual["work.json"] == "" {
		return manifest, errors.New("导出包缺少 HEAD 或作品标识")
	}
	return manifest, nil
}

func (w *workspace) importWork(input string) (any, error) {
	clean, err := cleanAbs(input)
	if err != nil {
		return nil, err
	}
	importDir := filepath.Join(w.root, "moluo-v4-imports")
	if filepath.Dir(clean) != importDir || !strings.HasSuffix(strings.ToLower(clean), ".zip") {
		return nil, errors.New("导入文件必须位于当前工作区 moluo-v4-imports 目录")
	}
	manifest, err := inspectExport(clean)
	if err != nil {
		return nil, err
	}
	root, err := w.workRoot(manifest.WorkID)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(root); err == nil {
		return nil, errors.New("相同作品 ID 已存在；禁止覆盖导入")
	}
	if err := os.MkdirAll(w.works, 0700); err != nil {
		return nil, err
	}
	token, err := randomHex(8)
	if err != nil {
		return nil, err
	}
	stage := filepath.Join(w.works, ".import-"+manifest.WorkID+"-"+token)
	if err := os.Mkdir(stage, 0700); err != nil {
		return nil, err
	}
	stageActive := true
	defer func() {
		if stageActive {
			_ = os.RemoveAll(stage)
		}
	}()
	archive, err := zip.OpenReader(clean)
	if err != nil {
		return nil, err
	}
	defer archive.Close()
	for _, file := range archive.File {
		if file.Name == "EXPORT_MANIFEST.json" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(reader, maxJSONFile+1))
		_ = reader.Close()
		if err != nil {
			return nil, err
		}
		if sumBytes(data) != manifest.Files[file.Name] {
			return nil, errors.New("导入时文件内容发生变化")
		}
		if err := newFile(stage, file.Name, data); err != nil {
			return nil, err
		}
	}
	var h head
	if err := loadJSON(stage, "HEAD.json", &h); err != nil {
		return nil, err
	}
	if h.WorkID != manifest.WorkID || h.Revision != manifest.Revision {
		return nil, errors.New("导入包 HEAD 与清单的作品或版本不匹配")
	}
	var info map[string]any
	if err := loadJSON(stage, "work.json", &info); err != nil {
		return nil, err
	}
	if info["workId"] != manifest.WorkID {
		return nil, errors.New("导入包作品标识不匹配")
	}
	validator := *w
	validator.validationRoot = stage
	validator.validationWorkID = manifest.WorkID
	if _, err := validator.doctorCore(manifest.WorkID, false); err != nil {
		return nil, fmt.Errorf("导入数据完整性检查失败：%w", err)
	}
	if _, err := os.Lstat(root); err == nil {
		return nil, errors.New("导入时目标作品目录被创建")
	}
	if err := rejectSymlinks(root); err != nil {
		return nil, err
	}
	if err := os.Rename(stage, root); err != nil {
		return nil, err
	}
	stageActive = false
	result := map[string]any{"workId": manifest.WorkID, "revision": manifest.Revision, "files": len(manifest.Files)}
	if err := w.backupWork(manifest.WorkID, root, h); err != nil {
		return result, savedBackupError{Cause: errors.New("作品已导入、独立备份失败：" + err.Error())}
	}
	if _, err := w.doctor(manifest.WorkID); err != nil {
		return result, savedBackupError{Cause: errors.New("作品已导入、健康检查未通过：" + err.Error())}
	}
	result["backup"] = "正常"
	return result, nil
}
