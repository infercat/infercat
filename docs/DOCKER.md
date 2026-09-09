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
