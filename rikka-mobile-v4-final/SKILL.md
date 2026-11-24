---
name: moluo-v4-local
description: 在已绑定的 RikkaHub 3.3.1 小说工作区使用墨络 v4 本地安全程序读写分章小说、长期记忆、章节历史、结构化状态和独立备份。
---

# 墨络 v4 手机本地助手规则

本 Skill 只适用于用户明确启用的小说助手及其**独立小说工作区**。普通聊天助手不得启用此 Skill 或绑定该工作区。保留助手现有写作系统提示词的视角、风格、篇幅；“只输出正文”视为最终展示要求，内部仍应先用工具读取、审稿、保存和回读。如平台或更高优先级指令冲突，遵守其优先级，不能绕过。

当前程序版本：`4.9.3-dev`（候选版，尚未完成安卓真机集中验收）。`version` 返回的版本必须与包内 `VERSION` 一致。

## 首次安装与边界

1. 必须先确认 RikkaHub Rootfs 已就绪、已授予 `workspace_shell` 的必要执行审批。Skill 文本本身不能运行程序。首次只在**虚构、全新的小说 Workspace** 执行 `sh /skills/moluo-v4-local/install.sh`。失败立即停止，报告原样错误。不得把 `/workspace/moluo/`、`/workspace/moluo-v4/`、既有私人作品当作新版本测试目录。
2. 安装脚本核对随包的 arm64 程序哈希，只复制到 `/workspace/moluo-v4-runtime/bin/moluo`。核心程序只允许访问当前 `/workspace` 下的 `moluo-v4-works`、`moluo-v4-inbox`、`moluo-v4-backups`、`moluo-v4-exports`、`moluo-v4-imports`。不得把其他项目或私人路径交给程序。
3. 通过 `workspace_shell` 运行 `/workspace/moluo-v4-runtime/bin/moluo version`、`init --title '虚构作品名'`，抄录新生成的 24 位作品 ID。随后每次命令都必须带同一 `--work <作品ID>`。新作品不会修改旧数据；没有用户明确授权，绝不把正式作品导入或迁移。
4. 官方 `workspace_write_file` 仍可绕过核心程序，因此助手**只能**把候选 JSON 写入程序返回的 inbox 路径，且 `overwrite:false`；禁止直接写 `HEAD.json`、章节、索引、备份、运行程序或旧工作区。若工具审批、Rootfs、脚本或二进制不可用，停止写入并明确报告，不要改用直接写数据文件。
5. `confirm` 与 `authorDecision` 是**调用方写入的记录字段**，用于把作者决定与不可变内容绑定；程序无法验证填写者真实身份。RikkaHub 的平台审批、助手隔离和用户本人在场仍是最终身份边界，不得把 `confirm:true` 宣称为平台级身份认证。

## 29 个用户可用命令索引（与 4.9.3-dev 源码一致）

所有命令先带 `--workspace /workspace`（默认值即可）和 `--work <作品ID>`；首次 `init` 只带 `--title`。

