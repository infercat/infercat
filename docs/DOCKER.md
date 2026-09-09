# Docker

English · [简体中文](DOCKER.zh-CN.md)

Releases publish `ghcr.io/infercat/infercat:<version>` and `:latest` for Linux amd64 and arm64.
The image contains Infercat, not an inference engine. It uses a pinned Distroless static base:
no shell or package manager, with user/group `65532:65532`.

## Start a host

Start your engine first, then:

```sh
docker run -d --name infercat -v infercat:/data ghcr.io/infercat/infercat:latest --data-dir /data serve --upstream http://host.docker.internal:8080
```

Docker Desktop supplies `host.docker.internal`. On Linux, add
`--add-host=host.docker.internal:host-gateway` before the image name. The engine must listen on
an address reachable from the container; the hostname mapping does not expose a Linux engine
that listens only on host loopback. Use the port your engine actually serves.

`/data` is the persistent volume. A new named volume inherits the image's non-root ownership;
a bind-mounted directory must be writable by UID/GID `65532:65532`. Back up the whole volume:
it holds the host identity (`host.key.json`), keys, configuration, usage, and any remote-admin
state. Keep the same volume when replacing the container so existing invites keep working.

The default command is `serve --data-dir /data`. Supplying a command replaces that default,
so keep `--data-dir /data` on every Infercat invocation. There is no `INFERCAT_DATA_DIR` setting.

## Invite and manage friends

```sh
docker exec infercat infercat --data-dir /data keys add alice
docker exec infercat infercat --data-dir /data keys list
docker exec infercat infercat --data-dir /data status
```

Use the key ID from the list to pause or revoke a friend:

```sh
docker exec infercat infercat --data-dir /data keys pause KEY_ID
docker exec infercat infercat --data-dir /data keys revoke KEY_ID --yes
```

The printed invite is the friend's access credential. Give it to that friend; keep the volume
and any backup private. Commands run directly with `docker exec`; the image has no shell.

## Console

The local console listens on loopback **inside the container**, so it is unreachable from
outside this container's bridge network. Publishing port 9101 does not change that. Use
`docker exec` for keys and status as above.

Enable remote access on the running container with `docker exec infercat infercat --data-dir /data remote on`.
Open the returned link or paste the admin code into the web app to reach `/console`.
Use `remote status`, `remote rotate`, or `remote off` through the same `docker exec` command;
`--json` supports scripts. The internal loopback console must remain enabled. These commands
use the host's local admin API and need no shell or published console port.
An admin code can manage keys and settings: keep it separate from friends' invites.

## Build without publishing

With Docker and its Buildx plugin installed, run `make release-dry` from a checkout. GoReleaser
2.18 builds each Linux image from the existing release binary and loads local tags ending in
`-amd64` and `-arm64`; `--skip=publish` prevents pushes. Version and `latest` multi-architecture
manifests are published only by the tagged release workflow.

The image carries the application's `LICENSE` and `THIRD_PARTY_NOTICES.md` under
`/usr/share/licenses/infercat/`; the base's package copyright notices remain under `/usr/share/doc/`.

## Apple's container tool

Verified with Apple `container` 1.4.1 on Apple silicon. To build without Rosetta, set
`rosetta = false` under `[build]` in `~/.config/container/config.toml`, preserving other
settings, then restart the idle runtime with `container system stop && container system start`.
On an Apple-only Mac, stock `make release-dry` produces the release binaries and packages but
fails at the image stage because it requires Docker/Buildx; it is not a successful full dry run.
Use the resulting Linux arm64 binary with the unchanged Dockerfile, licence and notices:

```sh
image_context=$(mktemp -d)
cp Dockerfile LICENSE THIRD_PARTY_NOTICES.md dist/cli_linux_arm64*/infercat "$image_context/"
container build --platform linux/arm64 -t infercat:local "$image_context"
container network inspect default
```

Use the network's reported `ipv4Gateway` as the host address (192.168.64.1 in this example),
and bind your engine to that address on the Mac, using its actual port. The gateway becomes
available when a container or builder activates the network. `host.docker.internal` is not
provided; this form needs no DNS or packet-filter changes. Before the first run, initialize
the **new** named volume once: Docker copies the image directory's ownership into a new
volume; Apple's tool does not. The pinned helper below changes only the volume root's owner;
the Infercat image continues to run as 65532:65532, with no shell:

```sh
container run --rm --user 0 -v infercat:/data docker.io/library/busybox@sha256:9db7b59979c38555a39def84a31fb98b5296952f9e3afd4f6f11f05b07adfab0 chown 65532:65532 /data
container run -d --name infercat -v infercat:/data infercat:local --data-dir /data serve --upstream http://192.168.64.1:8080
container exec infercat infercat --data-dir /data keys add alice
container exec infercat infercat --data-dir /data status
```

Use `container exec` in place of `docker exec` for the other management commands above.
Keep the same volume when replacing the container. See Apple's [configuration](https://github.com/apple/container/blob/1.4.1/docs/container-system-config.md#build)
and [volume reference](https://github.com/apple/container/blob/1.4.1/docs/volumes.md).
