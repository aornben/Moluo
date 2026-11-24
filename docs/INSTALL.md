# 构建、安装和升级

## 获取 GitHub Release 安装包

在 `v4.9.3-dev` 的 **Pre-release** 下载 `moluo-v4.9.3-dev-android-arm64.zip` 和 `moluo-v4.9.3-dev-android-arm64.zip.sha256`。在下载目录执行下列命令，确认结果为 `OK` 再导入。

```sh
sha256sum -c moluo-v4.9.3-dev-android-arm64.zip.sha256
```

PowerShell 可执行 `Get-FileHash -Algorithm SHA256 ./moluo-v4.9.3-dev-android-arm64.zip`，将结果与校验文件中的完整值比较。公开包重新构建后在开发机验证，Android ARM64/RikkaHub 真机安装待验证。

## 从源码构建

使用 Go 1.27 和 Python 3。从源码目录根执行下列命令。若 Go 未加入 PATH，把环境变量 `MOLUO_GO` 设为 Go 可执行文件路径。

```sh
python rikka-mobile-v4-final/build_zip.py
```

输出 `rikka-mobile-v4-final/dist/moluo-v4-local-android-candidate-4.9.3-dev.zip`。打包器交叉编译 Linux ARM64 ELF，核对 Skill 命令表，并生成版本文件、载荷清单和载荷校验和。若相同版本的输出已存在且字节不同，请在一个新的源码副本构建并保留旧包。ZIP 外部哈希可用 `sha256sum <ZIP路径>`，PowerShell 可用 `Get-FileHash -Algorithm SHA256 <ZIP路径>`。自行构建的哈希应以本地结果为准。

在电脑上验证 Go 核心，可从 `rikka-mobile-v4-core/` 执行 `go build ./...`。外部发行二进制时请随附[第三方声明](../THIRD_PARTY_NOTICES.md)及其引用的许可证文件。

## Android / RikkaHub 安装

1. 在 ARM64 手机的 RikkaHub 3.3.1 中初始化 Linux arm64 Rootfs，确认为专用小说测试助手绑定全新的虚构 Workspace。
2. 将已校验的 Release ZIP 或自行构建的 ZIP 导入该助手的 Skill，启用所需的文件工具和 `workspace_shell`，保留 Shell 审批。
3. 运行 `sh /skills/moluo-v4-local/install.sh`。若路径不同，以 Skill 实际挂载路径为准。脚本需要 Rootfs 内的 `sh` 与 `sha256sum`，会校验载荷并检查版本，再将程序安装到 `/workspace/moluo-v4-runtime/bin/moluo`。
4. 运行 `/workspace/moluo-v4-runtime/bin/moluo version`，核对 `4.9.3-dev`、`linux` 和 `arm64`。然后按[入门示例](../README.md#安装与入门)初始化虚构作品。

## 升级与回退

- 升级前用当前程序运行 `export --work WORK_ID`，将完整 ZIP 保存到设备外，记录旧版本、作品 ID、revision、导出的 SHA-256，并保留旧 Skill 和可校验的旧安装包。在独立工作区验证导入和 `doctor` 后，再选择是否更新原工作区运行时。
- 安装脚本在替换 runtime 后验证失败时会尝试自动恢复旧程序。升级成功后如需回退，优先在独立工作区安装留存的旧包，并用升级前的导出验证。旧程序读取新写入数据的兼容性需另行确认。
- 程序版本回退与数据恢复是两个操作。先核对 `doctor` 和备份，再考虑使用 `restore-copy` 在副本上检查，避免覆盖现有作品。
- 旧单 JSON 信封的副本放在 `/workspace/moluo-v4-imports/` 后用 `import-legacy --input` 指向绝对路径。无作品 ID 的历史分章格式按 Skill 放在测试 Workspace 的 `/workspace/moluo-v4/` 后使用 `import-old-v4`。部分对象包装格式会被拒绝。保留源副本和脱敏后的失败路径、结构类型。
