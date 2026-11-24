# 使用与常见问题

- **新聊天怎样找回资料？** 绑定同一作品 Workspace，提供作品 ID，先调用 `status` 或 `doctor`，再按需求调用 `query` 和 `read`。看到 `nextCursor` 时按 Skill 续页。助手自行复述的记忆需与工具结果核对。
- **怎样安全定稿？** 用 `prepare → draft → review → finalize`，按需用 `read-prepare` 分页。核对审稿结果、当前 revision、逐字证据与作者决定，定稿后查看回读结果和备份状态。[Skill 提供候选示例](../rikka-mobile-v4-final/SKILL.md)。
- **时间线冲突如何处理？** 检查返回的事件标识、日期精度和两条原文。按 Skill 记录作者裁定并重新核对 revision。保留原始证据与历史。
- **阅读版能用于整库恢复吗？** `export-reading --work WORK_ID --format md` 供阅读。需要恢复时保留 `export --work WORK_ID` 生成的完整作品 ZIP。
- **怎样构造虚构旧分章数据？** [历史协议](../rikka-mobile-v4/reference/V4_STORAGE.md)和[离线参考写入器](../tools/mobile_v4_reference.py)描述旧格式。下面的示例可在仓库根运行，用于初始化空白历史目录。分章和事务的虚构测试示例见 [Go 迁移测试](../rikka-mobile-v4-core/oldv4_maintenance_test.go)。参考写入器供电脑端迁移夹具使用。
- **装不上或找不到程序？** 先确认 Skill 的实际挂载位置、Rootfs 架构、工作区权限、平台审批和校验输出。安装成功后用 `version` 核对。提交反馈时使用虚构复现。

```python
from pathlib import Path
from tools.mobile_v4_reference import MobileV4Store

store = MobileV4Store(Path("fixture-workspace") / "moluo-v4")
store.create_empty(meta={"title": "虚构：纸月城"})
```