| 命令 | 关键参数 | 说明 |
| --- | --- | --- |
| `version` | 无 | 版本与目标架构 |
| `init` | `--title` | 新建作品，返回 24 位 ID |
| `status` | `--work` | HEAD、章节、待办、备份、封存状态 |
| `doctor` | `--work` | 完整性检查；`需要修复/严重异常` 时停写 |
| `new-op` | `--work` | 生成 operationId 与 inbox 路径 |
| `segment` | `--work --operation --input --revision --index` | 长章分段（1～65536 字节/段） |
| `prepare` | `--work --id --chapter N --revision` | 写前检索与任务单 |
| `read-prepare` | `--work --prepare --section context\|requirements --cursor --limit` | 读取 prepare 内容并分页 |
| `draft` | `--work --revision --input` | 独立草稿，不改 HEAD |
| `review` | `--work --revision --input` | 逐项审稿；`pass` 才能定稿 |
| `finalize` | `--work --revision --input` | 统一事务定稿 + 备份 + 回读 |
| `chapter-history` | `--work --id --cursor --limit` | 从独立备份列出该章历史修订 |
| `chapter-rollback` | `--work --input --revision` | 有授权地回退到旧修订 |
| `mutate` | `--work --revision --input` | 人物/实体/待办/蓝图/状态/调序 |
| `read` | `--work --id --cursor --limit` | 读取正文（UTF-8 字节游标） |
| `query` | `--work --kind ...` | 查询与分页 |
| `metrics` | `--work` | 汉字/字节/磁盘/对象/归档统计（不是 Token） |
| `backup` | `--work` | 补一次独立备份 |
| `recover` | `--work --operation --confirm` | 放弃未提交事务 |
| `unlock` | `--work --confirm` | 确认写入者停止后清理残留锁 |
| `restore` | `--work --revision` | 恢复到空作品位置 |
| `restore-copy` | `--work --revision` | 生成独立恢复副本 |
| `adopt-recovery` | `--work --input --operation --revision --confirm` | 激活恢复副本 |
| `export` | `--work` | 完整 ZIP（可恢复） |
| `export-legacy` | `--work` | 旧版单文件 JSON（仅供旧流程） |
| `export-reading` | `--work --format txt\|md` | 阅读版（不可恢复） |
| `import` | `--input` | 导入完整 ZIP 到空位置 |
| `import-legacy` | `--input` | 导入用户复制的 v1/v2/v3 旧 JSON |
| `import-old-v4` | 无 | 校验并只读复制无 workId 的旧 v4 分章目录到新作品 |

上表共有 29 个用户可用命令。旧 `append` 名称只保留为安全拒绝入口，调用时会要求改走 `prepare → draft → review → finalize`，不计入可用命令。

`query` 支持 `--kind chapters|characters|tasks|entities|memory|blueprint|states`，以及 `--query`、`--character`、`--chapter`、`--from-chapter`、`--to-chapter`、`--status`、`--from-date`、`--to-date`、`--cursor`、`--limit`。单页超 32KiB 会自动缩小；`complete:false` 必须继续取页。

## 读取与写作

- 写作前先 `status --work ID` 和 `query --work ID --kind chapters --cursor 0 --limit 20`。`manifestSealed:false` 表示旧版 HEAD 未封存 manifest 哈希，应如实报告这一层未校验。按人物和章节目标用 `query --kind chapters|characters|entities|tasks|memory|blueprint|states`，必要时指定 `--query 关键词`、`--character 人物ID`、`--chapter 目标章节`、`--from-chapter`/`--to-chapter` 和 `--status`；按 `nextCursor` 继续取页直到 `complete:true`。按章节范围查询记忆时查看 `archivesRead`/`archivesSkipped`，需要查全书时取消范围并继续取完所有页。对关键事实、期限、伏笔和人物认知必须追溯 `read --work ID --id 章节ID --cursor 0 --limit 8192` 原章证据；正文过长时继续读取下一字节游标。不能把未读归档或未知内容猜成事实。
- 人物认知的旧认识须标注当时章节；现状以最新有证据的变化为准。计划、候选和已确认正史不可混用。写前轻量查询，必要时扩大检索；写后核对正文证据、时间线、期限、金额、地点和未完成任务，不凭相似剧情自动核销。
- 写小说正文优先满足正式助手原有写作提示词。工具操作过程不必出现在最终给用户的正文里。无法确认的连续性事实要保留未知，不自造日期或事件。

## 统一写入：prepare → draft → review → finalize

1. 先运行 `doctor --work ID`；只有返回 `正常` 才开始。写作第 N 章前必须执行：
   `prepare --work ID --id <章节ID> --chapter N --revision <当前HEAD>`。
   程序会读取前章、任务、人物/实体、活动记忆和全部历史归档，生成 `prepareId`、机器可读任务单 `requirements` 和 `coverage`。若 `coverage.complete` 不为 true，或返回“准备失败”，禁止继续定稿。需要查看上下文或大任务单时用 `read-prepare --work ID --prepare <prepareId> --section context|requirements --cursor 0 --limit 20`，按 `nextCursor` 取完，`complete:false` 不能当作看全；`requirementsComplete:false` 时不得开始写作。
