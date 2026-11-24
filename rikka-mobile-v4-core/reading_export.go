package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// exportReading creates a separate reading copy. The complete ZIP remains the
// only format that can be imported with revision and evidence intact.
func (w *workspace) exportReading(id, format string) (any, error) {
	if format != "txt" && format != "md" {
		return nil, errors.New("阅读版只支持 txt 或 md")
	}
	if _, err := w.doctor(id); err != nil {
		return nil, err
	}
	root, h, m, err := w.load(id)
	if err != nil {
		return nil, err
	}
	entries, err := w.entries(root, m)
	if err != nil {
		return nil, err
	}
	byID := map[string]entry{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	ordered := entries
	if len(m.ChapterOrder) != 0 {
		ordered = make([]entry, 0, len(entries))
		for _, chapterID := range m.ChapterOrder {
			e, ok := byID[chapterID]
			if !ok {
				return nil, errors.New("章节顺序引用缺失章节")
			}
			ordered = append(ordered, e)
		}
		if len(ordered) != len(entries) {
			return nil, errors.New("章节顺序数量不一致")
		}
	}
	dir := filepath.Join(w.root, "moluo-v4-exports")
	if err := rejectSymlinks(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	token, err := randomHex(8)
	if err != nil {
		return nil, err
	}
	name := id + "-" + revTag(h.Revision) + "-" + token + "." + format
	path, err := safePath(dir, name)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = f.Close()
			_ = os.Remove(path)
		}
	}()
	digest := sha256.New()
	out := io.MultiWriter(f, digest)
	for _, e := range ordered {
		ch, err := loadChapter(root, e.Path)
		if err != nil {
			return nil, err
		}
		actual, err := fileSHA(root, e.Path)
		if err != nil || actual != e.SHA256 {
			return nil, errors.New("导出期间章节哈希变化")
		}
		heading := ch.Title + "\n\n"
		if format == "md" {
			heading = "# " + ch.Title + "\n\n"
		}
		if _, err := io.WriteString(out, heading+ch.Content+"\n\n"); err != nil {
			return nil, err
		}
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	verified, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	readback := sha256.New()
	_, readErr := io.Copy(readback, verified)
	closeErr := verified.Close()
	if readErr != nil || closeErr != nil || hex.EncodeToString(readback.Sum(nil)) != hex.EncodeToString(digest.Sum(nil)) {
		return nil, errors.New("阅读版导出回读哈希错误")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	complete = true
	return map[string]any{"workId": id, "revision": h.Revision, "path": path, "sha256": hex.EncodeToString(digest.Sum(nil)), "bytes": info.Size(), "format": format, "chapters": len(ordered), "importable": false}, nil
}
