# Docker

[English](DOCKER.md) · 简体中文

每次发布都会为 Linux amd64 和 arm64 提供 `ghcr.io/infercat/infercat:<version>` 和 `:latest`。
镜像只包含 Infercat，不包含推理引擎。它使用固定摘要的 Distroless static 基础镜像：
没有 shell 或包管理器，以用户和用户组 `65532:65532` 运行。

## 启动主机

先启动推理引擎，然后运行：

```sh
docker run -d --name infercat -v infercat:/data ghcr.io/infercat/infercat:latest --data-dir /data serve --upstream http://host.docker.internal:8080
```

Docker Desktop 提供 `host.docker.internal`。Linux 上需要在镜像名前加上
`--add-host=host.docker.internal:host-gateway`。引擎必须监听容器能访问的地址；这个域名映射
不会让仅监听 Linux 主机回环地址的引擎变得可访问。请使用引擎实际监听的端口。

`/data` 是持久化数据卷。新建的命名卷会继承镜像中非 root 用户的目录所有权；
如果挂载主机目录，该目录必须允许 UID/GID `65532:65532` 写入。请备份整个数据卷：
它保存主机身份（`host.key.json`）、密钥、配置、用量，以及远程管理状态。
替换容器时保留同一个数据卷，已有邀请码才能继续使用。

默认命令是 `serve --data-dir /data`。显式提供命令会替换默认值，因此每次调用 Infercat
都要带上 `--data-dir /data`。程序没有 `INFERCAT_DATA_DIR` 设置。

## 邀请和管理朋友

```sh
docker exec infercat infercat --data-dir /data keys add alice
docker exec infercat infercat --data-dir /data keys list
docker exec infercat infercat --data-dir /data status
```

使用列表中的密钥 ID 暂停或撤销某位朋友的访问：

```sh
docker exec infercat infercat --data-dir /data keys pause KEY_ID
docker exec infercat infercat --data-dir /data keys revoke KEY_ID --yes
```

打印出的邀请码就是朋友的访问凭证。把它交给那位朋友；数据卷及其备份应妥善保管。
通过 `docker exec` 直接运行命令；镜像中没有 shell。

## 控制台

本地控制台只监听**容器内部**的回环地址，因此无法从容器的桥接网络外访问。
映射 9101 端口也不会改变这一点。请按上面的示例通过 `docker exec` 管理密钥和查看状态。

如果挂载的数据已经启用远程访问，就可以在网页应用中使用管理码打开控制台。
目前必须先在本地控制台启用远程访问并保留管理码。用于在终端启用远程访问的命令将在下个版本提供。
管理码可以管理密钥和设置，应与朋友的邀请码分开保管。

## 构建但不发布

安装 Docker 及其 Buildx 插件后，在代码仓库中运行 `make release-dry`。GoReleaser 2.18
用已有的发布二进制构建各个 Linux 镜像，并加载以 `-amd64` 和 `-arm64` 结尾的本地标签；
`--skip=publish` 会阻止推送。只有标签触发的发布工作流才会发布版本号和 `latest` 的多架构清单。

镜像内 `/usr/share/licenses/infercat/` 包含应用的 `LICENSE` 和 `THIRD_PARTY_NOTICES.md`；
基础镜像的软件包版权声明保留在 `/usr/share/doc/` 下。
