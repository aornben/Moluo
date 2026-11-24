"""墨络 v4 文件协议的电脑离线参考实现；不在 Android 上运行。

只接受显式传入的隔离目录。手机 Skill 依照 V4_STORAGE.md 使用工作区文件工具；
此模块用于迁移副本、完整性检查、导出和故障注入测试，不提供手机端事务保证。
"""
from __future__ import annotations

import copy
import hashlib
import json
import re
from datetime import date, timedelta
from pathlib import Path

FORMAT = "moluo-mobile-v4"
ID_RE = re.compile(r"^[A-Za-z0-9_-]{1,128}$")
DATE_RE = re.compile(r"^\d{4}-\d{2}-\d{2}$")
QUOTE_RE = re.compile(r"[“\"]([^”\"]{1,500})[”\"]")
PAGE_SIZE = 50


class StoreError(ValueError):
    pass


class Conflict(StoreError):
    pass


def _number(revision: int) -> str:
    return f"r{revision:06d}"


class MobileV4Store:
    def __init__(self, root: Path):
        self.root = Path(root)
        self._resolved_root = self.root.resolve(strict=False)

    def _path(self, relative: str) -> Path:
        if not isinstance(relative, str):
            raise StoreError("路径不是文本")
        path = Path(relative)
        if path.is_absolute() or ".." in path.parts or not path.parts:
            raise StoreError("无效的相对路径")
        candidate = (self.root / path).resolve(strict=False)
        if not candidate.is_relative_to(self._resolved_root):
            raise StoreError("路径越过 v4 数据目录")
        return candidate

    def _read(self, relative: str):
        path = self._path(relative)
        try:
            return json.loads(path.read_text(encoding="utf-8"))
        except (OSError, ValueError) as exc:
            raise StoreError(f"文件不存在或 JSON 无效：{relative}") from exc

    def _new(self, relative: str, data) -> None:
        """模拟 workspace_write_file(overwrite:false)：文件已存在就失败，不比较内容。"""
        path = self._path(relative)
        text = json.dumps(data, ensure_ascii=False, separators=(",", ":"))
        path.parent.mkdir(parents=True, exist_ok=True)
        if path.exists():
            raise Conflict(f"不可变文件已存在：{relative}")
        with path.open("x", encoding="utf-8", newline="\n") as target:
            target.write(text)
        if path.read_text(encoding="utf-8") != text:
            raise StoreError(f"写后回读不一致：{relative}")

    def _ensure(self, relative: str, data) -> None:
        """手机协议要求的先读后写：同内容跳过，不同内容停止。"""
        path = self._path(relative)
        if not path.exists():
            self._new(relative, data)
            return
        try:
            existing = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, ValueError) as exc:
            raise Conflict(f"已存在文件不是可比较的 JSON：{relative}") from exc
        if existing != data:
            raise Conflict(f"已存在文件内容不同：{relative}")

    @staticmethod
    def _hash_data(data) -> str:
        text = json.dumps(data, ensure_ascii=False, separators=(",", ":"))
        return hashlib.sha256(text.encode("utf-8")).hexdigest()

    def _sha256(self, relative: str) -> str:
        try:
            return hashlib.sha256(self._path(relative).read_bytes()).hexdigest()
        except OSError as exc:
            raise StoreError(f"哈希目标文件缺失或不可读：{relative}") from exc

    def _content_hashes(self, manifest: dict, overrides: dict | None = None) -> dict:
        """人物/待办/记忆/目录页的内容哈希；章节已有 sha256，不重复计算。"""
        overrides = overrides or {}
        paths = [manifest["charactersPath"], manifest["tasksPath"], manifest["memoryPath"]]
        paths.extend(page["path"] for page in manifest.get("pages", []))
        for key in ("memoryArchives", "taskArchives"):
            paths.extend(item["path"] for item in manifest.get(key, []))
        hashes = {}
        for relative in paths:
            if relative in overrides:
                hashes[relative] = self._hash_data(overrides[relative])
            else:
                hashes[relative] = self._sha256(relative)
        return hashes

    def _refresh_hashes(self, manifest: dict, overrides: dict | None = None) -> dict:
        manifest["contentHashes"] = self._content_hashes(manifest, overrides)
        return manifest

    def _replace_head(self, head: dict) -> None:
        path = self._path("HEAD.json")
        path.write_text(json.dumps(head, ensure_ascii=False, separators=(",", ":")), encoding="utf-8")
        if self._read("HEAD.json") != head:
            raise StoreError("HEAD 回读不一致")

    def _backup_head(self, head: dict) -> None:
        self._ensure(f"recovery/head-{_number(head['revision'])}.json", head)
        previous = self._path("HEAD.previous.json")
        previous.write_text(json.dumps(head, ensure_ascii=False, separators=(",", ":")), encoding="utf-8")
        if self._read("HEAD.previous.json") != head:
            raise StoreError("前一版 HEAD 回读不一致")

    def _head_manifest(self) -> tuple[dict, dict]:
        head = self._read("HEAD.json")
        if not isinstance(head, dict):
            raise StoreError("HEAD 结构无效")
        if head.get("format") != FORMAT or head.get("schemaVersion") != 4:
            raise StoreError("HEAD 格式不支持")
        if not isinstance(head.get("manifest"), str):
            raise StoreError("HEAD 缺少 manifest 路径")
        manifest = self._read(head["manifest"])
        if not isinstance(manifest, dict):
            raise StoreError("manifest 结构无效")
        if manifest.get("format") != FORMAT or manifest.get("schemaVersion") != 4:
            raise StoreError("manifest 格式不支持")
        if manifest.get("revision") != head.get("revision"):
            raise StoreError("HEAD 与 manifest 版本不一致")
        return head, manifest

    def migrate_legacy(self, legacy: dict, *, fail_after_chapters: int | None = None) -> dict:
        """只在独立副本上使用；不写原 v1/v2/v3 文件。"""
        if self._path("HEAD.json").exists():
            raise Conflict("v4 HEAD 已存在，禁止重复迁移覆盖")
        if (legacy.get("app"), legacy.get("format"), legacy.get("schemaVersion")) != (
            "moluo-novel-manager", "moluo-novel-backup", 1
        ) or legacy.get("data", {}).get("schemaVersion") != 1:
            raise StoreError("旧版信封不支持")
        data = legacy["data"]
        for key in ("meta", "characters", "chapters", "plotTasks"):
            if key not in data:
                raise StoreError(f"旧版缺少 {key}")
        if not isinstance(data["chapters"], list):
            raise StoreError("章节列表无效")
        if not isinstance(legacy.get("revision"), int) or legacy["revision"] < 0:
            raise StoreError("旧版 revision 无效")
        entries = []
        used = set()
        for number, chapter in enumerate(data["chapters"], 1):
            ident = chapter.get("id")
            if not isinstance(ident, str) or not ID_RE.fullmatch(ident) or ident in used:
                raise StoreError("章节 ID 非法或重复")
            used.add(ident)
            path = f"chapters/{ident}-r000001.json"
            self._ensure(path, chapter)
            entries.append({"id": ident, "number": number, "title": chapter.get("title", ""),
                            "path": path, "sha256": self._sha256(path)})
            if fail_after_chapters == number:
                raise StoreError("注入：迁移章节后中断")
        self._ensure("characters/r000001.json", data["characters"])
        self._ensure("tasks/r000001.json", data["plotTasks"])
        self._ensure("memory/r000001.json", {"chapterCards": [], "activeFacts": [], "knowledge": [],
                                              "openHooks": [], "openQuestions": [], "constraints": [],
                                              "timeline": [], "archives": [], "summaries": [],
                                              "sourceRevision": 1,
                                              "coverage": "partial" if entries else "complete"})
        pages = []
        for offset in range(0, len(entries), PAGE_SIZE):
            batch = entries[offset:offset + PAGE_SIZE]
            path = f"pages/p{offset // PAGE_SIZE + 1:04d}-r000001.json"
            self._ensure(path, batch)
            pages.append({"start": offset + 1, "end": offset + len(batch), "path": path})
        manifest = {"format": FORMAT, "schemaVersion": 4, "revision": 1, "previousManifest": None,
                    "legacyRevision": legacy.get("revision", 0), "meta": data["meta"],
                    "legacySavedAt": legacy.get("savedAt"),
                    "legacyEnvelopeExtra": {k: v for k, v in legacy.items() if k not in
                                            {"app", "format", "schemaVersion", "revision", "savedAt", "data"}},
                    "legacyDataExtra": {k: v for k, v in data.items() if k not in
                                        {"schemaVersion", "meta", "characters", "chapters", "plotTasks"}},
                    "charactersPath": "characters/r000001.json", "tasksPath": "tasks/r000001.json",
                    "memoryPath": "memory/r000001.json", "memoryRevision": 1,
                    "chapterCount": len(entries), "pages": pages,
                    "memoryArchives": [], "taskArchives": []}
        self._refresh_hashes(manifest)
        self._ensure("manifests/r000001.json", manifest)
        self.validate_manifest(manifest, full=True, allow_unverified_done=True)
        head = {"format": FORMAT, "schemaVersion": 4, "revision": 1,
                "manifest": "manifests/r000001.json"}
        self._ensure("HEAD.json", head)
        self._head_manifest()
        return head

    def create_empty(self, *, meta: dict | None = None) -> dict:
        """初始化空白 v4 作品，不要求先造旧版文件。"""
        if self._path("HEAD.json").exists():
            raise Conflict("v4 HEAD 已存在，禁止覆盖")
        if self.root.exists() and any(self.root.iterdir()):
            raise Conflict("目标目录不是空目录，禁止初始化覆盖")
        if meta is None:
            meta = {"title": "未命名作品", "author": "", "synopsis": ""}
        if not isinstance(meta, dict):
            raise StoreError("作品 meta 结构无效")
        empty_memory = {"chapterCards": [], "activeFacts": [], "knowledge": [],
                        "openHooks": [], "openQuestions": [], "constraints": [],
                        "timeline": [], "archives": [], "summaries": [],
                        "sourceRevision": 1, "coverage": "complete"}
        self._ensure("characters/r000001.json", [])
        self._ensure("tasks/r000001.json", [])
        self._ensure("memory/r000001.json", empty_memory)
        manifest = {"format": FORMAT, "schemaVersion": 4, "revision": 1, "previousManifest": None,
                    "legacyRevision": 0, "legacySavedAt": None, "legacyEnvelopeExtra": {},
                    "legacyDataExtra": {}, "meta": copy.deepcopy(meta),
                    "charactersPath": "characters/r000001.json", "tasksPath": "tasks/r000001.json",
                    "memoryPath": "memory/r000001.json", "memoryRevision": 1,
                    "chapterCount": 0, "pages": [], "memoryArchives": [], "taskArchives": []}
        self._refresh_hashes(manifest)
        self._ensure("manifests/r000001.json", manifest)
        self.validate_manifest(manifest, full=True)
        head = {"format": FORMAT, "schemaVersion": 4, "revision": 1,
                "manifest": "manifests/r000001.json"}
        self._ensure("HEAD.json", head)
        self._head_manifest()
        return head

    def validate_manifest(self, manifest: dict, *, full: bool, check_memory: bool = True,
                          allow_unverified_done: bool = False,
                          check_content_hashes: bool = True) -> None:
        for key in ("charactersPath", "tasksPath", "memoryPath", "memoryRevision", "pages", "chapterCount"):
            if key not in manifest:
                raise StoreError(f"manifest 缺少 {key}")
        chars = self._read(manifest["charactersPath"])
        tasks = self._read(manifest["tasksPath"])
        memory = self._read(manifest["memoryPath"]) if check_memory else {"coverage": "partial"}
        if not isinstance(chars, list) or not isinstance(tasks, list):
            raise StoreError("人物或任务列表无效")
        if not isinstance(memory, dict) or memory.get("coverage") not in ("complete", "partial"):
            raise StoreError("长期记忆结构无效")
        if check_memory and memory.get("sourceRevision") != manifest["memoryRevision"]:
            raise StoreError("记忆索引版本过期或不一致")
        if any(not isinstance(c, dict) or not isinstance(c.get("id"), str) or
               not ID_RE.fullmatch(c["id"]) for c in chars):
            raise StoreError("人物 ID 无效")
        char_ids = {c["id"] for c in chars}
        if len(char_ids) != len(chars):
            raise StoreError("人物 ID 重复")
        chapters = self._entries(manifest)
        chapter_ids = set()
        chapter_contents = {}
        chapter_order = {}
        paths = set()
        for number, entry in enumerate(chapters, 1):
            ident = entry.get("id")
            if (entry.get("number") != number or not isinstance(ident, str) or
                    not ID_RE.fullmatch(ident) or ident in chapter_ids or entry.get("path") in paths):
                raise StoreError("章节编号、ID 或路径重复/无效")
            chapter_ids.add(ident)
            chapter_order[ident] = number
            paths.add(entry["path"])
            if full:
                chapter = self._read(entry["path"])
                if chapter.get("id") != ident or chapter.get("title") != entry.get("title"):
                    raise StoreError("章节文件与目录不一致")
                if not isinstance(chapter.get("content"), str):
                    raise StoreError("章节正文不是文本")
                chapter_contents[ident] = chapter["content"]
                if entry.get("sha256") and self._sha256(entry["path"]) != entry["sha256"]:
                    raise StoreError(f"章节内容哈希不一致：{entry['path']}")
                if check_memory and entry.get("cardPath"):
                    card = self._read(entry["cardPath"])
                    self._verify_card(card, chapter.get("content"), ident)
        content_hashes = manifest.get("contentHashes")
        if check_content_hashes and content_hashes is not None:
            expected_hashes = self._content_hashes(manifest)
            if not isinstance(content_hashes, dict) or set(content_hashes) != set(expected_hashes):
                raise StoreError("内容哈希清单不完整或与目录不一致")
            for relative, expected in expected_hashes.items():
                if content_hashes.get(relative) != expected:
                    raise StoreError(f"文件内容哈希不一致：{relative}")
        if check_memory:
            cards = memory.get("chapterCards", [])
            if any(card.get("chapterId") not in chapter_ids for card in cards):
                raise StoreError("记忆索引引用缺失章节")
            if len({card.get("chapterId") for card in cards}) != len(cards):
                raise StoreError("记忆索引重复章节证据卡")
            expected_cards = {entry["id"]: entry.get("cardPath") for entry in chapters if entry.get("cardPath")}
            if any(expected_cards.get(card.get("chapterId")) != card.get("path") for card in cards):
                raise StoreError("记忆索引证据卡路径与章节目录不一致")
            archived_cards = sum(int(descriptor.get("cardCount", descriptor.get("counts", {}).get("cards", 0)))
                                 for descriptor in manifest.get("memoryArchives", []))
            if memory["coverage"] == "complete" and \
                    len({c.get("chapterId") for c in cards}) + archived_cards != len(chapters):
                raise StoreError("记忆覆盖率声明不实")
        archived_tasks = []
        for descriptor in manifest.get("memoryArchives", []):
            archive = self._read(descriptor.get("path"))
            if not isinstance(archive, dict):
                raise StoreError("记忆归档结构无效")
            archived_ids = set(archive.get("chapterIds", []))
            if not archived_ids.issubset(chapter_ids):
                raise StoreError("记忆归档引用缺失章节")
            for item in archive.get("facts", []):
                if item.get("sourceChapterId") not in archived_ids or not item.get("evidence"):
                    raise StoreError("归档事实缺少原章证据")
                if full and item["evidence"] not in chapter_contents[item["sourceChapterId"]]:
                    raise StoreError("正文证据不一致：归档事实")
            for item in archive.get("knowledge", []):
                if item.get("learnedChapterId") not in archived_ids or item.get("characterId") not in char_ids \
                        or not item.get("evidence"):
                    raise StoreError("归档人物认知缺少原章证据")
                if full and item["evidence"] not in chapter_contents[item["learnedChapterId"]]:
                    raise StoreError("正文证据不一致：归档人物认知")
            for item in archive.get("constraints", []) + archive.get("timeline", []):
                if item.get("sourceChapterId") not in archived_ids or not item.get("evidence"):
                    raise StoreError("归档约束缺少原章证据")
                if full and item["evidence"] not in chapter_contents[item["sourceChapterId"]]:
                    raise StoreError("正文证据不一致：归档约束")
            for item in archive.get("hooks", []) + archive.get("questions", []):
                source = item.get("openedChapterId") or item.get("sourceChapterId")
                if source not in archived_ids or not item.get("evidence"):
                    raise StoreError("归档伏笔或问题缺少原章证据")
                if full and item["evidence"] not in chapter_contents[source]:
                    raise StoreError("正文证据不一致：归档伏笔或问题")
            for card in archive.get("chapterCards", []):
                if card.get("chapterId") not in archived_ids or not card.get("path"):
                    raise StoreError("归档证据卡引用无效")
                if not self._path(card["path"]).exists():
                    raise StoreError("归档证据卡文件缺失")
        if check_memory:
            self._validate_summary_index(memory, manifest, chapter_ids)
        for descriptor in manifest.get("taskArchives", []):
            archived = self._read(descriptor.get("path"))
            if not isinstance(archived, list):
                raise StoreError("任务归档结构无效")
            archived_tasks.extend(archived)
        task_ids = set()
        task_keys = set()
        for task in tasks:
            if not isinstance(task, dict) or not isinstance(task.get("id"), str) or not ID_RE.fullmatch(task["id"]):
                raise StoreError("剧情任务 ID 无效")
            if task["id"] in task_ids:
                raise StoreError("剧情任务 ID 重复")
            task_ids.add(task["id"])
            title_key = (" ".join(str(task.get("title", "")).split()).casefold(), task.get("plannedChapter"))
            if title_key in task_keys:
                raise StoreError("重复剧情任务标题和计划章节")
            task_keys.add(title_key)
            if task.get("status") not in ("todo", "in_progress", "done") or task.get("priority") not in ("normal", "high"):
                raise StoreError("剧情任务状态或优先级无效")
            if not isinstance(task.get("plannedChapter"), int) or not 1 <= task["plannedChapter"] <= 9999:
                raise StoreError("剧情任务计划章节无效")
            if any(ident not in char_ids for ident in task.get("characterIds", [])):
                raise StoreError("剧情任务引用缺失人物")
            if task.get("status") == "done":
                if task.get("completedChapterId") not in chapter_ids or not task.get("completionEvidence"):
                    raise StoreError("已完成剧情缺少章节或正文依据")
                if full and not allow_unverified_done:
                    self._check_done_task(task, chapter_contents)
        for task in archived_tasks:
            if not isinstance(task, dict) or not isinstance(task.get("id"), str) or not ID_RE.fullmatch(task["id"]):
                raise StoreError("归档剧情任务 ID 无效")
            if task["id"] in task_ids:
                raise StoreError("归档剧情任务 ID 重复")
            task_ids.add(task["id"])
            if task.get("status") != "done" or task.get("completedChapterId") not in chapter_ids \
                    or not task.get("completionEvidence"):
                raise StoreError("归档任务必须是带章节依据的已完成任务")
            if any(ident not in char_ids for ident in task.get("characterIds", [])):
                raise StoreError("归档剧情任务引用缺失人物")
            if full and not allow_unverified_done:
                self._check_done_task(task, chapter_contents)
        for fact in memory.get("activeFacts", []):
            if fact.get("certainty") not in ("certain", "possible", "unknown", "conflict"):
                raise StoreError("事实确定性无效")
            if fact.get("sourceChapterId") not in chapter_ids or not fact.get("evidence"):
                raise StoreError("事实缺少有效章节证据")
            if full and fact["evidence"] not in chapter_contents[fact["sourceChapterId"]]:
                raise StoreError("正文证据不一致：长期事实")
        for item in memory.get("knowledge", []):
            if item.get("characterId") not in char_ids or item.get("learnedChapterId") not in chapter_ids:
                raise StoreError("人物认知引用无效")
            if not item.get("evidence"):
                raise StoreError("人物认知缺少证据")
            if full and item["evidence"] not in chapter_contents[item["learnedChapterId"]]:
                raise StoreError("正文证据不一致：人物认知")
        for hook in memory.get("openHooks", []):
            if hook.get("openedChapterId") not in chapter_ids or hook.get("status") != "open":
                raise StoreError("伏笔引用或状态无效")
            if full and hook.get("evidence") and hook["evidence"] not in chapter_contents[hook["openedChapterId"]]:
                raise StoreError("正文证据不一致：伏笔")
        if check_memory:
            self._validate_constraints(memory, chapter_ids, chapter_order, chapter_contents, full=full)

    @staticmethod
    def _task_quotes(evidence) -> list[str]:
        if not isinstance(evidence, str):
            return []
        return [quote.strip() for quote in QUOTE_RE.findall(evidence) if quote.strip()]

    @classmethod
    def _check_done_task(cls, task: dict, chapter_contents: dict[str, str]) -> None:
        chapter_id = task.get("completedChapterId")
        evidence = task.get("completionEvidence")
        quotes = cls._task_quotes(evidence)
        if not quotes:
            raise StoreError("已完成剧情缺少可核验的正文原文证据")
        if chapter_id not in chapter_contents:
            raise StoreError("已完成剧情引用的章节不存在")
        if any(quote not in chapter_contents[chapter_id] for quote in quotes):
            raise StoreError(f"已完成剧情正文原文证据不一致：{chapter_id}")

    @staticmethod
    def _parse_date(value: str, label: str) -> date:
        if not isinstance(value, str) or not DATE_RE.fullmatch(value):
            raise StoreError(f"{label}日期格式无效")
        try:
            return date.fromisoformat(value)
        except ValueError as exc:
            raise StoreError(f"{label}日期无效") from exc

    def _validate_constraints(self, memory: dict, chapter_ids: set[str], chapter_order: dict[str, int],
                              chapter_contents: dict[str, str], *, full: bool) -> None:
        constraints = memory.get("constraints", [])
        timeline = memory.get("timeline", [])
        if not isinstance(constraints, list) or not isinstance(timeline, list):
            raise StoreError("时间线或约束结构无效")
        known = {}
        for item in constraints:
            if not isinstance(item, dict) or not isinstance(item.get("id"), str):
                raise StoreError("约束缺少 ID")
            ident = item["id"]
            if ident in known:
                raise StoreError("约束 ID 重复")
            known[ident] = item
            if item.get("kind") not in ("deadline", "amount"):
                raise StoreError("约束类型无效")
            if item.get("sourceChapterId") not in chapter_ids or not item.get("evidence"):
                raise StoreError("约束缺少有效章节证据")
            if full and item["evidence"] not in chapter_contents[item["sourceChapterId"]]:
                raise StoreError("正文证据不一致：时间线或金额约束")
            if item.get("supersedes") and item["supersedes"] not in known:
                raise StoreError("约束 supersedes 引用不存在")
            if item["kind"] == "deadline":
                if item.get("dueDate") is not None:
                    due = self._parse_date(item["dueDate"], "截止")
                    if item.get("anchorDate") is not None or item.get("relativeDays") is not None:
                        if not isinstance(item.get("relativeDays"), int) or item["relativeDays"] <= 0:
                            raise StoreError("相对期限天数无效")
                        anchor = self._parse_date(item.get("anchorDate"), "起始")
                        if anchor + timedelta(days=item["relativeDays"]) != due:
                            raise StoreError("相对期限换算与绝对截止日期矛盾")
                else:
                    if not isinstance(item.get("relativeDays"), int) or item["relativeDays"] <= 0:
                        raise StoreError("截止日期缺少有效相对期限")
                    self._parse_date(item.get("anchorDate"), "起始")
            else:
                if not isinstance(item.get("value"), (int, float)) or isinstance(item.get("value"), bool) or item["value"] <= 0:
                    raise StoreError("金额必须为正数")
                if not isinstance(item.get("currency"), str) or not item["currency"].strip():
                    raise StoreError("金额缺少币种")
        for item in timeline:
            if not isinstance(item, dict) or not isinstance(item.get("id"), str):
                raise StoreError("时间线事件缺少 ID")
            if item.get("sourceChapterId") not in chapter_ids or not item.get("evidence"):
                raise StoreError("时间线事件缺少有效章节证据")
            if full and item["evidence"] not in chapter_contents[item["sourceChapterId"]]:
                raise StoreError("正文证据不一致：时间线事件")
            start = self._parse_date(item.get("startDate") or item.get("date"), "时间线")
            end_value = item.get("endDate")
            if end_value is not None and self._parse_date(end_value, "时间线结束") < start:
                raise StoreError("时间线事件结束日期早于开始日期")
        self._check_constraint_conflicts(constraints, chapter_order)

    def _check_constraint_conflicts(self, constraints: list[dict], chapter_order: dict[str, int]) -> None:
        for kind in ("deadline", "amount"):
            groups = {}
            for item in constraints:
                if item.get("kind") != kind:
                    continue
                subject = item.get("subject")
                if not isinstance(subject, str) or not subject.strip():
                    raise StoreError("截止日期或金额缺少主题")
                key = (subject.strip(), item.get("currency") if kind == "amount" else "")
                groups.setdefault(key, []).append(item)
            for key, items in groups.items():
                items = sorted(items, key=lambda item: chapter_order.get(item["sourceChapterId"], 10**9))
                active = {}
                for item in items:
                    if kind == "deadline":
                        value = item.get("dueDate")
                        if not value:
                            anchor = self._parse_date(item["anchorDate"], "起始")
                            value = (anchor + timedelta(days=item["relativeDays"])).isoformat()
                    else:
                        value = round(float(item["value"]), 6)
                    supersedes = item.get("supersedes")
                    if supersedes:
                        active.pop(supersedes, None)
                    for existing in active.values():
                        if existing != value:
                            label = "截止日期" if kind == "deadline" else "金额"
                            raise StoreError(f"{label}矛盾：{key[0]}")
                    active[item["id"]] = value

    def _validate_summary_index(self, memory: dict, manifest: dict, chapter_ids: set[str]) -> None:
        archives = memory.get("archives", [])
        manifest_archives = manifest.get("memoryArchives", [])
        summaries = memory.get("summaries", [])
        if not isinstance(archives, list) or not isinstance(manifest_archives, list) or not isinstance(summaries, list):
            raise StoreError("摘要或归档索引结构无效")
        archive_by_path = {item.get("path"): item for item in archives if isinstance(item, dict)}
        manifest_paths = {item.get("path") for item in manifest_archives if isinstance(item, dict)}
        if set(archive_by_path) != manifest_paths:
            raise StoreError("记忆快照归档索引与 manifest 不一致")
        summary_by_path = {}
        for summary in summaries:
            if not isinstance(summary, dict) or not isinstance(summary.get("archivePath"), str):
                raise StoreError("摘要索引缺少归档路径")
            path = summary["archivePath"]
            if path in summary_by_path:
                raise StoreError("摘要索引重复归档路径")
            summary_by_path[path] = summary
        if set(summary_by_path) != manifest_paths:
            raise StoreError("摘要索引与归档文件不完整")
        for descriptor in manifest_archives:
            path = descriptor.get("path")
            archive = self._read(path)
            chapter_ids_in_archive = set(archive.get("chapterIds", []))
            if not chapter_ids_in_archive.issubset(chapter_ids):
                raise StoreError("归档索引引用缺失章节")
            summary = summary_by_path[path]
            start, end = summary.get("start"), summary.get("end")
            if not isinstance(start, int) or not isinstance(end, int) or start < 1 or end < start:
                raise StoreError("摘要范围无效")
            if start != descriptor.get("start") or end != descriptor.get("end"):
                raise StoreError("摘要范围与归档描述不一致")
            memory_descriptor = archive_by_path[path]
            if memory_descriptor.get("start") != start or memory_descriptor.get("end") != end:
                raise StoreError("记忆归档描述与摘要范围不一致")
            source_ids = summary.get("sourceChapterIds", [])
            if not isinstance(source_ids, list) or set(source_ids) != chapter_ids_in_archive:
                raise StoreError("摘要来源章节与归档不一致")
            counts = summary.get("counts", {})
            expected_counts = {"facts": len(archive.get("facts", [])),
                               "knowledge": len(archive.get("knowledge", [])),
                               "constraints": len(archive.get("constraints", [])),
                               "timeline": len(archive.get("timeline", [])),
                               "cards": len(archive.get("chapterCards", [])),
                               "hooks": len(archive.get("hooks", [])),
                               "questions": len(archive.get("questions", []))}
            if counts != expected_counts or memory_descriptor.get("counts") != expected_counts:
                raise StoreError("摘要计数与归档内容不一致")
            if not isinstance(summary.get("summary"), str) or not summary["summary"]:
                raise StoreError("摘要文本缺失")

    def _entries(self, manifest: dict) -> list[dict]:
        pages = manifest.get("pages")
        count = manifest.get("chapterCount")
        if not isinstance(pages, list) or not isinstance(count, int) or count < 0:
            raise StoreError("章节分页目录无效")
        entries = []
        for index, page in enumerate(pages):
            if (not isinstance(page, dict) or page.get("start") != len(entries) + 1 or
                    not isinstance(page.get("end"), int) or page["end"] < page["start"] or
                    page["end"] - page["start"] >= PAGE_SIZE):
                raise StoreError("章节页范围无效")
            batch = self._read(page.get("path"))
            if not isinstance(batch, list) or len(batch) != page["end"] - page["start"] + 1:
                raise StoreError("章节页内容无效")
            if index < len(pages) - 1 and len(batch) != PAGE_SIZE:
                raise StoreError("中间章节页未满却出现下一页")
            entries.extend(batch)
        if len(entries) != count:
            raise StoreError("章节数量与分页不符")
        return entries

    def validate(self, *, full: bool = True, allow_unverified_done: bool = False) -> dict:
        head, manifest = self._head_manifest()
        self.validate_manifest(manifest, full=full, allow_unverified_done=allow_unverified_done)
        pending = self._path(f"transactions/{_number(head['revision'] + 1)}.json")
        return {"revision": head["revision"], "chapters": manifest["chapterCount"],
                "pendingNext": pending.exists()}

    def read_chapter(self, number: int) -> dict:
        _, manifest = self._head_manifest()
        if not 1 <= number <= manifest["chapterCount"]:
            raise StoreError("章节不存在")
        page = next((p for p in manifest["pages"] if p["start"] <= number <= p["end"]), None)
        if page is None:
            raise StoreError("章节分页缺失")
        batch = self._read(page["path"])
        if not isinstance(batch, list) or len(batch) != page["end"] - page["start"] + 1:
            raise StoreError("章节页内容无效")
        entry = batch[number - page["start"]]
        chapter = self._read(entry["path"])
        if chapter.get("id") != entry["id"]:
            raise StoreError("章节 ID 不一致")
        return chapter

    def context_for(self, target_chapter: int, *, character_ids: tuple[str, ...] = ()) -> dict:
        """只读取轻量资料和最近一章；保留事实确定性，不替模型写正文。"""
        _, manifest = self._head_manifest()
        if target_chapter != manifest["chapterCount"] + 1:
            raise StoreError("只能为下一章生成写前上下文")
        tasks = self._read(manifest["tasksPath"])
        memory = self._read(manifest["memoryPath"])
        chosen = set(character_ids)
        due = [t for t in tasks if t["status"] != "done" and t["plannedChapter"] <= target_chapter]
        soon = [t for t in tasks if t["status"] != "done" and target_chapter < t["plannedChapter"] <= target_chapter + 2]
        for task in [*due, *soon]:
            chosen.update(task.get("characterIds", []))
        retrieved = self.retrieve_memory(character_ids=tuple(chosen), max_archives=2)
        return {"chapter": target_chapter,
                "characters": self._read(manifest["charactersPath"]),
                "dueTasks": due, "soonTasks": soon,
                "facts": [f for f in memory.get("activeFacts", [])
                          if not f.get("characterIds") or chosen.intersection(f["characterIds"])],
                "knowledge": [k for k in memory.get("knowledge", []) if k.get("characterId") in chosen],
                "openHooks": [h for h in memory.get("openHooks", [])
                              if not h.get("characterIds") or chosen.intersection(h["characterIds"])],
                "openQuestions": [q for q in memory.get("openQuestions", [])
                                  if not q.get("characterIds") or chosen.intersection(q["characterIds"])],
                "constraints": [c for c in memory.get("constraints", [])
                                if not c.get("characterIds") or chosen.intersection(c["characterIds"])],
                "timeline": [t for t in memory.get("timeline", [])
                             if not t.get("characterIds") or chosen.intersection(t["characterIds"])],
                "summaries": retrieved["summaries"],
                "archiveHits": retrieved["archiveHits"],
                "archiveSearchComplete": retrieved["complete"],
                "nextArchiveCursor": retrieved["nextCursor"],
                "remainingArchiveCount": retrieved["remainingArchives"],
                "archivedFacts": retrieved["facts"],
                "archivedKnowledge": retrieved["knowledge"],
                "archivedConstraints": retrieved["constraints"],
                "archivedTimeline": retrieved["timeline"],
                "archivedHooks": retrieved["hooks"],
                "archivedQuestions": retrieved["questions"],
                "coverage": memory.get("coverage"),
                "previousChapter": self.read_chapter(target_chapter - 1) if target_chapter > 1 else None}

    @staticmethod
    def _verify_card(card: dict, content: str, chapter_id: str) -> None:
        if not isinstance(card, dict) or card.get("chapterId") != chapter_id or not isinstance(content, str):
            raise StoreError("证据卡与章节正文不一致")
        for field in ("facts", "knowledgeChanges", "hookChanges", "taskEvidence", "openQuestions",
                      "constraints", "timeline"):
            items = card.get(field, [])
            if not isinstance(items, list):
                raise StoreError(f"证据卡字段无效：{field}")
            for item in items:
                if not isinstance(item, dict) or not isinstance(item.get("evidence"), str) or not item["evidence"]:
                    raise StoreError(f"证据卡缺少正文引文：{field}")
                if field in ("constraints", "timeline") and not isinstance(item.get("id"), str):
                    raise StoreError(f"证据卡缺少 ID：{field}")
                if item["evidence"] not in content:
                    raise StoreError(f"正文证据不一致：{field}；章节 {chapter_id}")
                source_key = {"facts": "sourceChapterId", "knowledgeChanges": "learnedChapterId",
                              "constraints": "sourceChapterId", "timeline": "sourceChapterId"}.get(field)
                if source_key and item.get(source_key) != chapter_id:
                    raise StoreError(f"证据卡来源章节不一致：{field}；章节 {chapter_id}")
                if field == "hookChanges" and item.get("status") == "open" and item.get("openedChapterId") != chapter_id:
                    raise StoreError(f"证据卡伏笔来源章节不一致：{chapter_id}")

    @staticmethod
    def _apply_card(memory: dict, card: dict, content: str) -> dict:
        MobileV4Store._verify_card(card, content, card.get("chapterId") if isinstance(card, dict) else "")
        updated = copy.deepcopy(memory)
        for field, source, key in (("activeFacts", "facts", lambda x: x["id"]),
                                   ("knowledge", "knowledgeChanges", lambda x: (x["characterId"], x["factId"]))):
            current = {key(item): item for item in updated.get(field, [])}
            for item in card.get(source, []):
                current[key(item)] = item
            updated[field] = list(current.values())
        hooks = {item["id"]: item for item in updated.get("openHooks", [])}
        for item in card.get("hookChanges", []):
            if item.get("status") == "resolved":
                hooks.pop(item["id"], None)
            elif item.get("status") == "open":
                hooks[item["id"]] = item
            else:
                raise StoreError("伏笔变化状态无效")
        updated["openHooks"] = list(hooks.values())
        questions = {item["id"]: item for item in updated.get("openQuestions", [])}
        for item in card.get("openQuestions", []):
            if item.get("status") == "resolved":
                questions.pop(item["id"], None)
            else:
                questions[item["id"]] = item
        updated["openQuestions"] = list(questions.values())
        for field in ("constraints", "timeline"):
            current = {item["id"]: item for item in updated.get(field, [])}
            for item in card.get(field, []):
                current[item["id"]] = item
            updated[field] = list(current.values())
        return updated

    def append_chapter(self, chapter: dict, *, expected_revision: int, tasks: list | None = None,
                       memory: dict | None = None, card: dict | None = None,
                       fail_at: str | None = None, allow_task_delete: bool = False,
                       allow_task_reopen: bool = False) -> dict:
        head, old = self._head_manifest()
        revision = head["revision"]
        if revision != expected_revision:
            raise Conflict("版本已变化")
        ident = chapter.get("id")
        if not isinstance(ident, str) or not ID_RE.fullmatch(ident):
            raise StoreError("章节 ID 非法")
        old_entries = self._entries(old)
        if any(item["id"] == ident for item in old_entries):
            raise Conflict("章节 ID 重复")
        if tasks is not None:
            previous_list = self._read(old["tasksPath"])
            previous_tasks = {task["id"]: task for task in previous_list}
            new_tasks = {}
            for task in tasks:
                if not isinstance(task, dict) or not isinstance(task.get("id"), str):
                    raise StoreError("剧情任务结构无效")
                if task["id"] in new_tasks:
                    raise StoreError("剧情任务 ID 重复")
                new_tasks[task["id"]] = task
        next_revision = revision + 1
        tag = _number(next_revision)
        transaction_path = f"transactions/{tag}.json"
        if self._path(transaction_path).exists():
            raise Conflict("上次写入未完成，必须先恢复")
        chapter_path = f"chapters/{ident}-{tag}.json"
        card_path = f"memory/cards/{ident}-{tag}.json" if card is not None else None
        manifest_path = f"manifests/{tag}.json"
        page_number = len(old["pages"]) if old["chapterCount"] % PAGE_SIZE else len(old["pages"]) + 1
        page_path = f"pages/p{page_number:04d}-{tag}.json"
        task_path = f"tasks/{tag}.json" if tasks is not None else old["tasksPath"]
        memory_path = f"memory/{tag}.json"
        planned = [chapter_path, page_path, memory_path, manifest_path]
        if tasks is not None:
            planned.append(task_path)
        if card_path:
            planned.append(card_path)
        self._new(transaction_path, {"baseRevision": revision, "targetRevision": next_revision,
                                     "baseManifest": head["manifest"], "targetManifest": manifest_path,
                                     "planned": planned, "operation": "append_chapter"})
        if fail_at == "after_transaction":
            raise StoreError("注入：transaction 后中断")
        self._ensure(chapter_path, chapter)
        if fail_at == "after_chapter":
            raise StoreError("注入：章节后中断")
        if tasks is not None:
            missing = set(previous_tasks) - set(new_tasks)
            if missing and not allow_task_delete:
                raise StoreError("不允许通过任务快照删除剧情待办")
            for task in tasks:
                before = previous_tasks.get(task["id"])
                if before and before.get("status") == "done" and task.get("status") != "done" \
                        and not allow_task_reopen:
                    raise StoreError("不允许重开已完成剧情待办")
                if task.get("status") == "done" and (before is None or task != before):
                    if task.get("completedChapterId") == ident:
                        contents = {ident: chapter.get("content", "")}
                    else:
                        target = next((entry for entry in old_entries
                                       if entry["id"] == task.get("completedChapterId")), None)
                        if target is None:
                            raise StoreError("已完成剧情引用的章节不存在")
                        contents = {target["id"]: self._read(target["path"]).get("content", "")}
                    self._check_done_task(task, contents)
            self._ensure(task_path, tasks)
        if memory is None:
            memory = self._read(old["memoryPath"])
        memory = copy.deepcopy(memory)
        memory.setdefault("archives", [])
        memory.setdefault("summaries", [])
        if card_path:
            if card.get("chapterId") != ident:
                raise StoreError("新证据卡章节 ID 不符")
            memory = self._apply_card(memory, card, chapter.get("content", ""))
            self._ensure(card_path, card)
            memory["chapterCards"] = [item for item in memory.get("chapterCards", [])
                                      if item.get("chapterId") != ident]
            memory["chapterCards"].append({"chapterId": ident, "path": card_path})
        memory["coverage"] = ("complete" if len({c.get("chapterId") for c in memory.get("chapterCards", [])})
                              == old["chapterCount"] + 1 else "partial")
        memory["sourceRevision"] = next_revision
        self._ensure(memory_path, memory)
        updated = copy.deepcopy(old)
        updated.update({"revision": next_revision, "previousManifest": head["manifest"],
                        "tasksPath": task_path, "memoryPath": memory_path, "memoryRevision": next_revision})
        updated.setdefault("memoryArchives", [])
        updated.setdefault("taskArchives", [])
        entry = {"id": ident, "number": old["chapterCount"] + 1,
                 "title": chapter.get("title", ""), "path": chapter_path,
                 "sha256": self._sha256(chapter_path)}
        if card_path:
            entry["cardPath"] = card_path
        if old["chapterCount"] % PAGE_SIZE:
            last = old["pages"][-1]
            batch = self._read(last["path"])
            new_page = batch + [entry]
            updated["pages"][-1] = {"start": last["start"], "end": entry["number"], "path": page_path}
        else:
            new_page = [entry]
            updated["pages"].append({"start": entry["number"], "end": entry["number"], "path": page_path})
        self._ensure(page_path, new_page)
        updated["chapterCount"] = old["chapterCount"] + 1
        self._refresh_hashes(updated)
        self._ensure(manifest_path, updated)
        self.validate_manifest(updated, full=False, allow_unverified_done=True)
        if fail_at == "after_manifest":
            raise StoreError("注入：manifest 后中断")
        self._backup_head(head)
        if self._read("HEAD.json") != head:
            raise Conflict("提交前 HEAD 已变化")
        self._replace_head({"format": FORMAT, "schemaVersion": 4,
                            "revision": next_revision, "manifest": manifest_path})
        if fail_at == "after_head":
            raise StoreError("注入：HEAD 后中断")
        self.validate(full=False, allow_unverified_done=True)
        return self._read("HEAD.json")

    def _commit_snapshot(self, *, expected_revision: int, operation: str, new_files: dict,
                         updated_manifest: dict, fail_at: str | None = None,
                         full_validation: bool = False) -> dict:
        head, old = self._head_manifest()
        if head["revision"] != expected_revision:
            raise Conflict("版本已变化")
        next_revision = head["revision"] + 1
        tag = _number(next_revision)
        manifest_path = f"manifests/{tag}.json"
        transaction_path = f"transactions/{tag}.json"
        if self._path(transaction_path).exists():
            raise Conflict("上次写入未完成，必须先恢复")
        updated = copy.deepcopy(updated_manifest)
        updated["revision"] = next_revision
        updated["previousManifest"] = head["manifest"]
        self._new(transaction_path, {"baseRevision": head["revision"], "targetRevision": next_revision,
                                     "baseManifest": head["manifest"], "targetManifest": manifest_path,
                                     "planned": [*new_files, manifest_path], "operation": operation})
        if fail_at == "after_transaction":
            raise StoreError("注入：transaction 后中断")
        for relative, value in new_files.items():
            self._ensure(relative, value)
        if fail_at == "after_files":
            raise StoreError("注入：资料写入后中断")
        self._refresh_hashes(updated, new_files)
        self._ensure(manifest_path, updated)
        self.validate_manifest(updated, full=full_validation, allow_unverified_done=True)
        if fail_at == "after_manifest":
            raise StoreError("注入：manifest 后中断")
        self._backup_head(head)
        if self._read("HEAD.json") != head:
            raise Conflict("提交前 HEAD 已变化")
        self._replace_head({"format": FORMAT, "schemaVersion": 4,
                            "revision": next_revision, "manifest": manifest_path})
        if fail_at == "after_head":
            raise StoreError("注入：HEAD 后中断")
        self.validate(full=full_validation, allow_unverified_done=True)
        return self._read("HEAD.json")

    def append_character(self, character: dict, *, expected_revision: int,
                         fail_at: str | None = None) -> dict:
        head, old = self._head_manifest()
        if head["revision"] != expected_revision:
            raise Conflict("版本已变化")
        if not isinstance(character, dict) or not isinstance(character.get("id"), str) or \
                not ID_RE.fullmatch(character["id"]) or not str(character.get("name", "")).strip():
            raise StoreError("人物 ID 或名称无效")
        characters = self._read(old["charactersPath"])
        if any(item.get("id") == character["id"] for item in characters):
            raise Conflict("人物 ID 已存在")
        next_revision = head["revision"] + 1
        path = f"characters/r{next_revision:06d}.json"
        updated = copy.deepcopy(old)
        updated["charactersPath"] = path
        return self._commit_snapshot(expected_revision=expected_revision, operation="append_character",
                                     new_files={path: [*characters, character]},
                                     updated_manifest=updated, fail_at=fail_at)

    def update_character(self, character_id: str, fields: dict, *, expected_revision: int,
                         fail_at: str | None = None) -> dict:
        head, old = self._head_manifest()
        if head["revision"] != expected_revision:
            raise Conflict("版本已变化")
        if not isinstance(fields, dict) or "id" in fields:
            raise StoreError("人物更新不能修改 ID")
        characters = copy.deepcopy(self._read(old["charactersPath"]))
        target = next((item for item in characters if item.get("id") == character_id), None)
        if target is None:
            raise StoreError("人物不存在")
        target.update(copy.deepcopy(fields))
        if not str(target.get("name", "")).strip():
            raise StoreError("人物名称不能为空")
        next_revision = head["revision"] + 1
        path = f"characters/r{next_revision:06d}.json"
        updated = copy.deepcopy(old)
        updated["charactersPath"] = path
        return self._commit_snapshot(expected_revision=expected_revision, operation="update_character",
                                     new_files={path: characters}, updated_manifest=updated, fail_at=fail_at)

    def _validate_task_snapshot(self, tasks: list, manifest: dict, *, previous: list | None = None,
                                allow_reopen: bool = False) -> None:
        if not isinstance(tasks, list):
            raise StoreError("任务快照必须是数组")
        chars = {c["id"] for c in self._read(manifest["charactersPath"])}
        entries = self._entries(manifest)
        previous_by_id = {task["id"]: task for task in (previous or [])}
        ids = set()
        for task in tasks:
            if not isinstance(task, dict) or not isinstance(task.get("id"), str) or not ID_RE.fullmatch(task["id"]):
                raise StoreError("剧情任务 ID 无效")
            if task["id"] in ids:
                raise StoreError("剧情任务 ID 重复")
            ids.add(task["id"])
            if task.get("status") not in ("todo", "in_progress", "done") or task.get("priority") not in ("normal", "high"):
                raise StoreError("剧情任务状态或优先级无效")
            if not isinstance(task.get("plannedChapter"), int) or not 1 <= task["plannedChapter"] <= 9999:
                raise StoreError("剧情任务计划章节无效")
            if any(ident not in chars for ident in task.get("characterIds", [])):
                raise StoreError("剧情任务引用缺失人物")
            before = previous_by_id.get(task["id"])
            if before and before.get("status") == "done" and task.get("status") != "done" and not allow_reopen:
                raise StoreError("不允许重开已完成剧情待办")
            if task.get("status") == "done":
                target = next((entry for entry in entries if entry["id"] == task.get("completedChapterId")), None)
                if target is None:
                    raise StoreError("已完成剧情引用的章节不存在")
                self._check_done_task(task, {target["id"]: self._read(target["path"]).get("content", "")})
        keys = {(" ".join(str(t.get("title", "")).split()).casefold(), t.get("plannedChapter")) for t in tasks}
        if len(keys) != len(tasks):
            raise StoreError("重复剧情任务标题和计划章节")

    def append_task(self, task: dict, *, expected_revision: int, fail_at: str | None = None) -> dict:
        head, old = self._head_manifest()
        if head["revision"] != expected_revision:
            raise Conflict("版本已变化")
        if not isinstance(task, dict):
            raise StoreError("剧情任务结构无效")
        tasks = copy.deepcopy(self._read(old["tasksPath"]))
        if any(item["id"] == task.get("id") for item in tasks):
            raise Conflict("剧情任务 ID 已存在")
        tasks.append(copy.deepcopy(task))
        self._validate_task_snapshot(tasks, old)
        next_revision = head["revision"] + 1
        path = f"tasks/r{next_revision:06d}.json"
        updated = copy.deepcopy(old)
        updated["tasksPath"] = path
        return self._commit_snapshot(expected_revision=expected_revision, operation="append_task",
                                     new_files={path: tasks}, updated_manifest=updated, fail_at=fail_at)

    def update_task(self, task_id: str, fields: dict, *, expected_revision: int,
                    allow_reopen: bool = False, fail_at: str | None = None) -> dict:
        head, old = self._head_manifest()
        if head["revision"] != expected_revision:
            raise Conflict("版本已变化")
        if not isinstance(fields, dict) or "id" in fields:
            raise StoreError("待办更新不能修改 ID")
        tasks = copy.deepcopy(self._read(old["tasksPath"]))
        target = next((item for item in tasks if item.get("id") == task_id), None)
        if target is None:
            raise StoreError("剧情待办不存在")
        target.update(copy.deepcopy(fields))
        self._validate_task_snapshot(tasks, old, previous=self._read(old["tasksPath"]), allow_reopen=allow_reopen)
        next_revision = head["revision"] + 1
        path = f"tasks/r{next_revision:06d}.json"
        updated = copy.deepcopy(old)
        updated["tasksPath"] = path
        return self._commit_snapshot(expected_revision=expected_revision, operation="update_task",
                                     new_files={path: tasks}, updated_manifest=updated, fail_at=fail_at)

    def delete_task(self, task_id: str, *, expected_revision: int, confirm_delete: bool = False,
                    fail_at: str | None = None) -> dict:
        if not confirm_delete:
            raise StoreError("删除剧情待办需要明确确认")
        head, old = self._head_manifest()
        if head["revision"] != expected_revision:
            raise Conflict("版本已变化")
        tasks = [task for task in self._read(old["tasksPath"]) if task.get("id") != task_id]
        if len(tasks) == len(self._read(old["tasksPath"])):
            raise StoreError("剧情待办不存在")
        next_revision = head["revision"] + 1
        path = f"tasks/r{next_revision:06d}.json"
        updated = copy.deepcopy(old)
        updated["tasksPath"] = path
        return self._commit_snapshot(expected_revision=expected_revision, operation="delete_task",
                                     new_files={path: tasks}, updated_manifest=updated, fail_at=fail_at)

    def replace_chapter(self, number: int, chapter: dict, *, expected_revision: int,
                        confirm_replace: bool = False, fail_at: str | None = None) -> dict:
        if not confirm_replace:
            raise StoreError("整章替换需要明确确认")
        head, old = self._head_manifest()
        if head["revision"] != expected_revision:
            raise Conflict("版本已变化")
        entries = self._entries(old)
        if not 1 <= number <= len(entries):
            raise StoreError("章节不存在")
        target = next(entry for entry in entries if entry["number"] == number)
        if not isinstance(chapter, dict) or chapter.get("id") != target["id"]:
            raise StoreError("替换章节 ID 必须与原章一致")
        if not isinstance(chapter.get("content"), str):
            raise StoreError("章节正文不是文本")
        tasks = self._read(old["tasksPath"])
        for task in tasks:
            if task.get("status") == "done" and task.get("completedChapterId") == target["id"]:
                self._check_done_task(task, {target["id"]: chapter["content"]})
        next_revision = head["revision"] + 1
        tag = _number(next_revision)
        chapter_path = f"chapters/{target['id']}-{tag}.json"
        page_number = (number - 1) // PAGE_SIZE + 1
        page = next(page for page in old["pages"] if page["start"] <= number <= page["end"])
        batch = self._read(page["path"])
        new_page = copy.deepcopy(batch)
        index = number - page["start"]
        new_page[index] = {"id": target["id"], "number": number, "title": chapter.get("title", ""),
                           "path": chapter_path, "sha256": self._hash_data(chapter)}
        if target.get("cardPath"):
            new_page[index]["cardPath"] = target["cardPath"]
        page_path = f"pages/p{page_number:04d}-{tag}.json"
        updated = copy.deepcopy(old)
        updated["pages"] = copy.deepcopy(old["pages"])
        for item in updated["pages"]:
            if item["path"] == page["path"]:
                item["path"] = page_path
                break
        return self._commit_snapshot(expected_revision=expected_revision, operation="replace_chapter",
                                     new_files={chapter_path: chapter, page_path: new_page},
                                     updated_manifest=updated, fail_at=fail_at, full_validation=True)

    def recover_next(self) -> str:
        head, old = self._head_manifest()
        tag = _number(head["revision"] + 1)
        transaction_path = f"transactions/{tag}.json"
        if not self._path(transaction_path).exists():
            return "无待恢复写入"
        tx = self._read(transaction_path)
        if tx.get("baseRevision") != head["revision"] or tx.get("baseManifest") != head["manifest"]:
            raise Conflict("未完成记录与当前 HEAD 不一致")
        target = tx["targetManifest"]
        try:
            manifest = self._read(target)
            for path in tx.get("planned", []):
                self._read(path)
            self.validate_manifest(manifest, full=True, allow_unverified_done=True)
            if (manifest["revision"] != head["revision"] + 1 or
                    manifest.get("previousManifest") != head["manifest"]):
                raise StoreError("目标版本不符")
            selected = target
            result = "完成已写入的版本"
        except StoreError:
            selected = f"manifests/{tag}-recovery.json"
            manifest = copy.deepcopy(old)
            manifest.update({"revision": head["revision"] + 1, "previousManifest": head["manifest"]})
            self._ensure(selected, manifest)
            self.validate_manifest(manifest, full=True, allow_unverified_done=True)
            result = "放弃不完整版本并保留旧内容"
        self._backup_head(head)
        if self._read("HEAD.json") != head:
            raise Conflict("恢复前 HEAD 已变化")
        self._replace_head({"format": FORMAT, "schemaVersion": 4,
                            "revision": head["revision"] + 1, "manifest": selected})
        self.validate(full=True, allow_unverified_done=True)
        return result

    def restore_head_from_backup(self, old_revision: int) -> dict:
        """仅在当前 HEAD 损坏/不完整时使用；保留现有文件与备份。"""
        try:
            self.validate(full=True, allow_unverified_done=True)
        except StoreError:
            pass
        else:
            raise Conflict("当前 HEAD 完整，拒绝自动回退")
        backup = self._read(f"recovery/head-{_number(old_revision)}.json")
        manifest = self._read(backup["manifest"])
        if backup["revision"] != old_revision or manifest["revision"] != old_revision:
            raise StoreError("恢复备份版本不符")
        self.validate_manifest(manifest, full=True, allow_unverified_done=True)
        self._replace_head(backup)
        self.validate(full=True, allow_unverified_done=True)
        return backup

    def restore_previous_head(self) -> dict:
        previous = self._read("HEAD.previous.json")
        return self.restore_head_from_backup(previous["revision"])

    def rebuild_memory(self, *, expected_revision: int) -> dict:
        """从原章与现有证据卡建立新索引；缺卡章节保持未知，不编造事实。"""
        head, old = self._head_manifest()
        if head["revision"] != expected_revision:
            raise Conflict("版本已变化")
        entries = self._entries(old)
        try:
            old_memory = self._read(old["memoryPath"])
        except StoreError:
            old_memory = {"archives": [], "summaries": []}
        memory = {"chapterCards": [], "activeFacts": [], "knowledge": [],
                  "openHooks": [], "openQuestions": [], "constraints": [],
                  "timeline": [], "archives": copy.deepcopy(old_memory.get("archives", [])),
                  "summaries": copy.deepcopy(old_memory.get("summaries", [])),
                  "coverage": "partial"}
        facts = {}
        knowledge = {}
        hooks = {}
        questions = {}
        constraints = {}
        timeline = {}
        for entry in entries:
            chapter = self._read(entry["path"])
            if chapter.get("id") != entry["id"]:
                raise StoreError("原章 ID 不一致，停止重建")
            if not entry.get("cardPath"):
                continue
            card = self._read(entry["cardPath"])
            self._verify_card(card, chapter.get("content"), entry["id"])
            memory["chapterCards"].append({"chapterId": entry["id"], "path": entry["cardPath"]})
            for fact in card.get("facts", []):
                facts[fact["id"]] = fact
            for item in card.get("knowledgeChanges", []):
                knowledge[(item["characterId"], item["factId"])] = item
            for item in card.get("hookChanges", []):
                if item.get("status") == "resolved":
                    hooks.pop(item["id"], None)
                elif item.get("status") == "open":
                    hooks[item["id"]] = item
            for item in card.get("openQuestions", []):
                if item.get("status") == "resolved":
                    questions.pop(item["id"], None)
                else:
                    questions[item["id"]] = item
            for item in card.get("constraints", []):
                constraints[item["id"]] = item
            for item in card.get("timeline", []):
                timeline[item["id"]] = item
        memory["activeFacts"] = list(facts.values())
        memory["knowledge"] = list(knowledge.values())
        memory["openHooks"] = list(hooks.values())
        memory["openQuestions"] = list(questions.values())
        memory["constraints"] = list(constraints.values())
        memory["timeline"] = list(timeline.values())
        archived_cards = sum(item.get("counts", {}).get("cards", 0) for item in memory["archives"])
        memory["coverage"] = "complete" if len(memory["chapterCards"]) + archived_cards == len(entries) else "partial"
        next_revision = head["revision"] + 1
        memory["sourceRevision"] = next_revision
        tag = _number(next_revision)
        tx_path = f"transactions/{tag}.json"
        if self._path(tx_path).exists():
            raise Conflict("已有未完成写入，先处理")
        memory_path = f"memory/{tag}.json"
        manifest_path = f"manifests/{tag}.json"
        self._new(tx_path, {"baseRevision": head["revision"], "targetRevision": next_revision,
                            "baseManifest": head["manifest"], "targetManifest": manifest_path,
                            "planned": [memory_path, manifest_path], "operation": "rebuild_memory"})
        self._ensure(memory_path, memory)
        updated = copy.deepcopy(old)
        updated.update({"revision": next_revision, "previousManifest": head["manifest"],
                        "memoryPath": memory_path, "memoryRevision": next_revision})
        self._refresh_hashes(updated)
        self._ensure(manifest_path, updated)
        self.validate_manifest(updated, full=True, allow_unverified_done=True)
        self._backup_head(head)
        if self._read("HEAD.json") != head:
            raise Conflict("提交前 HEAD 已变化")
        self._replace_head({"format": FORMAT, "schemaVersion": 4,
                            "revision": next_revision, "manifest": manifest_path})
        self.validate(full=True, allow_unverified_done=True)
        return self._read("HEAD.json")

    def _archive_descriptor(self, start: int, end: int, path: str, facts: list, knowledge: list,
                            constraints: list, timeline: list, cards: list,
                            hooks: list | None = None, questions: list | None = None) -> dict:
        hooks = hooks or []
        questions = questions or []
        characters = set()
        subjects = set()
        global_relevance = False
        for item in [*facts, *knowledge, *constraints, *timeline, *hooks, *questions]:
            characters.update(item.get("characterIds", []))
            if item.get("characterId"):
                characters.add(item["characterId"])
            if item.get("subject"):
                subjects.add(str(item["subject"]))
            if not item.get("characterIds") and not item.get("characterId") and not item.get("subject"):
                global_relevance = True
        counts = {"facts": len(facts), "knowledge": len(knowledge),
                  "constraints": len(constraints), "timeline": len(timeline), "cards": len(cards),
                  "hooks": len(hooks), "questions": len(questions)}
        return {"id": f"mem_{start:04d}_{end:04d}", "start": start, "end": end, "path": path,
                "characters": sorted(characters), "subjects": sorted(subjects),
                "global": global_relevance,
                "counts": counts, "cardCount": len(cards)}

    def _archive_summary(self, descriptor: dict) -> dict:
        counts = descriptor["counts"]
        text = (f"第{descriptor['start']}-{descriptor['end']}章归档："
                f"{counts['facts']}条事实、{counts['knowledge']}条人物认知、"
                f"{counts['hooks']}个未解伏笔、{counts['constraints']}条期限/金额约束、"
                f"{counts['timeline']}个时间线事件、{counts['cards']}张证据卡。"
                f"原始证据仍在对应章节和证据卡文件中。")
        return {"id": "sum_" + descriptor["id"][4:], "start": descriptor["start"],
                "end": descriptor["end"], "summary": text,
                "sourceChapterIds": [], "characters": descriptor["characters"],
                "subjects": descriptor["subjects"], "counts": counts,
                "archivePath": descriptor["path"]}

    def compact_memory(self, *, expected_revision: int, keep_recent: int = 50,
                       archive_span: int = 50, fail_at: str | None = None) -> dict:
        """把较老的事实/认知/约束/卡片和已完成任务归档；不删除原章或证据卡。"""
        head, old = self._head_manifest()
        if head["revision"] != expected_revision:
            raise Conflict("版本已变化")
        if not isinstance(keep_recent, int) or keep_recent < 1 or not isinstance(archive_span, int) or archive_span < 1:
            raise StoreError("归档窗口无效")
        entries = self._entries(old)
        number_by_id = {entry["id"]: entry["number"] for entry in entries}
        cutoff = len(entries) - keep_recent
        if cutoff <= 0:
            return head
        memory = copy.deepcopy(self._read(old["memoryPath"]))
        tasks = copy.deepcopy(self._read(old["tasksPath"]))
        memory.setdefault("archives", [])
        memory.setdefault("summaries", [])
        memory_archives = copy.deepcopy(old.get("memoryArchives", memory["archives"]))
        task_archives = copy.deepcopy(old.get("taskArchives", []))
        summaries = memory.setdefault("summaries", [])
        new_files = {}
        next_revision = head["revision"] + 1
        tasks_changed = False
        for start in range(1, cutoff + 1, archive_span):
            end = min(cutoff, start + archive_span - 1)
            ids = {entry["id"] for entry in entries if start <= entry["number"] <= end}
            facts = [item for item in memory.get("activeFacts", []) if item.get("sourceChapterId") in ids]
            knowledge = [item for item in memory.get("knowledge", []) if item.get("learnedChapterId") in ids]
            constraints = [item for item in memory.get("constraints", []) if item.get("sourceChapterId") in ids]
            timeline = [item for item in memory.get("timeline", []) if item.get("sourceChapterId") in ids]
            cards = [item for item in memory.get("chapterCards", []) if item.get("chapterId") in ids]
            hooks = [item for item in memory.get("openHooks", []) if item.get("openedChapterId") in ids]
            questions = [item for item in memory.get("openQuestions", [])
                         if item.get("openedChapterId") in ids or item.get("sourceChapterId") in ids]
            if facts or knowledge or constraints or timeline or cards or hooks or questions:
                path = f"memory/archives/m{start:04d}-{end:04d}-r{next_revision:06d}.json"
                new_files[path] = {"start": start, "end": end,
                                   "chapterIds": sorted(ids), "facts": facts, "knowledge": knowledge,
                                   "constraints": constraints, "timeline": timeline,
                                   "hooks": hooks, "questions": questions,
                                   "chapterCards": cards}
                descriptor = self._archive_descriptor(start, end, path, facts, knowledge,
                                                      constraints, timeline, cards, hooks, questions)
                memory["archives"].append(descriptor)
                memory_archives.append(descriptor)
                summary = self._archive_summary(descriptor)
                summary["sourceChapterIds"] = sorted(ids, key=lambda ident: number_by_id[ident])
                summaries.append(summary)
                memory["activeFacts"] = [item for item in memory.get("activeFacts", [])
                                         if item.get("sourceChapterId") not in ids]
                memory["knowledge"] = [item for item in memory.get("knowledge", [])
                                       if item.get("learnedChapterId") not in ids]
                memory["constraints"] = [item for item in memory.get("constraints", [])
                                         if item.get("sourceChapterId") not in ids]
                memory["timeline"] = [item for item in memory.get("timeline", [])
                                      if item.get("sourceChapterId") not in ids]
                memory["chapterCards"] = [item for item in memory.get("chapterCards", [])
                                          if item.get("chapterId") not in ids]
                memory["openHooks"] = [item for item in memory.get("openHooks", [])
                                       if item.get("openedChapterId") not in ids]
                memory["openQuestions"] = [item for item in memory.get("openQuestions", [])
                                           if item.get("openedChapterId") not in ids
                                           and item.get("sourceChapterId") not in ids]
            archived_tasks = [task for task in tasks if task.get("status") == "done" and
                              number_by_id.get(task.get("completedChapterId"), 10**9) <= end]
            if archived_tasks:
                path = f"tasks/archives/t{start:04d}-{end:04d}-r{next_revision:06d}.json"
                new_files[path] = archived_tasks
                task_archives.append({"id": f"task_{start:04d}_{end:04d}", "start": start, "end": end,
                                      "path": path, "count": len(archived_tasks)})
                archived_ids = {task["id"] for task in archived_tasks}
                tasks = [task for task in tasks if task["id"] not in archived_ids]
                tasks_changed = True
        if not new_files:
            return head
        archived_card_count = sum(item.get("cardCount", item.get("counts", {}).get("cards", 0))
                                  for item in memory["archives"])
        memory["coverage"] = ("complete" if len(memory.get("chapterCards", [])) + archived_card_count == len(entries)
                              else "partial")
        memory["sourceRevision"] = next_revision
        memory_path = f"memory/r{next_revision:06d}.json"
        new_files[memory_path] = memory
        updated = copy.deepcopy(old)
        updated.update({"memoryPath": memory_path, "memoryRevision": next_revision,
                        "memoryArchives": memory_archives, "taskArchives": task_archives})
        if tasks_changed:
            task_path = f"tasks/r{next_revision:06d}.json"
            new_files[task_path] = tasks
            updated["tasksPath"] = task_path
        return self._commit_snapshot(expected_revision=expected_revision, operation="compact_memory",
                                     new_files=new_files, updated_manifest=updated, fail_at=fail_at)

    def _select_archives(self, memory: dict, characters: set[str], subjects: set[str]) -> list[dict]:
        selected = []
        for descriptor in reversed(memory.get("archives", [])):
            if descriptor.get("global", True) or characters.intersection(descriptor.get("characters", [])) or \
                    subjects.intersection(descriptor.get("subjects", [])):
                selected.append(descriptor)
        return selected

    def retrieve_memory(self, *, character_ids: tuple[str, ...] = (), subjects: tuple[str, ...] = (),
                        max_archives: int = 2, cursor: int = 0) -> dict:
        if not isinstance(max_archives, int) or max_archives < 1 or not isinstance(cursor, int) or cursor < 0:
            raise StoreError("归档批次参数无效")
        _, manifest = self._head_manifest()
        memory = self._read(manifest["memoryPath"])
        characters = set(character_ids)
        subject_set = set(subjects)
        matches = self._select_archives(memory, characters, subject_set)
        hits = matches[cursor:cursor + max_archives]
        next_cursor = cursor + len(hits)
        complete = next_cursor >= len(matches)
        result = {"summaries": copy.deepcopy(memory.get("summaries", [])),
                  "archiveHits": copy.deepcopy(hits), "complete": complete,
                  "nextCursor": None if complete else next_cursor,
                  "remainingArchives": max(0, len(matches) - next_cursor),
                  "facts": [], "knowledge": [],
                  "constraints": [], "timeline": [], "hooks": [], "questions": []}
        for descriptor in hits:
            archive = self._read(descriptor["path"])
            result["facts"].extend(item for item in archive.get("facts", [])
                                   if not item.get("characterIds") or characters.intersection(item["characterIds"]))
            result["knowledge"].extend(item for item in archive.get("knowledge", [])
                                       if item.get("characterId") in characters)
            result["constraints"].extend(item for item in archive.get("constraints", [])
                                         if not item.get("characterIds") or characters.intersection(item["characterIds"])
                                         or item.get("subject") in subject_set)
            result["timeline"].extend(item for item in archive.get("timeline", [])
                                      if not item.get("characterIds") or characters.intersection(item["characterIds"]))
            result["hooks"].extend(item for item in archive.get("hooks", [])
                                   if not item.get("characterIds") or characters.intersection(item["characterIds"]))
            result["questions"].extend(item for item in archive.get("questions", [])
                                       if not item.get("characterIds") or characters.intersection(item["characterIds"]))
        return result

    def export_legacy(self) -> dict:
        head, manifest = self._head_manifest()
        self.validate_manifest(manifest, full=True, check_memory=False, allow_unverified_done=True,
                               check_content_hashes=False)
        tasks = self._read(manifest["tasksPath"])
        for descriptor in manifest.get("taskArchives", []):
            tasks.extend(self._read(descriptor["path"]))
        exported = {"app": "moluo-novel-manager", "format": "moluo-novel-backup", "schemaVersion": 1,
                "revision": manifest["legacyRevision"] + head["revision"] - 1,
                "savedAt": manifest["legacySavedAt"],
                "data": {"schemaVersion": 1, "meta": copy.deepcopy(manifest["meta"]),
                         "characters": self._read(manifest["charactersPath"]),
                         "chapters": [self._read(entry["path"]) for entry in self._entries(manifest)],
                         "plotTasks": tasks}}
        exported.update(copy.deepcopy(manifest["legacyEnvelopeExtra"]))
        exported["data"].update(copy.deepcopy(manifest["legacyDataExtra"]))
        return exported
