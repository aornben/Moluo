# 墨络手机 v4 分章存储协议（开发候选）

本协议只要求官方 RikkaHub 的 `workspace_read_file` 和 `workspace_write_file`。现有 `/workspace/moluo/novel-data.json`、`backups/`、`continuity/` 均保留原样；新格式单独放在 `/workspace/moluo-v4/`。切换 Skill 不等于自动迁移或删除旧文件。

## 1. 路径和文件

| 文件 | 用途 |
| --- | --- |
| `/workspace/moluo-v4/HEAD.json` | 当前版本指针；只含版本和 manifest 路径 |
| `/workspace/moluo-v4/HEAD.previous.json` | 切换 HEAD 前回读验证的上一版指针，供新聊天快速找回 |
| `manifests/r000001.json` | 不可变的分页目录、资料文件路径、上一个版本和可选 `contentHashes` |
| `pages/p0001-r000001.json` | 最多 50 个章节目录项；新增章只写受影响的最后一页 |
| `chapters/<chapterId>-r000001.json` | 完整单章对象，保留未知字段；后续改章写新版本，不覆盖旧章 |
| `characters/r000001.json` | 人物数组快照；没有修改人物时复用旧路径 |
| `tasks/r000001.json` | 剧情待办数组快照；没有修改时复用旧路径 |
| `memory/r000001.json` | 核心长期记忆快照（活动层 + 摘要/归档索引）；历史证据卡继续按章独立保存 |
| `memory/cards/<chapterId>-r000001.json` | 新章短证据卡，含时间、事实、人物认知、伏笔和任务原文依据 |
| `memory/archives/m0001-0050-r000001.json` | 历史记忆归档：事实、认知、伏笔、问题、约束、时间线、证据卡索引；原章/卡不删除 |
| `memory` 快照的 `archives`/`summaries` | 按需检索用的归档描述与压缩摘要；摘要不替代原始证据 |
| `tasks/archives/t0001-0050-r000001.json` | 已完成且较老的任务归档；导出旧 JSON 时与活动任务合并 |
| `transactions/r000001.json` | 本轮计划写的文件和原 HEAD 版本；留作中断检测，不删除 |
| `recovery/head-r000000.json` | HEAD 改写前的回读备份；不能代替完整作品导出 |

路径中的 `<chapterId>` 只能来自已验证的 `^[A-Za-z0-9_-]{1,128}$`，不能由聊天文本拼出任意路径。版本号是六位以上十进制整数，单调递增。**所有章节、人物、任务、记忆和 manifest 写入都用 `overwrite:false`；只有 HEAD 经过备份并最后写入。**

HEAD：

```json
{"format":"moluo-mobile-v4","schemaVersion":4,"revision":1,"manifest":"manifests/r000001.json"}
```

manifest 至少包含 `format`、`schemaVersion`、`revision`、`previousManifest`、`meta`、`charactersPath`、`tasksPath`、`memoryPath`、`chapterCount`、`pages`。一页最多 50 章；页引用为 `{"start":1,"end":50,"path":"pages/p0001-r000001.json"}`。页内每个章节目录项为 `{"id":"ch_1","number":1,"title":"测试章节","path":"chapters/ch_1-r000001.json","cardPath":"memory/cards/ch_1-r000001.json"}`；迁入的旧章没有证据卡时可省略 `cardPath` 并标记记忆覆盖不完整。目录项不含正文。单章文件保存完整章节对象 `id/title/content` 及旧版未知字段。人物和待办也保留原对象与未知字段。

manifest 可带 `contentHashes`：人物、待办、记忆和每个目录页的 SHA-256。电脑离线参考程序在每次提交时维护它；`validate_manifest` 在存在时逐项核对。手机只有读写工具，**不要求也不伪造**该字段；没有它时健康报告必须明确写“人物/待办/记忆/目录页内容哈希未验证”，不能宣称内容未被篡改。