2. 用任务单写作。每条硬要求必须对应真实来源、章节证据和稳定 ID。AI 猜测、没有来源的设定只能作为候选，不能自行升级成硬要求；作者确认的延期/豁免必须记录 `authorDecision`。
3. 用 `new-op --work ID` 生成操作 ID 和 inbox 路径。写独立草稿候选：
   `{"operationId":"32位十六进制","prepareId":"...","chapter":{"id":"ch_1","title":"第1章","content":"虚构正文","outline":"...","reviewNotes":"..."},"card":{...}}`
   写到 `new-op` 返回的 inbox 路径，`overwrite:false`；执行
   `draft --work ID --revision <HEAD> --input <路径>`。
   草稿不会改变 HEAD，也不会直接成为正文。`card` 格式同前；每条事实/认知/伏笔必须有当前正文逐字 `evidence` 和来源章 ID。
4. 草稿完成后执行 `review`。审稿候选：
   `{"operationId":"...","prepareId":"...","draftId":"...","requirements":[{"id":"task:task_x","status":"satisfied","quote":"草稿中的逐字句子","location":"..."}],"authorDecision":""}`
   写到 inbox 后运行 `review --work ID --revision <HEAD> --input <路径>`。程序逐项核验：要求 ID 存在、没有遗漏、`satisfied` 的 quote 确实在草稿中、文件/草稿/HEAD 哈希一致。`waived`/`deferred`/`not_applicable` 必须有作者决定，延期目标必须晚于本章。任何缺陷都会使 `verdict=fail`，必须修订并重新跑全量审稿；不能只改一句“已检查”。
5. 只有 review 返回 `pass` 才能定稿。定稿候选：
   `{"operationId":"...","prepareId":"...","draftId":"...","reviewId":"...","chapter":{"id":"ch_1","title":"第1章","content":""},"card":{...}}`
   运行 `finalize --work ID --revision <HEAD> --input <路径>`。`chapter.content` 可留空，程序使用已审草稿正文；若填写必须与草稿完全一致。`finalize` 会再次核验 prepare/draft/review/HEAD、正文哈希和审稿通过，调用统一事务、写 HEAD、备份，并从磁盘真正回读。禁止直接 `append`；直接 `append` 现在会被拒绝。
