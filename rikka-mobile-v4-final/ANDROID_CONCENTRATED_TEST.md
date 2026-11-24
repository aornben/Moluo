# 墨络 v4 手机维护复测（仅虚构独立 Workspace）

> 目标候选版本：`4.9.3-dev`；安装后的真实 `version`、包内 `VERSION` 与最终交付记录必须完全一致，否则立即停止。
>
> **4.9.3-dev 增量验收项（同一虚构作品，按顺序执行）：**
>
> 在独立 RikkaHub 测试助手及虚构 Workspace 中，通过平台 Shell 审批执行 `sh /skills/moluo-v4-local/install.sh`，运行 `version` 记录版本。
>
> **#04 旧版迁移**。把仓库历史写入器生成的虚构旧 v4 分章目录复制到该 Workspace 的 `/workspace/moluo-v4/`，旧应用无需安装。记录源哈希，执行 `import-old-v4`，用返回的 workId 执行 `doctor --work ID` 并回读正文、人物、记忆、待办、时间线。查看缺失证据卡/记忆字段。重复导入应返回同 ID、`idempotent:true`，源哈希保持一致。另用独立的虚构对象页或损坏页验证报出路径与实际类型，且无新作品发布。已保留原始失败副本时，先脱敏，再保留失败页相对路径、根类型和完整错误用于比较。
>
> **#10 时间线**。在同一虚构作品的两章逐字引用交接事件，用 `draft` 的 `card.timeline` 填入不同记录 ID、相同 `eventId`、各自的 `sourceChapterId` 和 `evidence`。先用精确日期 `2157-04-12` 与 `2157-05-01`，确认后一个 `review` 失败、`finalize` 无法推进 revision。再用不同记录 ID、同主题、`timeStatus:"approximate"` 且无范围复现报告形态，检查 `timelineConflicts` 将其标为疑似并返回证据。用 `doctor --work ID` 核对活动及归档结果。作者需裁定时，使用 Skill 的 `audit → audit-decision` 示例并通过平台确认；按新 revision 重跑，不直接改正文/归档文件。
>
> 只记录以上两项的真实版本、命令、返回值、revision 和文件哈希。保存失败工具记录。

<details>
<summary>4.9.2-dev 历史集中验收提示词</summary>

此提示词用于新候选包的**一次集中开发候选测试**。当前 24 项仍有开发端缺口，不能借此迁移既有私人作品。请先按本目录 README 将新 ZIP 导入独立的墨络测试助手、绑定全新虚构 Workspace，确认 Rootfs 就绪，并保留 Shell 审批。正式小说助手原系统提示词无需修改；主聊天助手不得绑定小说 Workspace 或 Skill。

复制下面整段给**墨络测试助手**，只发一次。测试过程中平台要求的 Shell 审批仍由用户自己决定；若失败请保留工具记录，不用继续重试或改动旧作品。