电脑离线迁移器可在章节目录项中附 `sha256`，完整性检查必须核对该值。手机 Skill 只使用读写文件工具，不能可靠地自行计算 SHA-256；手机新章不填伪造哈希，只做逐字段回读和引用检查，并在健康报告中标明“内容哈希未检查”。开发端包文件的 SHA-256 由独立打包程序计算和核验。

记忆快照可沿用 v3 的 `activeFacts`、`knowledge`、`openHooks`、`openQuestions`，并增加 `chapterCards`、`constraints`、`timeline`、`coverage`、`sourceRevision`；manifest 的 `memoryRevision` 必须与记忆快照的 `sourceRevision` 相同。每条重要约束保存来源章节、短原文证据、确定/推测/未知/冲突；读者知道与角色知道分别记录。证据卡路径同时在 manifest 的章节目录项和记忆快照中可定位；新章保存时始终写一个新记忆快照，缺卡则 `coverage=partial`。**索引不是正文来源，失效时根据 manifest 指向的原章重建。**

`constraints` 条目支持：
- `kind:"deadline"`：`subject` + `dueDate`（YYYY-MM-DD），或 `anchorDate` + `relativeDays`；二者同时存在时必须换算一致。
- `kind:"amount"`：`subject` + 正数 `value` + `currency`。

`timeline` 条目支持 `subject`、`startDate`/`date`、可选 `endDate`。每条约束/事件都要有 `sourceChapterId`、正文 `evidence`、`certainty`。同一 subject/币种的新期限或金额若改变旧值，必须用 `supersedes` 指向被替代 ID；否则视为矛盾。手机 Skill 用这些字段做写作前检查，电脑深度检查执行同一规则。

## 2. 读取和写作

新聊天依次读 HEAD → manifest → 人物、待办、核心记忆；此时不读全书正文。写第 N 章时只读包含 N-1 的目录页，再读上一章、相关人物和待办、相关证据卡；旧事实不确定或有冲突时，沿 manifest 的页范围精确读取对应原章。不能只凭摘要核销待办。manifest 只有页路径，不装几百个章节目录项；仍须监测文件工具返回和模型上下文。

**所有 `overwrite:false` 写入都必须先读后写**：文件不存在才写；已存在且解析后的 JSON 与本次完全一致就跳过；存在但内容不同立即停止。手机 `workspace_write_file` 对已存在文件即使同内容也会报错，不能把电脑端的同内容幂等当成手机行为。

**任务快照必须保留全部旧任务 ID**：禁止因漏传任务而整表删除；已完成任务改回 todo 只有在用户明确“确认重开待办”后允许；任何新增或变更的 `done` 都必须在 `completedChapterId` 原章中找到 `completionEvidence` 的短引文。新增人物、独立登记/修改待办也走同样的 transaction → 新快照 → manifest → 备份 HEAD → 最后 HEAD 顺序，不能只写一个快照文件。

普通写作：检索约束 → 拟稿 → 核查时间、知情、因果、伏笔、动机、节奏、语言重复 → 在交付前修订**新草稿** → 按下述协议保存 → 回读 → 交付完整正文。只凭正文确凿证据核销到期任务；任务完成说明不能夸大未证实后果。历史正文修改仍须旧 Skill 的明确替换授权。

## 3. 提交协议与恢复