6. 修订已定稿章节时，先 `prepare` 同一章节 ID，再 draft/review；finalize 候选加 `"mode":"replace","confirm":true,"expectedChapterSha256":"原章节目录 SHA-256"`，并由用户明确授权。插章/调章：先对新增章执行完整 prepare/draft/review/finalize，再用 `mutate` 的 `chapter-order` 提交覆盖全部章节、无重复的 `order` 数组和顶层 `"confirm":true`。该操作只改 manifest 叙事顺序，不改章节文件/SHA/证据；目录和读取以 `query chapters` 的 `order` 为准。
7. 长章分段仍使用同一 `new-op` 操作 ID：每段 1～65536 UTF-8 字节，先写 `<操作ID>-s000N.txt`，执行 `segment`；全部段保存后，把草稿候选设 `"segmentCount":段数`、`chapter.content:""`，执行 `draft` 让程序拼接并哈希，再 review/finalize。中断后先 `status`、核对操作 ID 和已保存段，继续缺段；不得另造 ID 覆盖。
8. 人物、待办、世界实体和蓝图仍使用 `{"operationId":"...","kind":"character-add|character-update|task-add|task-update|task-reopen|task-delete|task-archive-done|entity-add|entity-update|blueprint-set|blueprint-promote|chapter-status|chapter-order|state-transition","confirm":false,"record":{...}}` 和 `mutate`。`chapter-status` 只允许 draft/review 状态维护，不能用它把章节直接改成 final。人物认知/秘密/关系/伤病死亡须使用 `cognition`、`secrets`、`relationships`、`injuries` 等数组并附真实章节证据；实体 `relations`/`ownershipHistory`/`conditionHistory`/`transfers` 同样必须回查原文。`chapter-order` 只做叙事顺序，不重编号、不改正文/证据。
9. 三种已有的高风险 mutation 必须使用 `new-op` 返回的新 operationId、当前 revision 和以下最小授权字段，候选仍只写入当前作品 inbox，且 `overwrite:false`：
   - 删除一条登记错误的待办：`{"operationId":"...","kind":"task-delete","confirm":true,"record":{"id":"task_x","authorDecision":"作者明确说明删除原因"}}`。程序把完整原记录移入不可变删除归档；不得用普通 `task-update` 伪装删除。
   - 归档已完成待办：`{"operationId":"...","kind":"task-archive-done","confirm":true,"record":{"throughChapter":50}}`。只归档 `status:"done"` 且实际完成章节不晚于截止章的任务；没有符合项时必须失败，不能清空活动待办。
   - 晋升明确选择的蓝图候选：`{"operationId":"...","kind":"blueprint-promote","confirm":true,"record":{"authorDecision":"作者确认晋升这些候选","items":[{"field":"subplots","id":"sub_x"}]}}`。只允许程序支持的字段与候选 ID，冲突、重复或不存在的项必须整体拒绝，不能直接覆盖 canon。
10. 语义审计用 `entity-add` 写入 `kind:"audit"`/`"audit-decision"`，状态候选与授权用 `kind:"state-proposal"`/`"state-approval"`；四类记录都只能 `status:"candidate"`，创建后不可原地修改或变更类型。更正只能追加一条绑定原记录 revision 与内容 SHA-256 的新记录；具体字段见下文。
11. 若返回版本冲突、锁、缺备份、哈希、审稿失败、授权冲突或证据错误：保留候选与错误原文，重新 `status`/`doctor`，向用户报告；不能覆盖 HEAD、手改候选或换作品重试。成功后再 `doctor`、`read` 或 `query` 回读版本、章节、任务和备份。若报告“正文已保存、备份失败”，停止后续写入，先 `backup --work ID` 修复并再次 doctor。

## 章节历史与安全回退

1. 需要回退时先 `chapter-history --work ID --id <章节ID> --cursor 0 --limit 20`，按 `nextCursor` 取完；每条历史给出 `revision`、章节文件 `sha256` 和来源快照，必须来自程序的不可变备份，不能手工改正文或索引。
2. 回退候选写入当前作品 inbox：
   `{"operationId":"32位十六进制","chapterId":"ch_1","confirm":true,"authorDecision":"作者授权说明","targetRevision":<旧revision>,"targetChapterSha256":"历史修订 SHA-256","expectedChapterSha256":"当前修订 SHA-256"}`
   运行 `chapter-rollback --work ID --revision <当前HEAD> --input <路径>`。程序要求目标 revision 早于当前、两个 SHA 不同且有效、当前章节与目标章节都来自可信快照，并复用替换路径的任务/记忆/归档门禁。
3. 存在记忆历史归档时不再一律拒绝。程序逐条核验该章节在不可变归档中的原文引文：目标历史正文仍包含全部引文时允许回退，并保留既有 archives/summaries、过滤已归档的重复 history；任一归档引文失效、归档缺失或哈希错误，或活动任务/记忆证据无法安全重建时，操作会原子拒绝并指出具体记录。不得删除归档或手改 HEAD 来绕过。
4. 回退成功后重新 `doctor`、`read` 该章、`query` 任务/记忆/归档核对；失败时保留现场，不覆盖原目录。

## 结构化因果状态转换

`mutate` 的 `state-transition` 用于**显式、可审计**的状态变化，不代替模型或作者的文学推理。候选示例：