> 历史候选版本：`4.9.2-dev`。该版本的安装记录保留在历史包中。
>
> 现在对墨络 v4 手机本地候选包做一次集中虚构数据验收。严禁读取、改写、迁移或清理原 `/workspace/moluo/`、`/workspace/moluo-v4/`、既有私人作品。先显示当前 Skill 是否加载、Workspace 是否绑定、Rootfs/Shell 是否可用，然后执行 `sh /skills/moluo-v4-local/install.sh`，记录程序真实 `version` 输出和安装校验结果。若路径、审批、架构或哈希失败，立即停止并报告，不得用原生文件工具直接写 HEAD、章节或索引。
>
> 只用全新作品 `虚构：纸月城` 执行 `init`，记下 `workId`。用真实工具顺序完成：`status`/`doctor`；新增一位虚构人物；新增一卷；登记一条“第一章必须打开纸盒”的待办；提交一个 `schemaVersion:2` 的强结构蓝图（主题、主线、支线、卷纲、章节提纲、人物弧、节奏、重大事件），确认缺失卷纲时会被拒绝。先直接调用一次 `append`，确认程序以中文拒绝并提示必须走 `prepare → draft → review → finalize`。
>
> 对第一章执行 `prepare --work <workId> --id ch_1 --chapter 1 --revision <HEAD>`，记下 prepareId，确认 requirements 含“打开纸盒”待办且 coverage.complete 为 true；用 `read-prepare` 分页读完上下文。先写一份故意不打开纸盒的草稿，执行 `draft`，再用缺少证据的 `review`，确认 verdict=fail 且 `finalize` 被拒绝。再写一份确实包含“阿甲打开纸盒。”的独立草稿（新的 operationId，同一 prepareId），执行 `draft`；审稿候选逐条填写 requirements，`satisfied` 的 quote 必须是草稿中的逐字句子。只有 `review` 返回 pass 后才执行 `finalize`；定稿候选的 `chapter.content` 留空，程序必须使用已审草稿。记录 finalize 返回的 revision、readback 和备份结果，并用 `read` 回读正文。
>
> 第一章虚构正文应包含“九月一日，缴费还有二十一天。”；证据卡登记 `subject=虚构医药费`、`kind=deadline`、`anchorDate=2026-09-01`、`relativeDays=21`、`dueDate=2026-09-22`，引文必须逐字匹配正文。再登记一项有此章证据的虚构地点或物品。对第二章重新执行 prepare；在草稿中写出“阿甲打开纸盒。”，审稿时用该句和实际章节 ID 满足待办要求，finalize 后再用实际正文核销待办；从 `query` 回读核销。另登记一条尚未完成的第三章伏笔，确认不会被自动核销。
>
> 在同一虚构作品中，先故意用旧 revision 提交另一条候选，确认冲突被拒绝；再提交一个正文没有发生的核销，确认被拒绝；再用虚构第三章候选登记同一 `subject=虚构医药费`、未声明 `supersedes` 的 `dueDate=2026-11-06`，确认在写入前被拒绝且 `doctor` 仍正常。用 `query` 的 `nextCursor` 续页，并用 `--from-chapter`/`--to-chapter`/`--status` 做一次章节/待办组合筛选；若 `complete:false`，不得当作已经检索完。用 `chapter-order` 把第二章调到第一章前面，确认 `query chapters` 的 `order` 改变、两章正文 SHA/证据不变；再提交缺少一章的顺序，确认被拒绝。再给人物记录一条带本章原文证据的 `cognition`，给物品记录一条 `relations`/`ownershipHistory`，确认伪证据会被拒绝。运行 `backup`、`export`，核对导出路径、SHA-256 和独立备份。再运行一次同一 `install.sh` 检查安装脚本重复执行后版本和数据不变。不得删除真实或旧作品；如需验证恢复，只允许用本次新建的虚构作品和程序的 `restore-copy`，保留原件。
>
> 切换到新聊天后只凭 `workId` 重新读回人物、章节、未完成伏笔和已完成待办。最后运行 `doctor`，用简体中文列出：实际调用了哪些工具、每次版本号、回读结果、拒绝了哪些危险操作、备份与导出结果、安装是否能再次运行、存在的错误及未检查项。不要把聊天里自称“已保存”当成证据；只以工具结果、文件回读、哈希和 revision 为准。若普通聊天助手仍能访问本小说 Workspace，请标为隔离失败。若云端 DeepSeek 接收了工具返回的虚构正文，按正常云模型流程如实说明。
>
> **4.9.2-dev 历史增量验收项：**
>
> 1. **章节历史与基本回退**：先 `chapter-history --work <workId> --id ch_1 --cursor 0 --limit 20`，确认返回该章不可变历史与 SHA；把 ch_1 用 replace 定稿出第二版（正文改动、证据同步），记录当前 SHA。提交章回退候选（`confirm:true`、`authorDecision`、`targetRevision`、`targetChapterSha256`、`expectedChapterSha256`）执行 `chapter-rollback`，确认回到第一版正文、任务/记忆门禁仍通过、`doctor` 正常；再用错误的 SHA 重试一次，确认被拒绝。
> 2. **结构化状态转换**：先 `mutate` 一条 `state-transition`：`subjectId=<物品ID>`、`stateType=location`、`allowInitial:true`、`value="旧桥"`、带本章逐字证据与 `authorDecision`；再用 `expectedPreviousStateId`/`expectedPreviousValue` 转换到 `"钟楼"`，`query --kind states` 应只显示最新态且旧状态仍在历史。故意用错误的前置状态和普通 `entity-add` 正史状态各试一次，均必须被拒绝。
> 3. **全类别归档与兼容回退**：在虚构作品连续写到第 51 章后，检查 `query --kind memory --from-chapter 1 --to-chapter 1` 能检索到归档的约束/时间线证据，`archivesRead`/`archivesSkipped` 合理；当前事实、认知、未解伏笔不得被归档丢失。先 replace 一个有归档引文的章节并完整保留所有旧引文，确认兼容替换成功、归档/摘要仍在且 history 无重复；再回退到同样保留全部归档引文的可信历史修订，确认成功且 `doctor` 正常。随后尝试删除任一不可变归档引文，确认替换/回退原子拒绝并指出具体记录，不能笼统地把“存在归档”当作拒绝条件。
> 4. **跨实体审计**：用虚构候选制造“任务引用不存在人物”“认知引用不存在事实”“实体关系指向不存在主体”各一次，确认 `doctor` 或写入被中文拒绝；修复后再 `doctor`，必须恢复 `正常`。
> 5. **旧版迁移**：把含 300 章、正文汉字总数≥100 万的**虚构**旧版 JSON 复制到 `/workspace/moluo-v4-imports/`，执行 `import-legacy`，确认返回章节数、独立备份正常、`doctor` 正常、逐章可读；再次导入同一副本，确认返回同一 `workId` 与 `backup` 状态，源文件字节不变。
> 6. **证据失效**：用 replace 改写某章正文并删掉旧证据引文，确认写入被拒绝；用新引文重写后 `doctor` 正常、`query memory` 不再返回旧证据。
> 7. **不可变模型语义审计**：提交完整 `audit` candidate，至少包含 `id/name/kind/status/auditType/result/claim/subjectId/sourceChapterId/evidence/uncertainty`，其中 result 为 `potential_conflict|consistent|uncertain`。确认写 canon、伪证据、entity-update 原地修改和普通实体改成 audit 均被拒绝；query 回读程序写入的 `recordedRevision/contentSha256`。作者决定必须顶层 `confirm:true`，并提交新的 `audit-decision`，record 包含 `id/name/kind/status/targetId/targetRevision/targetContentSha256/decision/authorDecision`，decision 为 `confirmed|rejected`；错误 revision/SHA 与悬挂目标均应拒绝。再追加一条带 `supersedesId/supersededRevision/supersededContentSha256` 的审计更正，确认原记录仍可查询。
> 8. **状态候选唯一授权闭环**：首次 `state-proposal` record 完整包含 `id/name/kind/status/subjectId/stateType/value/sourceChapterId/evidence/uncertainty/allowInitial:true`；query 回读 `recordedRevision/contentSha256/statePayloadSha256`。先在没有批准时提交带 proposalId 的 transition，确认拒绝。再用新 approval ID、顶层 `confirm:true` 提交 `state-approval`，record 包含 `targetId/targetRevision/targetContentSha256/decision:"approved"/authorDecision`；最后 transition 顶层 `confirm:true`，同时指定 `proposalId`、唯一 `approvalId`、相同 authorDecision 和完全相同的严格 payload，确认正史生成而 proposal/approval 仍为不可变 candidate。分别尝试错误 approvalId、`decision:"rejected"`、改 value/JSON 类型、漏字段、重复或冲突批准，均须原子拒绝；后续状态用相同 expectedPreviousStateId/Value 取代 allowInitial。
> 9. **蓝图候选晋升**：在蓝图 candidate 层放入一个虚构支线或章纲；先提交缺 `confirm` 或缺 `authorDecision` 的 `blueprint-promote`，确认分层完全不变。再以 `confirm:true`、作者决定和 `items:[{"field":"subplots","id":"..."}]` 精确晋升，确认只移动所选项、promotionHistory/operation/revision 可回读；重复、未知或与 canon 冲突的选择整体拒绝。
> 10. **待办删除与完成归档**：新增一条可删除的虚构未完成待办，先省略确认调用 `task-delete`，确认拒绝；再用 `confirm:true` 和 `record.authorDecision` 删除，确认完整原记录进入不可变 deleted 归档而不是消失。另创建两条有真实正文完成证据的 done 待办和一条未完成待办；先省略确认调用 `task-archive-done`，确认拒绝，再用 `confirm:true`、`throughChapter` 归档，确认只移动截止章内的 done 待办、未完成项保留、query/doctor/manifest 的路径、数量、范围和 SHA 一致。
> 11. **包与命令契约**：确认 `version` 为 4.9.2-dev；SKILL 命令表共有 29 个用户可用命令且每个都能在 CLI 分派中找到，旧 `append` 只返回安全拒绝。核对安装载荷 SHA；ZIP 自身 SHA 只与包外最终交付记录比较，不把包内 `SHA256SUMS` 误报成 ZIP 自哈希。

开发端候选通过这一步后，仍要按 `V4_24_CURRENT_STATUS.md` 处理未验证项；不可将本提示词的测试结果解释成 24 项全部完成。

</details>
