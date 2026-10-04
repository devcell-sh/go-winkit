# go-winkit

A disposable Windows machine with a Linux shell, from one YAML file, in
minutes, on your Mac or Linux box.

winkit builds Windows VM images without anyone clicking through an
installer. It fetches the media, writes the answer file, drives the install
in QEMU, runs your scripts, and hands back a disk image you can boot or
throw away. It is the Windows layer behind
[devcell](https://github.com/DimmKirr/devcell), and it works on its own.

## Try it

```sh
brew install qemu wimlib      # Docker Desktop running for the rootfs build
task install                  # builds ./bin/winkit and copies it to ~/.local/bin

winkit build --file examples/pe-wsl1-alpine winkit-core.qcow2
winkit start winkit-core.qcow2
winkit ssh winkit-core -- 'wsl -d winkit --exec cat /etc/alpine-release'
winkit stop winkit-core
```

The first build downloads Windows media and builds an Alpine rootfs with
Docker, so it takes a while. After that everything is cached and `start`
is the only wait. `start` holds the terminal until the guest answers SSH,
then prints how to reach it.

## Who this is for

- You develop on Apple Silicon and need a Windows box that appears and
  disappears on command. Native ARM64, HVF or Virtualization.framework,
  no Parallels license.
- You run CI that touches Windows drivers, installers, or WIM files and
  do not want to babysit a licensed VM. Build the image once and boot it
  for each job.
- You maintain a dev environment tool and want Windows as a target without
  owning the install pipeline. Use the Go API; devcell does.

If you want a Windows desktop to use by hand, UTM or dockur/windows is a
better fit. winkit is for the unattended path.

## Three ways to build

| Mode | `winkit.yaml` | What you get | Disk |
|---|---|---|---|
| Core | `pe: true` + `wsl:` | WinPE boot volume with WSL1 Alpine or Nix, SSH, drvfs to Windows paths. Fresh on every boot, state on a data disk. | ~4 GB |
| PE | `pe: true` | WinPE alone, with gosshd as the shell. For driver tests and media validation. | ~4 GB |
| Full | neither | A real Windows 11 install with OpenSSH and RDP, optionally with WSL1. For things that need a desktop or Windows services. | ~64 GB |

Output is qcow2 by default, or `--image-type utm` for UTM and `box` for a
Vagrant box with a `vagrant-qemu` Vagrantfile.

Why WSL1 and not WSL2: WSL1 is a syscall layer, not a VM. It needs no
nested virtualization, shares the Windows network stack, and reads host
files through drvfs. That is what makes a Linux shell inside WinPE under
QEMU possible at all. Microsoft does not support this combination. winkit
ships the runtime and has end to end tests that boot it.

## The config

```yaml
from: windows/11-pro-arm64
pe: true
wsl:
  image: alpine          # or nix, any docker ref, a .wsl URL, a local tarball
  services: ./s6         # s6 service dirs supervised inside the distro
commands:
  boot:
    - powershell C:/scripts/tools.ps1
hostname: winkit
ports:
  gossh: 20022
  rdp: 23389
```

`winkit init` writes a fully commented template. Hooks run per Windows
setup pass (`specialize`, `oobe`, `boot`, `wsl`) with per command timeout,
retries, and reboot control.

## Examples

Each directory is a complete `winkit.yaml` plus a README. They are
load tested in CI so they cannot drift from the schema.

- [`pe-wsl1-alpine`](examples/pe-wsl1-alpine): the Core mode above.
- [`pe-wsl1-nix`](examples/pe-wsl1-nix): Core with a Nix home-manager rootfs; point `WINKIT_NIXHOME` at your own flake.
- [`pe-wsl1-nix-home-manager`](examples/pe-wsl1-nix-home-manager): Core with a Linux desktop (IceWM, Xvfb, x11vnc) composed from the devcell-sh/home modules and supervised by s6.
- [`pe-minimal`](examples/pe-minimal): WinPE alone.
- [`full-wsl1-alpine`](examples/full-wsl1-alpine): full install with WSL1, s6 services, and post install commands.
- [`library-consumer`](examples/library-consumer): the Go API, `winkit.Build` then `winkit.Start`.

## Beyond build and start

`winkit status`, `logs`, and `stop` manage running VMs; `winkit cp` pushes
a file into one (`winkit cp tool.exe E:\apps\tool.exe`). `winkit export`
exposes the pieces on their own: render and validate `autounattend.xml`,
build and inspect bootable ISOs, inject a payload into `boot.wim`, or
assemble a standalone WinPE ISO. `winkit debug fetch` pre-caches media
for CI, `debug diag` reads Windows Setup logs off the guest, and
`debug features` shows what a WIM has baked in. `--debug` traces every
file, WIM, and download operation.

## As a library

```sh
go get github.com/devcell-sh/go-winkit
```

`build` is the entry point: one call from install media to a disk image.
`vm` boots it, with `vm/qemu` and `vm/vz` backends. `media/*` fetches
Windows builds from the Microsoft Update Catalog and UUP dump plus the
virtio-win drivers. `winpe` prepares and provisions the image, `wsl` turns
Docker images into WSL rootfs tarballs, `unattend` renders answer files,
and `gosshd` is a small SSH server cross compiled into guests that have no
OpenSSH. What winkit owns and what it leaves to consumers is written down
in [ARCHITECTURE.md](ARCHITECTURE.md).

There is no v1 tag yet. Pin a version if you depend on it.

## Limits

- QEMU on Linux (KVM) and macOS (HVF), plus Virtualization.framework on
  macOS 13+. No Hyper-V.
- Windows media licensing is yours. winkit downloads public media from
  Microsoft and does not activate anything.
- WIM modifying commands need a build with libwim linked, which is what
  `task install` does.

## Testing

Tiers by what they need. Only the first runs anywhere.

| Tier | Command | Needs |
| --- | --- | --- |
| Unit | `task test` | nothing |
| WIM | `go test -tags wimlib ./winpe/` | libwim |
| Real media | `go test ./winpe/ ./test/e2e/` | seeded cache |
| Boot | `task test:integration` | seeded cache, libwim, QEMU |

Tiers past the first read install media from a cache, so a boot test
reruns in minutes instead of repaying a multi gigabyte download. Seed it
once with `task test:seed`; `task test:seed:where` prints the paths and
what is missing. Unseeded tests skip and name the path they wanted. The
cache lives under the user cache dir (`~/.cache/winkit` on Linux);
`WINKIT_CACHE_DIR` overrides it for both the CLI and the tests.