```json
{"operationId":"...","kind":"state-transition","confirm":true,
 "record":{"id":"state_key_tower","name":"铜钥匙位置","subjectId":"item_key",
 "stateType":"location","value":"钟楼","sourceChapterId":"ch_2",
 "evidence":"铜钥匙被带到钟楼","authorDecision":"作者确认转移",
 "expectedPreviousStateId":"state_key_bridge","expectedPreviousValue":"旧桥"}}
```

- 支持 `stateType`：`location`、`travel`、`inventory`、`debt`、`promise`、`injury`、`condition`。
- 首次状态必须 `allowInitial:true` 且不得伪造前置状态；后续状态必须匹配当前状态 ID 与值。
- 必须带作者决定、真实正文逐字证据、当前 HEAD revision 和 operationId；状态只追加不可变记录，不覆盖旧状态。
- 普通 `entity-add`/`entity-update` 不能创建或原地修改正史状态；必须走 `state-transition`。
- 失败的转换不得重试成“成功”；先 `doctor`、`query --kind states` 回读当前状态，再以正确的前置状态重试。

## 模型语义审计（#09/#13）

程序只能判断结构引用、章节存在、逐字证据与状态前置条件；人物动机、潜台词、不可靠叙述等语义结论必须由模型提出、作者确认。

1. 模型阅读正文与结构化数据后，用 `mutate` 的 `entity-add` 提交不可变审计候选。最小完整示例：
   `{"operationId":"...","kind":"entity-add","record":{"id":"audit_x","name":"虚构审计候选","kind":"audit","status":"candidate","auditType":"continuity","result":"potential_conflict","claim":"待核对主张","subjectId":"char_x","sourceChapterId":"ch_1","evidence":"当前章节中的逐字引文","uncertainty":"medium"}}`。
   `auditType` 只能是 `continuity|causality|character|timeline|other`，`result` 只能是 `potential_conflict|consistent|uncertain`，`uncertainty` 只能是 `low|medium|high`；逐字 evidence 必须命中 sourceChapterId 的正文。
2. 程序保存四类候选/决定时都会写入不可伪造的 `recordedRevision` 与 `contentSha256`；状态候选还会写 `statePayloadSha256`。成功后必须重新 query 并保存这些值，后续授权不得只凭可复用的文字说明。
3. 审计记录只能是 `candidate`，写 `canon`、用 `entity-update` 原地修改、把普通实体改成审计类型都会被拒绝。需要更正时只能用新 ID 追加一条 audit，并增加 `supersedesId`、`supersededRevision`、`supersededContentSha256` 精确指向原记录；原记录永久保留。
4. 作者决定必须是另一条绑定不可变目标内容的记录，并再次明确确认。示例：
   `{"operationId":"...","kind":"entity-add","confirm":true,"record":{"id":"audit_decision_x","name":"虚构审计决定","kind":"audit-decision","status":"candidate","targetId":"audit_x","targetRevision":12,"targetContentSha256":"64位目标内容SHA-256","decision":"confirmed","authorDecision":"作者确认该审计结论"}}`。
   `decision` 只能是 `confirmed|rejected`；targetRevision/content SHA 必须与重新 query 到的目标完全一致，缺 confirm、目标已经变化或悬挂目标都应原子拒绝。
5. 需要查看待审记录时用 `query --kind entities --status candidate --query <关键词>`；决定记录和被决定的审计仍是不可变 candidate，不会自动成为人物、实体、任务或正史。
6. 证据不足、存在多种解释或模型不确定时，只提交 `result:"uncertain"` 记录并明确告知用户，不得据此自动写正史。

## 时间线冲突检查（#10）

