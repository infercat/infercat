# Linux packages

English · [简体中文](LINUX.zh-CN.md)

The release includes `.deb` and `.rpm` packages for amd64 and arm64. They install the binary
at `/usr/bin/infercat`, a **user** service, and this guide with the licence and third-party
notices at `/usr/share/doc/infercat/`. Installation does not enable or start the service.

## Install

Download the matching package and checksums file from [Releases](https://github.com/infercat/infercat/releases).
Replace `<v>` with the release version; use `arm64` instead of `amd64` on ARM machines.
Verify the package's SHA-256 against its entry in the checksums file before installing.

```sh
sudo apt install ./infercat_<v>_linux_amd64.deb   # Debian / Ubuntu
sudo rpm -i ./infercat_<v>_linux_amd64.rpm       # Fedora / RHEL family
```

Use `sudo rpm -U ./infercat_<v>_linux_amd64.rpm` for an RPM upgrade. On Debian/Ubuntu, repeat
`apt install` with the new package. After upgrading, run `systemctl --user daemon-reload`
and `systemctl --user restart infercat` for each running user's service.

## Start as your user

Start your inference engine first. Run these commands as the user who will host the model,
**without sudo**:

```sh
systemctl --user daemon-reload
systemctl --user enable --now infercat
journalctl --user -u infercat -n 30 --no-pager
infercat status
infercat keys add alice
```

The unit at `/usr/lib/systemd/user/infercat.service` runs `infercat serve`, detects a local
engine, and restarts on failure. It uses settings remembered by an earlier `infercat serve`
under the same user. For a different engine URL, stop the unit, run
`infercat serve --upstream http://127.0.0.1:8080`, stop that foreground host with Ctrl-C,
and start the unit again. Never run both hosts against the same data directory.

`systemctl --user disable --now infercat` stops the service and disables automatic startup.
A user service normally follows the login session; to keep it running after logout, an
administrator can enable lingering with `sudo loginctl enable-linger USERNAME`.

## Data and console

Data belongs to the hosting user: `$XDG_CONFIG_HOME/infercat`, or `~/.config/infercat` when
that variable is unset. The service and CLI must use the same configuration directory.
Back up that entire directory: it contains the host identity, keys, settings and usage.
Package removal leaves this user data intact; keep it and backups private.

The console listens on `127.0.0.1:9101`. Run `infercat console` on the host to open it; it
is not exposed on the server's public network interface. For a headless server, forward
that loopback port through SSH and use the URL from `infercat console --print` privately.
To use the web app's console remotely, enable remote access in the local console and keep
the resulting admin code separate from friends' invites: it can manage the host.
