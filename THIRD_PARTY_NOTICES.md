# 第三方声明

| 项目 | 与墨络的关系 | 分发材料 |
| --- | --- | --- |
| Go 标准库及运行时，构建核验版本 1.27.4 | Go 模块无额外模块依赖。ARM64 程序静态包含 Go 运行时及所用标准库 | [Go LICENSE](licenses/Go-LICENSE.txt)、[Go PATENTS](licenses/Go-PATENTS.txt) |
| Go 工具链内附的 BoringSSL 通知 | Go 目标平台依赖清单包含标准库的 `crypto/internal/boring` 包路径，随附上游通知供发行核对 | [原始通知](licenses/Go-BoringSSL-LICENSE.txt) |
| Python 3、POSIX Shell、`sha256sum` | 外部构建或运行工具，脚本调用系统工具 | 本源码副本只分发项目脚本 |
| RikkaHub、Linux Rootfs、模型服务 | 运行环境和接口集成 | 本源码副本不分发客户端、系统镜像、模型权重或服务凭据 |

重新分发 Linux ARM64 二进制时，请随同二进制提供上表相关许可和版权声明。Go 及其组件沿用各自的许可。