- 在 `draft` 的 `card.timeline` 中提供记录 `id`、`sourceChapterId` 和逐字 `evidence`。能确认同一次事件时共用稳定的 `eventId`，用 `subject` 和 `event` 描述主题与动作。日期使用 `date` 或 `startDate`，显式范围再填 `endDate`。近似时间用 `timeStatus:"approximate"`，未知时间用 `timeStatus:"unknown"` 并省略日期。
- `review` 检查草稿卡，`finalize` 对最终提交卡重新检查。检查覆盖活动、历史和不可变归档。身份已确认且日期或闭区间不相容时，暂停定稿并报告两条记录、时间、章节、逐字证据与原因。主题相近、旧 ID 复用、或近似日期无范围时如实报告疑似，保留合法剧情。
- `doctor --work ID` 的 `timelineConflicts` 会报告已有及裁定过的冲突。查询原记录使用 `query --work ID --kind memory` 并按游标读完；时间线查询包含历史证据，引用为正史前要核对 `doctor` 与作者裁定。若仍有未决冲突，明确标出两种时间，暂停依赖该日期的正史推断。
- 作者选定一条记录后，沿用 `entity-add` 追加 `audit` 候选，设置 `auditType:"timeline"`、`result:"potential_conflict"`，把选定记录的 `contentSha256` 填入 `keepTimelineSha256`，对照记录填入 `otherTimelineSha256`。再用上节的 `audit-decision`、`decision:"confirmed"` 和目标 revision/SHA 记录平台确认过的作者决定。按新 revision 重新准备和审稿。程序保留原文和归档，裁定由 `auditId` 与 `chosenContentSha256` 回读。

## 正文 → 状态候选 → 作者授权 → 正史（#11 调用闭环）

1. 读取目标章节原文与当前状态（`query --kind states --character <主体ID>`）。
2. 若判断可能发生状态变化，提交不可变 `state-proposal`。首次状态最小 record 为：
   `{"id":"proposal_x","name":"虚构状态候选","kind":"state-proposal","status":"candidate","subjectId":"item_x","stateType":"location","value":"旧桥","sourceChapterId":"ch_1","evidence":"当前章节中的逐字引文","uncertainty":"low","allowInitial":true}`。
   后续状态不得带 `allowInitial:true`，必须改为同时带当前的 `expectedPreviousStateId` 和 `expectedPreviousValue`。程序保存后写入 `recordedRevision`、`contentSha256` 和 `statePayloadSha256`；必须 query 回读并保存这些值。
3. 证据不足、多解或不确定时**停止**，向用户说明不确定点，不提交正史。
4. 作者确认后，用全新的唯一 approval ID 提交绑定目标 revision 与内容 SHA 的授权；授权本身也需要顶层 confirm。示例：
   `{"operationId":"...","kind":"entity-add","confirm":true,"record":{"id":"approval_x","name":"虚构状态授权","kind":"state-approval","status":"candidate","targetId":"proposal_x","targetRevision":12,"targetContentSha256":"64位候选内容SHA-256","decision":"approved","authorDecision":"作者确认该状态变化"}}`。
   `decision` 只能是 `approved|rejected`。被拒绝的授权不能用于提交正史；同一候选的重复或冲突批准必须停下并报告，不能任选一条。
5. 再用候选中完全相同的状态 payload 提交正史。首次状态示例：
   `{"operationId":"...","kind":"state-transition","confirm":true,"record":{"id":"state_x","name":"虚构状态","subjectId":"item_x","stateType":"location","value":"旧桥","sourceChapterId":"ch_1","evidence":"当前章节中的逐字引文","allowInitial":true,"proposalId":"proposal_x","approvalId":"approval_x","authorDecision":"作者确认该状态变化"}}`。
   后续转换必须把 `allowInitial:true` 换为与候选完全相同的 `expectedPreviousStateId` 和 `expectedPreviousValue`。