1. 读 HEAD 和 manifest，记住旧 revision。检查目标章不存在或符合明确的追加/替换要求；核对人物 ID、任务引用、章节编号和待办完成依据。
2. 再读 HEAD；若 revision 或 manifest 路径变化，停止。检查 `transactions/r<新revision>.json`：若存在未完成记录，**先恢复，不新写**。
3. 以 `overwrite:false` 写 transaction，列出 `baseRevision`、目标 revision、计划的全部路径及操作类型。回读确认。此时正式 HEAD 未变。
4. 逐一写新章、受影响的任务/人物/记忆快照与最多 50 项的最后一页，全部用新路径和 `overwrite:false`；读回并核对 ID、文本、引用。没有修改的旧页和资料文件复用旧路径。写新 manifest 并回读，确保每个新引用的文件已存在且可解析。
5. 备份旧 HEAD 到 `recovery/head-r<旧revision>.json`（`overwrite:false`）并回读，同时写 `HEAD.previous.json`（`overwrite:true`）并回读。再次读取当前 HEAD；若不等于最初版本，停止并保留 transaction，不能覆盖他人的新版本。
6. **最后**写 HEAD（`overwrite:true`），再回读 HEAD、manifest、新章及相关资料。成功才说保存完成。写完 HEAD 后失败时，先查当前 HEAD 指向的文件是否完整；完整则视为提交成功，不要反复写正文。若 HEAD 损坏或指向缺失文件，先读 `HEAD.previous.json`，再核对对应的 `recovery/head-r<旧revision>.json` 和旧 manifest，确认后恢复指针并回读；不能凭聊天记忆猜版本。

transaction 留在目录中作为审计记录。启动时若 HEAD 为 r，检查 `transactions/r<r+1>.json`：存在即代表上次可能在 HEAD 切换前中断；**不可忽略后继续写**。若其中所有计划文件和 manifest 均完整，可在复核旧 HEAD 未变后完成 HEAD 切换；否则保持旧 HEAD，报告未完成，不得声称正文已保存。失败的 r+1 可由恢复流程写入一个指向旧内容的空操作 manifest 并推进 HEAD，保留原 transaction 和残留文件，不删除任何数据，然后从 r+2 重试。若无法核对，不自动恢复。

以上是基于文件工具的**可检测提交协议**，不是数据库事务，也没有跨文件原子性或 compare-and-swap。同一时刻只允许一个小说聊天写。RikkaHub 工具若单文件写入失败、返回截断或无法回读，立即停止，不凭模型记忆补全。整个目录导出是可靠离线备份的必要环节。

## 3.1 长期记忆分层、归档与按需检索

`memory` 快照是活动层，不保存全书所有事实。超过最近约 50 章的事实、人物认知、未解伏笔、开放问题、期限/金额约束、时间线和证据卡索引可归档到 `memory/archives/m<起>-<止>-r<版本>.json`；已完成且较老的任务归档到 `tasks/archives/t<起>-<止>-r<版本>.json`。归档文件保存完整条目、来源章节和 `evidence`；**不得删除原章**、`memory/cards/` 或改写历史证据；归档只增加可检索副本。

- `memory.archives` 与 `manifest.memoryArchives`：归档描述（起止章、路径、角色、主题、计数）。
- `memory.summaries`：每个归档的压缩摘要，含 `sourceChapterIds`、`counts`、`archivePath`；摘要只用于定位，不是新事实来源。
- `manifest.taskArchives`：已完成任务归档描述；`export_legacy` 必须把活动任务与归档任务合并。
- 归档写入同样走 transaction → archive 文件 → 活动 memory/tasks → manifest → 备份旧 HEAD → **最后** HEAD；所有文件先读后写。
- 每次创作先读 `summaries` 定位相关归档；每批最多读 2 个，若还有相关归档则继续下一批直到全部相关归档已检查。**手机没有可调用的 `retrieve_memory`/`context_for` Python 函数**：模型必须在回复或上下文中自己保存“已读 archive 路径列表 + 下一个未读 path”，按批读取并更新；电脑参考实现的 `nextCursor`、`remainingArchives`、`complete`/`archiveSearchComplete` 只是这个文件级流程的模拟字段。**不能把首批 2 个误当完整结果，也不必读取不相关 archives。**未解伏笔必须能被相关角色检索命中，不能因压缩而丢弃；无法续检时停止自动核销并报告未检查范围。
- 归档完成后，活动层的事实/认知/约束/卡片应约为最近 50 章；已完成任务移入归档；活动任务仍按原样保留未完成项。

## 3.2 非章节写入、空作品和章节替换

