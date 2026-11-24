package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const buildVersion = "4.9.3-dev"

type output struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
}

func emit(status, message string, data any) {
	_ = json.NewEncoder(os.Stdout).Encode(output{Status: status, Message: message, Data: data})
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("缺少命令：version、init、status、doctor、new-op、segment、prepare、read-prepare、draft、review、finalize、chapter-history、chapter-rollback、mutate、read、query、backup、recover、unlock、restore、restore-copy、adopt-recovery、export、export-legacy、export-reading、import、import-legacy、import-old-v4、metrics")
	}
	if args[0] == "version" {
		emit("正常", "墨络手机核心程序", map[string]any{"version": buildVersion, "goos": runtime.GOOS, "goarch": runtime.GOARCH})
		return nil
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	f.SetOutput(os.Stderr)
	workspace := f.String("workspace", "/workspace", "RikkaHub 工作区文件根目录")
	work := f.String("work", "", "作品稳定 ID")
	title := f.String("title", "", "新作品名称")
	input := f.String("input", "", "候选 JSON 绝对路径")
	expected := f.Int("revision", -1, "预期 HEAD revision")
	id := f.String("id", "", "章节稳定 ID")
	cursor := f.Int("cursor", 0, "正文 UTF-8 字节游标")
	limit := f.Int("limit", 8192, "单次返回正文 UTF-8 字节上限")
	kind := f.String("kind", "", "查询种类：chapters、characters、tasks、entities、memory、blueprint、states")
	keyword := f.String("query", "", "查询关键词")
	character := f.String("character", "", "按人物 ID 过滤")
	chapterNumber := f.Int("chapter", 0, "待办查询目标章节")
	fromChapter := f.Int("from-chapter", 0, "查询起始章节号")
	toChapter := f.Int("to-chapter", 0, "查询结束章节号")
	statusFilter := f.String("status", "", "按状态过滤：章节/待办/实体")
	fromDate := f.String("from-date", "", "记忆日期过滤起始（YYYY-MM-DD）")
	toDate := f.String("to-date", "", "记忆日期过滤结束（YYYY-MM-DD）")
	readingFormat := f.String("format", "", "阅读版导出格式：txt 或 md")
	prepareID := f.String("prepare", "", "prepare 记录 ID")
	prepareSection := f.String("section", "context", "prepare 分页内容：context 或 requirements")
	operation := f.String("operation", "", "待恢复的操作 ID")
	index := f.Int("index", 0, "分段序号，从 1 开始")
	confirm := f.Bool("confirm", false, "用户已确认清除异常锁或放弃未提交事务")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	w, err := openWorkspace(*workspace)
	if err != nil {
		return err
	}
	var data any
	switch args[0] {
	case "init":
		if *title == "" {
			return errors.New("初始化需要 --title")
		}
		data, err = w.initWork(*title)
	case "status":
		data, err = w.status(*work)
	case "doctor":
		data, err = w.doctor(*work)
	case "new-op":
		data, err = w.newOperation(*work)
	case "segment":
		data, err = w.stageSegment(*work, *operation, *input, *expected, *index)
	case "prepare":
		if *expected < 1 || *id == "" {
			return errors.New("prepare 需要 --work、--id 和 --revision")
		}
		data, err = w.prepareChapter(*work, *id, *chapterNumber, *expected)
	case "read-prepare":
		if *prepareID == "" {
			return errors.New("read-prepare 需要 --prepare")
		}
		data, err = w.readPrepareSection(*work, *prepareID, *prepareSection, *cursor, *limit)
	case "draft":
		if *expected < 1 {
			return errors.New("draft 需要 --revision")
		}
		data, err = w.draftWorkflow(*work, *input, *expected)
	case "review":
		if *expected < 1 {
			return errors.New("review 需要 --revision")
		}
		data, err = w.reviewWorkflow(*work, *input, *expected)
	case "finalize":
		if *expected < 1 {
			return errors.New("finalize 需要 --revision")
		}
		data, err = w.finalizeWorkflow(*work, *input, *expected)
	case "chapter-history":
		if *work == "" || *id == "" {
			return errors.New("chapter-history 需要 --work 和 --id")
		}
		data, err = w.chapterHistory(*work, *id, *cursor, *limit)
	case "chapter-rollback":
		if *work == "" || *input == "" || *expected < 1 {
			return errors.New("chapter-rollback 需要 --work、--input 和 --revision")
		}
		data, err = w.rollbackChapterFromFile(*work, *input, *expected)
	case "append":
		return errors.New("安全定稿禁止直接 append；请使用 prepare → draft → review → finalize")
	case "mutate":
		if *expected < 1 {
			return errors.New("写入需要 --revision")
		}
		if err := rejectFinalStatusCLI(*input); err != nil {
			return err
		}
		data, err = w.mutateFromFile(*work, *input, *expected)
	case "read":
		data, err = w.readChapter(*work, *id, *cursor, *limit)
	case "query":
		data, err = w.queryFiltered(*work, *kind, *keyword, *character, *cursor, *limit,
			*chapterNumber, *fromChapter, *toChapter, *statusFilter, *fromDate, *toDate)
	case "metrics":
		data, err = w.metrics(*work)
	case "backup":
		data, err = w.backup(*work)
	case "recover":
		data, err = w.recoverPending(*work, *operation, *confirm)
	case "unlock":
		data, err = w.unlockStale(*work, *confirm)
	case "restore":
		data, err = w.restore(*work, *expected)
	case "restore-copy":
		data, err = w.restoreCopy(*work, *expected)
	case "adopt-recovery":
		if *work == "" || *operation == "" || *input == "" || *expected < 1 {
			return errors.New("adopt-recovery 需要 --work、--input、--operation 和 --revision")
		}
		data, err = w.adoptRecovery(*work, *input, *operation, *expected, *confirm)
	case "export":
		data, err = w.exportWork(*work)
	case "export-legacy":
		data, err = w.exportLegacy(*work)
	case "export-reading":
		data, err = w.exportReading(*work, *readingFormat)
	case "import":
		data, err = w.importWork(*input)
	case "import-legacy":
		data, err = w.importLegacy(*input)
	case "import-old-v4":
		data, err = w.importOldV4()
	default:
		return fmt.Errorf("未知命令：%s", args[0])
	}
	if err != nil {
		var partial savedBackupError
		if errors.As(err, &partial) {
			partial.Data = data
			return partial
		}
		return err
	}
	emit("正常", "操作完成", data)
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		var partial savedBackupError
		if errors.As(err, &partial) {
			emit("需要修复", partial.Error(), partial.Data)
			os.Exit(3)
		}
		emit("严重异常", err.Error(), nil)
		os.Exit(2)
	}
}

func rejectFinalStatusCLI(path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var probe struct {
		Kind   string         `json:"kind"`
		Record map[string]any `json:"record"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	if probe.Kind == "chapter-status" {
		if status, _ := probe.Record["status"].(string); status == "final" {
			return errors.New("章节定稿禁止通过 mutate 直接完成；请使用 prepare → draft → review → finalize")
		}
	}
	return nil
}

func cleanAbs(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", errors.New("路径必须是绝对路径")
	}
	clean := filepath.Clean(path)
	if err := rejectSymlinks(clean); err != nil {
		return "", err
	}
	return clean, nil
}