6. 严格 payload 包含 `name`、`subjectId`、`stateType`、`value`、`sourceChapterId`、`evidence`、`allowInitial` 或两个 expectedPrevious 字段；字段是否存在、JSON 类型和值都必须与候选一致（对象键顺序不影响）。transition 必须明确给出唯一 `approvalId`，其 targetId/revision/content SHA、`decision:"approved"`、authorDecision 都须与 proposal 和本次 transition 一致。
7. 程序全部校验通过后才追加不可变正史状态，并在状态及操作记录中保存 proposal/approval 的 revision、内容 SHA 和 `statePayloadSha256`；候选与授权仍为不可变 candidate，不会被原地晋升。
8. 任一步失败都保留候选、授权与错误原文，重新 query 当前记录和状态后再决定是否追加新记录；不得修改旧记录、绕过唯一 approvalId 或把失败重试成“成功”。

## 阅读版导出

需要纯阅读文件时，在已完成 `doctor --work ID` 后执行 `export-reading --work ID --format txt` 或 `--format md`。程序按 `chapter-order` 叙事顺序导出全部章节，返回文件路径、字节数与 SHA-256；回读核对后再另存。阅读版不含完整证据、版本与备份结构，**不得用于恢复或导入**；迁移仍用 `export` 的完整 ZIP。

## 只读健康检查与诊断

用户说“检查墨络”时，依次执行：

1. `version`，核对源码版本、目标架构与包内 `VERSION`。
2. `status --work ID`，报告 revision、章节、待办、最近保存/备份、`backupCurrent`、`manifestSealed`。
3. `doctor --work ID`，报告 `正常／需要修复／严重异常` 与实际检查范围；warning 与完整性错误必须分开说明。
4. `metrics --work ID`，报告汉字、UTF-8 字节、磁盘、对象与归档计数；这些不是 Token 或费用。

报告未检查的手机工具行为、模型是否遵守本 Skill、云模型隐私和外部存储空间。电脑端哈希清单无法替代手机安装后自检。严重异常时停写、保留所有现场文件，禁止宣称已自动修复。

## 备份、恢复与旧版迁移

- `export --work ID` 导出完整作品 ZIP；需要旧版单文件时用 `export-legacy --work ID`，读取其回报路径与 SHA-256，并在用户确认后从 Files 另存。
- `import`/`restore` 只进入空的新作品 ID 位置，绝不覆盖现有作品。主作品 HEAD 损坏时先运行 `restore-copy --work ID --revision <可信版本>`，在 `/workspace/moluo-v4-recoveries/` 生成独立可校验副本，保持损坏现场原样，交由用户选择后续处置；不能直接覆盖。
- v1/v2/v3 旧版导入只读取**用户手动复制**到 `/workspace/moluo-v4-imports/` 的 JSON 副本，运行 `import-legacy --input /workspace/moluo-v4-imports/副本.json`。重复导入同一源文件会安全复用作品并补齐独立备份；源文件不被修改。另有只读 `import-old-v4`：只从固定 `/workspace/moluo-v4/` 复制到新作品 ID，旧目录保持原样；有历史归档时转换到新作品，源归档损坏则拒绝。
- 含旧版无原文依据的已完成待办会标记 `legacyUnverified`，不能把标记当成正文证据。首次手机测试不要执行任何正式作品迁移。
- 旧 v4 分章目录页须为目录对象数组，手机旧 Skill 省略的章节哈希在副本中计算。保留已提交的旧版事务历史，未完成事务应先由旧数据恢复流程处理，或换用完整虚构备份。查看 `chaptersWithoutEvidenceCards` 和 `missingMemoryFields`，缺失资料按缺失报告。对象页或其他未知格式保留原样并记录完整路径和类型错误。
- `recover`/`unlock` 仅在诊断异常、确认写入者已停止且用户同意后使用 `--confirm`。不执行任意文件或系统命令。

## 隐私与备份

本地程序不联网、不上传小说；但 RikkaHub 使用云端 DeepSeek 时，发送给模型的章节/人物/待办工具结果会离开手机。独立备份在同一手机工作区的另一目录，只防作品目录损坏或误删，**不防手机丢失**；用户仍需定期把完整导出 ZIP 另存到自己的安全位置。
