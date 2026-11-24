package main

import (
	"strings"
	"testing"
)

type deathWarningFixture struct {
	w  *workspace
	id string
}

func newDeathWarningFixture(t *testing.T) deathWarningFixture {
	t.Helper()
	w := testWorkspace(t)
	id := testInit(t, w)
	chapters := []string{
		"虚构旧事开始。",
		"阿澜倒下。迟舟在死前知道秘密。青禾第一次倒下。",
		"阿澜伤势加重。阿澜随后苏醒。青禾醒来。迟舟已经死亡。",
		"青禾在死前知道旧事。",
		"阿澜与守门人重新结盟。迟舟与守门人建立关系。青禾再次死亡。",
		"阿澜确认新事实。迟舟的旧伤在倒叙中出现。青禾新增伤病。守门人受伤。",
	}
	revision := 1
	for index, content := range chapters {
		op := strings.Repeat(string(rune('1'+index)), 32)
		path := testStage(t, w, id, op, "ch_"+string(rune('1'+index)), content)
		if _, err := w.appendFromFile(id, path, revision); err != nil {
			t.Fatal(err)
		}
		revision++
	}
	characters := []map[string]any{
		{"id": "char_lan", "name": "阿澜"},
		{"id": "char_chi", "name": "迟舟"},
		{"id": "char_qing", "name": "青禾"},
		{"id": "char_guard", "name": "守门人"},
	}
	characterOps := []string{"7", "8", "9", "a"}
	for index, character := range characters {
		op := strings.Repeat(characterOps[index], 32)
		path := testRecordStage(t, w, id, op, "character-add", character)
		if _, err := w.mutateFromFile(id, path, revision); err != nil {
			t.Fatal(err)
		}
		revision++
	}
	updates := []struct {
		op     string
		record map[string]any
	}{
		{strings.Repeat("b", 32), map[string]any{
			"id": "char_lan",
			"death": []any{
				map[string]any{"status": "dead", "sourceChapterId": "ch_2", "evidence": "阿澜倒下"},
				map[string]any{"status": "alive", "sourceChapterId": "ch_3", "evidence": "阿澜随后苏醒"},
			},
			"injuries":      []any{map[string]any{"status": "worse", "chapterId": "ch_3", "evidence": "阿澜伤势加重"}},
			"relationships": []any{map[string]any{"targetId": "char_guard", "kind": "ally", "sinceChapterId": "ch_5", "evidence": "重新结盟"}},
			"cognition":     []any{map[string]any{"factId": "fact_new", "beliefType": "fact", "learnedChapterId": "ch_6", "evidence": "确认新事实"}},
		}},
		{strings.Repeat("c", 32), map[string]any{
			"id":            "char_chi",
			"death":         map[string]any{"status": "dead", "sourceChapterId": "ch_3", "evidence": "迟舟已经死亡"},
			"injuries":      []any{map[string]any{"status": "injured", "chapterId": "ch_6", "timeStatus": "flashback", "evidence": "旧伤在倒叙中出现"}},
			"relationships": []any{map[string]any{"targetId": "char_guard", "kind": "ally", "sinceChapterId": "ch_5", "evidence": "迟舟与守门人建立关系"}},
			"cognition":     []any{map[string]any{"factId": "fact_secret", "beliefType": "secret", "learnedChapterId": "ch_2", "evidence": "死前知道秘密"}},
		}},
		{strings.Repeat("d", 32), map[string]any{
			"id": "char_qing",
			"death": []any{
				map[string]any{"status": "dead", "sourceChapterId": "ch_2", "evidence": "青禾第一次倒下"},
				map[string]any{"status": "alive", "sourceChapterId": "ch_3", "evidence": "青禾醒来"},
				map[string]any{"status": "dead", "sourceChapterId": "ch_5", "evidence": "青禾再次死亡"},
			},
			"knowledge": []any{map[string]any{"factId": "fact_old", "beliefType": "fact", "learnedChapterId": "ch_4", "evidence": "死前知道旧事"}},
			"injuries":  []any{map[string]any{"status": "injured", "chapterId": "ch_6", "evidence": "青禾新增伤病"}},
		}},
		{strings.Repeat("e", 32), map[string]any{
			"id":       "char_guard",
			"injuries": []any{map[string]any{"status": "injured", "chapterId": "ch_6", "evidence": "守门人受伤"}},
		}},
	}
	for _, update := range updates {
		path := testRecordStage(t, w, id, update.op, "character-update", update.record)
		if _, err := w.mutateFromFile(id, path, revision); err != nil {
			t.Fatal(err)
		}
		revision++
	}
	return deathWarningFixture{w: w, id: id}
}

