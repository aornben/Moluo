# 墨络 v4 手机本地候选包（4.9.3-dev，RC，未通过 Android 真机验收）

当前**候选版本**为 **4.9.3-dev**：`dist/moluo-v4-local-android-candidate-4.9.3-dev.zip`。本维护包只针对 #04、#10 做隔离虚构数据复测。包内 `SHA256SUMS` 校验安装载荷，ZIP 自身 SHA-256 见包外维护记录。公开源码副本提供构建材料。安装时以本次自行构建的 ZIP 和对应哈希为准。

在 **RikkaHub 3.3.1、已就绪的 Linux arm64 Rootfs 和全新虚构小说 Workspace** 使用本包。安装 Skill 后，通过小说助手的 Shell 审批执行安装脚本，脚本会核对程序、Skill、说明、验收提示词和版本文件的 SHA-256。

## 4.9.3-dev 维护与继承的 4.9.2-dev 契约

- **#04 旧 v4 迁移**。复制 `/workspace/moluo-v4/` 的虚构旧分章目录到新作品。支持已证实的数组页、旧版事务、已有原文证据和无哈希手机目录。发布前完成数据校验与独立备份。查看返回的缺失卡片次数及记忆字段，保留解析错误路径与实际类型。
- **#10 时间线**。在 `draft` 的 `card.timeline` 登记事件 ID、精度和逐字证据，走 `review → finalize`。`doctor --work ID` 报告跨活动/历史/归档的 `timelineConflicts`。核对 `severity`、两条日期及证据，有裁定时核对 `chosenContentSha256`；使用 Skill 的绑定审计示例处理作者决定。

- **章节历史与安全回退**：`chapter-history` 从独立备份列出同一章节的不可变修订；`chapter-rollback` 要求作者决定、confirm、预期 HEAD、目标/当前章节 SHA 全部匹配。存在记忆归档时逐条验证不可变归档引文：目标正文保留全部引文才允许替换/回退，并保留归档/摘要、过滤重复历史；任一引文失效或其他任务/记忆证据无法安全重建时原子拒绝。
- **结构化因果状态转换**：`mutate` 的 `state-transition` 支持 `location/travel/inventory/debt/promise/injury/condition`，首次状态需 `allowInitial`，后续状态须匹配当前状态 ID 与值，只追加不可变记录；普通实体增改不能绕过。
- **全类别归档边界**：50 章窗口同时归档历史记录与约束/时间线活动条目，当前事实、认知与未解伏笔保留在活动层；归档可按章节范围检索。
- **跨实体审计**：`doctor` 增加任务人物与完成证据、认知引用事实、实体引用的结构性检查；失效证据会被拒绝而不是静默沿用。
- **旧版迁移加固**：v1/v2/v3 旧 JSON 的规模导入（300 章/百万汉字）、未知字段保留、重复导入幂等并补齐缺失独立备份、失败清理暂存目录、损坏与超量拒绝。
- **Skill/CLI 同步**：Skill 收录 29 个用户可用命令、参数、授权与失败回读顺序；旧 `append` 仅保留为安全拒绝入口。补齐 `task-delete`、`task-archive-done`、`blueprint-promote` 的安全调用与集中验收。
- **不可变语义审计与更正**：`audit`/`audit-decision` 创建后不可原地修改或变更类型；程序写入 recordedRevision/contentSha256。决定绑定目标 revision 与内容 SHA，审计更正只能追加带 supersedesId/revision/SHA 的新记录，原始历史永久保留。
- **状态候选唯一授权链（4.9.2-dev）**：`state-proposal` 记录 revision、内容 SHA 和严格 state payload SHA；`state-approval` 必须 confirm，并绑定目标 revision/content SHA、明确 `approved|rejected`；`state-transition` 同时指定 proposalId 和唯一 approvalId，严格 payload、作者决定和前置状态全部一致后才可写入正史。

## 开发机构建

```text
python rikka-mobile-v4-final/build_zip.py
```

从仓库根目录运行。若 Go 不在 PATH，将环境变量 `MOLUO_GO` 设为 Go 可执行文件的路径。

脚本编译无 CGO 的 Linux arm64 ELF，校验 ELF 架构，生成 `VERSION`、逐文件 `SHA256SUMS`、`PACKAGE_MANIFEST.json` 和**带版本号的新 ZIP**，并逐项回读校验。构建前还会精确核对 SKILL、README 与 Android 集中验收提示的当前版本声明。Go 来源在 `rikka-mobile-v4-core/`，手机运行时不需要 Go 或 Python。旧 ZIP 保留，构建器不会覆盖；同版本不同内容会被拒绝。

## 安卓专项复测的准备

1. 只在**新建、仅含虚构内容**的 Workspace 中导入此 ZIP。使用仓库历史格式生成迁移夹具，手机无需安装旧应用。如仍保存旧 Skill、Workspace 或备份，保持原样。
2. 在 RikkaHub 的小说测试助手启用此 Skill 和 Workspace 的读取、写入与 Shell 工具。Rootfs 必须先完成初始化。保留 Shell 审批；不要为普通聊天助手授予小说 Workspace 或 Skill。
3. 在助手中要求先运行 `sh /skills/moluo-v4-local/install.sh`。若找不到该路径，检查 Skill 导入后的实际路径并报告，不猜测。成功输出版本后，用 `init --title '虚构：纸月城'` 创建新作品，保存返回的作品 ID。
4. 只执行包内 `ANDROID_CONCENTRATED_TEST.md` 顶部 #04 和 #10 专项，保存工具输出、原始路径、`revision` 与文件哈希。

`import-legacy` 只接收 `/workspace/moluo-v4-imports/` 中的**副本**，创建新的作品 ID，保留旧文件和未知字段；同一副本重复导入返回相同 ID 并补齐独立备份。旧版未带正文证据的历史核销会显式标记为未核验。第一次手机验收仅用虚构副本，不导入正式作品。

`import-old-v4` 的旧分章页是目录对象数组。使用仓库历史写入器生成副本即可复测，旧应用无需安装。已有归档按原章节证据转换，未解伏笔进入活动查询层，已提交的历史事务保留原始字节。真机报告涉及的原始 `[]main.entry` 失败页仍待定位。

`export-reading --work ID --format txt|md` 可额外生成按叙事顺序排列的阅读版正文，程序返回路径与 SHA-256。阅读版不含恢复所需全部资料，迁移和备份仍用 `export` 的完整 ZIP。

## 限制

程序级原子提交只保护**通过本地核心程序**提交的写入。RikkaHub 自带文件写入工具仍能直接改工作区文件，平台不提供强制沙箱隔离；必须用独立助手权限、Skill 约束和真实调用轨迹检查。`confirm`/`authorDecision` 是调用方写入的记录字段，程序无法验证真实身份；平台审批与助手隔离仍是身份边界。云端 DeepSeek 会接收工具返回的小说片段。独立备份与作品同处手机，需定期把完整 ZIP 另存。安装脚本的 10 项安装/升级/回退/损坏拒绝测试仍需在真实 Rootfs 上执行；当前只在 Windows 开发端验证了包结构、ELF 架构、哈希与版本一致性。
