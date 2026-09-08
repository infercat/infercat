[English](README.md) · 简体中文

# 仓库工具集

请在仓库根目录下运行这些工具。主机与浏览器的使用方法见[根目录 README](../README.zh-CN.md)；构建命令请参考[贡献指南](../CONTRIBUTING.md#building)。

- [install.sh](install.sh)：`curl | sh` 安装脚本；自动选择对应平台的归档，解压前先核对清单里对应的那条 SHA-256，无需 sudo 即可安装，并输出可执行文件路径。
- [install_test.sh](install_test.sh) + [install-fixture/](install-fixture/)：针对本地模拟 release 测试安装脚本，包含损坏归档与校验和错误等场景；由 `make check` 调用。
- [notices.sh](notices.sh)：`make notices` 重新生成第三方开源协议文本与清单；`make notices-check` 用于校验。
- [measure.sh](measure.sh)：针对已有的主机，测量直连、中继或原生连接三条路径的首 token 延迟和 token/s；打印每次运行的结果与中位数，便于记录。
- [subset-font.py](subset-font.py)：从两份语言表中提取字符重新构建 CJK WOFF2 子集；`python3 hack/subset-font.py --check` 检查字符覆盖率。详见[字体配置](../hosting/README.zh-CN.md#重新构建中文字体子集)。
- [load/](load/)：模拟多位好友使用独立密钥与隧道发起请求，采集请求、主机、引擎和中继指标样本；运行 `go run ./hack/load -h` 查看显式指定主机与输出的参数选项。
- [tunneldemo/](tunneldemo/)：在真实隧道上启动轻量 HTTP 服务以供 wasm 开发调试；运行 `go run ./hack/tunneldemo -h` 查看参数列表。[网页版隧道检查](../web/README.zh-CN.md#常用命令)会调用该工具。

测量与压测工具都是对着你自己提供的服务跑的；请用你自己的测试主机和密钥。这些工具不会帮你启动或重新配置引擎。