func TestDoctorDeathWarningsUseCurrentStateAndEvidenceChapter(t *testing.T) {
	fixture := newDeathWarningFixture(t)
	result, err := fixture.w.doctor(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	warnings := result.(map[string]any)["warnings"].([]string)
	if len(warnings) != 2 {
		t.Fatalf("死亡后状态告警数量错误：%v", warnings)
	}
	joined := strings.Join(warnings, "|")
	for _, expected := range []string{
		"人物 char_chi 已于第 3 章死亡，但 relationships 在第 5 章仍有记录",
		"人物 char_qing 已于第 5 章死亡，但 injuries 在第 6 章仍有记录",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("缺少预期告警 %q：%v", expected, warnings)
		}
	}
	for _, unexpected := range []string{"char_lan", "char_guard", "迟舟的旧伤", "cognition", "knowledge"} {
		if strings.Contains(joined, unexpected) {
			t.Fatalf("产生不应有的死亡后误报 %q：%v", unexpected, warnings)
		}
	}

	states, err := fixture.w.query(fixture.id, "states", "", "", 0, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	items := states.(map[string]any)["items"].([]any)
	lan := findState(t, items, "character_state", "char_lan")
	chi := findState(t, items, "character_state", "char_chi")
	qing := findState(t, items, "character_state", "char_qing")
	if lan["death"].(map[string]any)["status"] != "alive" ||
		chi["death"].(map[string]any)["status"] != "dead" || intValue(chi["death"].(map[string]any)["chapter"]) != 3 ||
		qing["death"].(map[string]any)["status"] != "dead" || intValue(qing["death"].(map[string]any)["chapter"]) != 5 {
		t.Fatalf("doctor 与 states 当前死亡状态不一致：lan=%v chi=%v qing=%v", lan, chi, qing)
	}
}

func resealDeathCharacters(t *testing.T, fixture deathWarningFixture, mutate func([]map[string]any)) {
	t.Helper()
	root, h, m, err := fixture.w.load(fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	records, err := currentRecords(root, m.CharactersPath)
	if err != nil {
		t.Fatal(err)
	}
	mutate(records)
	if err := atomicJSON(root, m.CharactersPath, records); err != nil {
		t.Fatal(err)
	}
	m.ContentHashes[m.CharactersPath], _ = fileSHA(root, m.CharactersPath)
	if err := atomicJSON(root, h.Manifest, m); err != nil {
		t.Fatal(err)
	}
	h.ManifestSHA256, _ = fileSHA(root, h.Manifest)
	if err := atomicJSON(root, "HEAD.json", h); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorDeathWarningDoesNotHideIntegrityErrors(t *testing.T) {
	t.Run("forged-evidence", func(t *testing.T) {
		fixture := newDeathWarningFixture(t)
		resealDeathCharacters(t, fixture, func(records []map[string]any) {
			for _, record := range records {
				if record["id"] == "char_chi" {
					record["relationships"].([]any)[0].(map[string]any)["evidence"] = "不存在的关系证据"
				}
			}
		})
		if _, err := fixture.w.doctor(fixture.id); err == nil || !strings.Contains(err.Error(), "证据") {
			t.Fatalf("伪造证据被降级为普通警告：%v", err)
		}
	})

	t.Run("invalid-compatible-death-status", func(t *testing.T) {
		fixture := newDeathWarningFixture(t)
		resealDeathCharacters(t, fixture, func(records []map[string]any) {
			for _, record := range records {
				if record["id"] == "char_qing" {
					record["death"].([]any)[2].(map[string]any)["status"] = "maybe"
				}
			}
		})
		if _, err := fixture.w.doctor(fixture.id); err == nil || !strings.Contains(err.Error(), "死亡状态") {
			t.Fatalf("非法旧格式死亡状态被降级为普通警告：%v", err)
		}
	})
}
