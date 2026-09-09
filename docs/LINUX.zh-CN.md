# Linux 安装包

[English](LINUX.md) · 简体中文

每个版本提供 amd64 和 arm64 的 `.deb`、`.rpm` 安装包。二进制安装到
`/usr/bin/infercat`，同时安装一个**用户级**服务；本指南、许可证和第三方声明位于
`/usr/share/doc/infercat/`。安装不会启用或启动服务。

## 安装

从 [Releases](https://github.com/infercat/infercat/releases) 下载对应的安装包和校验和文件。
将 `<v>` 替换为版本号；ARM 机器请将 `amd64` 替换为 `arm64`。
安装前，对照校验和文件中对应的条目，核验安装包的 SHA-256。

```sh
sudo apt install ./infercat_<v>_linux_amd64.deb   # Debian / Ubuntu
sudo rpm -i ./infercat_<v>_linux_amd64.rpm       # Fedora / RHEL family
```

升级 RPM 包时使用 `sudo rpm -U ./infercat_<v>_linux_amd64.rpm`。Debian/Ubuntu 使用新包
再次运行 `apt install`。升级后，每位正在运行服务的用户都应执行
`systemctl --user daemon-reload` 和 `systemctl --user restart infercat`。

## 以自己的用户身份启动

先启动推理引擎。以运行主机的用户身份执行以下命令，**不要加 sudo**：

```sh
systemctl --user daemon-reload
systemctl --user enable --now infercat
journalctl --user -u infercat -n 30 --no-pager
infercat status
infercat keys add alice
```

`/usr/lib/systemd/user/infercat.service` 中的服务运行 `infercat serve`，自动检测本地引擎，
失败时重启。它会使用同一用户之前运行 `infercat serve` 时保存的设置。如需更换引擎地址，
先停止服务，运行 `infercat serve --upstream http://127.0.0.1:8080`，再按 Ctrl-C 停止这个
前台主机，然后重新启动服务。不要让两个主机同时使用同一个数据目录。

`systemctl --user disable --now infercat` 会停止服务并取消自动启动。
用户级服务通常随登录会话运行；若希望退出登录后仍继续运行，管理员可执行
`sudo loginctl enable-linger USERNAME`。

## 数据与控制台

数据属于运行主机的用户，保存在 `$XDG_CONFIG_HOME/infercat`；未设置该变量时使用
`~/.config/infercat`。服务和 CLI 必须使用同一个配置目录。
请备份整个目录，其中包含主机身份、密钥、设置和用量。卸载安装包不会删除这些用户数据；
请妥善保管数据及其备份。

控制台监听 `127.0.0.1:9101`。在主机上运行 `infercat console` 即可打开；它不会暴露在服务器
的公网接口上。无桌面的服务器可以通过 SSH 转发这个回环端口，并私下使用
`infercat console --print` 打印的链接。如需通过网页应用远程管理主机，请先在本地控制台
启用远程访问，并将生成的管理码与朋友的邀请码分开保管：管理码可以管理主机。