- **人物**：新增写 `characters/r<新版本>.json`（旧数组 + 新人物）；修改保留 ID，合并字段后写新数组。manifest 的 `charactersPath` 指向新文件。
- **待办**：独立登记/修改写 `tasks/r<新版本>.json`；保留全部旧任务 ID；`done` 的证据要求同第 2 节。删除任务需用户明确确认，建议不提供普通删除入口。
- **空白作品**：没有旧数据时，写空人物、空待办、空记忆（`coverage=complete`、`sourceRevision=1`）和空页目录，manifest `chapterCount=0`，最后写 HEAD；已有 HEAD 时禁止初始化覆盖。
- **整章替换**：仅用户明确“确认替换第 N 章”后执行；章节 ID 不变，写新章节文件与受影响页，章节数不变，旧章保留。替换后证据卡、长期记忆和该章 done 任务必须能重新核对，否则停止。

## 4. 旧版迁移与回退

旧 v1/v2/v3 的 `novel-data.json` 都是 `schemaVersion=1` 信封；v2/v3 指 Skill 版本，不代表文件 schema 变了。迁移前读并校验旧信封，先导出整个旧 `moluo/`，记录旧 revision。逐章按原顺序复制完整对象到 v4 新路径；人物、待办复制到独立文件；已验证的 v3 索引可以参考，但未审章节保持未知。全部新文件和 manifest 回读通过后，最后以 `overwrite:false` 创建 v4 HEAD。**旧文件不删除、不改写。**

若旧文件很大，AI 不能可靠地把全书正文塞进一次工具调用：停止手机端迁移，使用电脑上的离线参考工具处理**用户导出的独立副本**，然后在手机 Files 导入整个 v4 目录。不得用截断后的 JSON 冒充迁移成功。迁移中断且 HEAD 不存在时，旧版仍可照常使用；先检查已写文件，再续写缺少文件或换独立工作区重试。

回退只需在小说助手禁用 v4 Skill、启用 v3 Skill，旧 `/workspace/moluo/novel-data.json` 仍在。v4 新写的章节不会自动回流旧版；若要带回，必须先完整导出 v4 并重建兼容的 v1 信封，校验后由用户确认覆盖旧版。禁止让两个格式同时写同一作品。

## 5. 导出、完整性和限制

日常完整备份：在 Files 导出**整个** `/workspace/moluo-v4/`，连同 `HEAD.json`、manifests、chapters、characters、tasks、memory、transactions、recovery。只复制 HEAD 或 manifest 不是完整小说。离线参考工具可将当前 manifest 引用的文件组装成旧 `novel-data.json`；手机端若无可靠 JSON 组装程序，不能声称已生成单文件完整导出。

完整性检查：HEAD 与 manifest 版本一致；页范围连续且每页不超过 50 章；章节编号连续、ID/路径唯一；每个引用文件存在且 JSON 合法；章节文件 ID 与目录项一致；任务引用的人物和完成章节存在；记忆引用的章节与人物存在；已读取证据卡的引文须出现在对应已保存原章中；所有 `done` 任务（包括新增任务）的完成引文须在对应原章中；时间线、相对/绝对期限和金额约束须可换算且无未声明 `supersedes` 的矛盾；`memoryArchives`/`taskArchives` 的归档条目、来源章节、证据卡和任务必须可核对，摘要不得替代原章；`contentHashes` 存在时逐项核对人物、待办、记忆、目录页和归档；检查 transaction 的下一版本是否未完成。证据与正文不一致时先停写并报告路径，不能假定只有索引损坏。普通写作前只核对本次涉及的页和文件；定期完整导出或恢复时检查所有章节。

工具读取上限与上下文限制仍存在。单章接近 8 MiB 时停止写入并分章规划，不能拆掉已有章节。无法从 `workspace_write_file` 获得强制事务、文件锁或安全的原子条件更新；本协议只保证**检测和保留恢复材料**，不能保证任何设备/模型永远不丢数据。云端模型仍会接收被读取的小说资料。
