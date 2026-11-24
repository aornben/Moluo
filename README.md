# 墨络 Moluo

中文 | [English](README_EN.md)

墨络面向使用 AI 写长篇小说的作者。它把章节、人物、世界设定、剧情待办、伏笔与原文证据保存到作品工作区，让新对话可以凭作品 ID 重新检索写作资料。

**当前版本 4.9.3-dev · 开发预览版。** 建议先用全新虚构作品体验。正式稿件应保有独立备份，写入和作者裁定由作者核对。

## 主要功能

- 分章存储、章节历史和带证据校验的修订回退。
- 正文准备、草稿、审稿、定稿流程。结构化资料包括人物档案、剧情蓝图、待办、伏笔、事实、认知、时间线和状态转换。
- 持久化记忆、分层归档和跨对话检索。新对话从工作区回读资料，分页结果需继续读取。
- 活动、历史与归档时间线冲突提示。对有明确证据的冲突可记录作者裁定，并绑定目标版本和哈希。
- 独立快照、恢复副本、完整作品 ZIP、阅读版导出及部分旧格式迁移。

## 架构与环境

| 目录 | 用途 |
| --- | --- |
| `rikka-mobile-v4-core/` | Go CLI、事务存储、检索、审稿、诊断及生成虚构夹具的 Go 测试 |
| `rikka-mobile-v4-final/` | RikkaHub Skill、Shell 安装器和 Python 打包器 |
| `tools/mobile_v4_reference.py` | 历史分章格式的电脑端参考写入器，用于构造虚构迁移夹具 |

手机目标环境为 **Android ARM64、RikkaHub 3.3.1、已初始化的 Linux arm64 Rootfs**。需要独立的 Workspace、相应文件工具和 Shell 审批。手机运行核心程序时无需安装 Go 或 Python。电脑端构建使用 Go 1.27，已核验工具链为 1.27.4。打包脚本使用 Python 3 标准库，已用 Python 3.14 验证。Windows 上已验证开发机构建与 CLI 冒烟。

## 安装与入门

从仓库 **Releases** 的 `v4.9.3-dev` 预发布获取 `moluo-v4.9.3-dev-android-arm64.zip` 与同名 `.sha256`，按[安装说明](docs/INSTALL.md)核对完整 ZIP。也可以在开发机自行构建。公开 ZIP 来自清理隐私后的源码快照，安装与升级验证在开发机完成，Android/RikkaHub 真机验证待做。

将 ZIP 导入 RikkaHub 的专用小说助手。在隔离的虚构 Workspace 中执行以下命令，经平台批准后运行。

```sh
sh /skills/moluo-v4-local/install.sh
/workspace/moluo-v4-runtime/bin/moluo version
/workspace/moluo-v4-runtime/bin/moluo init --title '虚构：纸月城'
```

保存 `init` 返回的 `data.workId`。用它替换 `WORK_ID`。

```sh
/workspace/moluo-v4-runtime/bin/moluo status --work WORK_ID
/workspace/moluo-v4-runtime/bin/moluo doctor --work WORK_ID
/workspace/moluo-v4-runtime/bin/moluo query --work WORK_ID --kind memory
```

写作、章节回读及作者授权的参数见 [Skill 命令表](rikka-mobile-v4-final/SKILL.md)。日常流程和故障排查见[使用与常见问题](docs/USAGE.md)。

## 数据与备份

当前作品文件位于 `/workspace/moluo-v4-works/`，按作品 ID 划分。CLI 通过 revision、写锁、内容哈希和事务提交保护其写入路径。独立快照位于 `/workspace/moluo-v4-backups/`，完整导出位于 `/workspace/moluo-v4-exports/`。快照和作品仍可能同处一台设备，定期把完整导出另存到独立介质。原生文件工具能直接改文件，应限制助手权限并通过 CLI 写入。云模型会收到发给它的工具返回内容，往云模型发送作品片段前请核对使用范围。

## 验收状态与已知限制

- 项目既有记录包含 Android ARM64/RikkaHub 真机使用。4.9.3-dev 的本轮发布准备验证在电脑端完成。19/24 是实用评估口径，全部功能的独立严格验收仍未完成。
- 既有电脑压力测试回读 301 章、1,051,700 个汉字。百万字级虚构压力结果仅覆盖相应技术场景，百万字真实创作全流程仍待验收。
- 部分真实旧库的对象包装格式仍无法迁移。活动与历史归档之间的时间线冲突尚待手机专项复测。
- 跨对话 30 题已作答，标准答案尚未独立核对。多作品隔离、语义审稿、崩溃恢复及最终交付仍有子项待验。
- AI 可能编造、漏记或出现语义错误。程序可以校验已提交的原文和结构化证据。作者身份与授权仍由平台审批和助手权限保障。

## v5.0 计划

补齐旧库兼容与时间线手机复测，独立核对跨对话答案，完成多作品隔离、语义审稿、崩溃恢复和最终交付验收。完成稳定性完善和验收后再发布 v5.0 正式版。

## 反馈与贡献

可通过 GitHub Issues 提交虚构复现和脱敏日志。贡献方式见 [CONTRIBUTING.md](CONTRIBUTING.md)，安全问题见 [SECURITY.md](SECURITY.md)，变更见 [CHANGELOG.md](CHANGELOG.md)。依赖和分发声明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
