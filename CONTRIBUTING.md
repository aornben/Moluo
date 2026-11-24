# 贡献

先用 GitHub Issue 描述场景、版本和最小虚构复现。提交 PR 时说明改动与验证命令，附脱敏的工具结果。涉及存储或恢复时使用临时 Workspace，核对 revision、回读、备份及失败后的状态。

Go 测试位于 `rikka-mobile-v4-core/`，在该目录执行 `go test ./...`。百万字压力项由 `MOLUO_STRESS=1` 单独启用。涉及 CLI 或 Skill 时，在独立源码副本运行 `python rikka-mobile-v4-final/build_zip.py` 检查命令和版本契约。

所有示例与夹具使用虚构内容。提交前检查暂存文件，排除工作区、导出、备份、日志和凭据。
