# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

winkit builds disposable Windows VM images from a YAML config, unattended:
fetches media, drives the install in QEMU, provisions over SSH, and hands
back a bootable qcow2. It is the Windows layer behind
[devcell](https://github.com/DimmKirr/devcell).

Three build modes: **PE** (WinPE ramdisk, ~4 GB, fresh each boot), **PE+WSL1**
(WinPE with a Linux distro via WSL1), and **Full** (real Windows 11 install,
~64 GB). WSL1 is used instead of WSL2 because it needs no nested
virtualization and shares the Windows network stack.

## Build and test

```sh
task build             # cross-compiles gosshd + winkit-service + implorer for Windows,
                       # then builds the host CLI → ./bin/winkit
task install           # build + copy to ~/.local/bin/winkit
task test              # unit tests (excludes test/e2e)
go test ./gosshd/...   # single package
go test -run TestFoo ./build/   # single test
```

wimlib-dependent features (WIM patching, full installs) need CGO and libwim:
```sh
# macOS: brew install wimlib
CGO_ENABLED=1 go test -tags wimlib ./winpe/...
```

Static linking (vendored libwim.a from go-wimlib):
```sh
WINKIT_LINK=static task build
```

### Integration / E2E tests

These download several GB and boot real VMs. Seed the cache first:
```sh
task test:seed                          # download Windows + virtio ISOs
task test:integration                   # boot WinPE in QEMU (tcg, ~40 min)
task test:examples:pe-wsl1-alpine       # full PE+WSL1 build+boot+verify
WINKIT_E2E=1 task test:examples         # all examples including multi-hour full install
task test:wsl                           # full Windows install with WSL1 + SFTP
```

Keep the VM alive after a test for manual debugging:
```sh
WINKIT_E2E_TEARDOWN=false task test:examples:pe-wsl1-alpine
```

## Architecture

### Package map

| Package | Role |
|---------|------|
| `start.go` | Library entry point: `winkit.Start()` boots an image, registers state |
| `build/` | Orchestrates full image builds: `build.PE()` for PE, `build.Build()` for full |
| `build/buildopts/` | `winkit.yaml` parsed into `BuildOpts` |
| `internal/config/` | YAML config parsing (`winkit.yaml`) |
| `internal/cli/` | Cobra command tree (thin wrappers over library calls) |
| `winpe/` | WinPE image assembly: boot.wim staging, payload injection, WIM patching, structured logging, WSL1 engine fetch |
| `winpe/embedded/` | `//go:embed`ed prebuilt Windows binaries (gosshd, winkit-service, implorer) |
| `vm/` | Backend-agnostic VM interface (`VMBackend`, `VM`) |
| `vm/qemu/` | QEMU backend: command builder, runner, QMP, screen capture, serial watch |
| `vm/vmstate/` | Running VM state tracking (`~/.winkit/run/<name>.json`) |
| `gosshd/` | Custom SSH server for WinPE guests (Win32-OpenSSH cannot serve sessions in PE) |
| `wsl/` | WSL1 distro builder: Docker-based rootfs (alpine, nix, any ref), s6 service injection |
| `implorer/` | Custom Windows desktop shell (Wails v3 + Svelte 5), separate `go.mod` |
| `cmd/winkit/` | CLI main |
| `cmd/winkit-service/` | Guest-side PID-1 supervisor (`init` verb), file receiver (`recv`), process launcher (`run`/`run-user`) |
| `unattend/` | autounattend.xml generation |
| `media/` | ISO/WIM/FAT media handling |

### Guest boot sequence (PE+WSL1)

`winpeshl.ini` starts `winkit-service.exe init --config X:\winkit\init.json`:

1. **wpeinit** + driver loading (SYSTEM context)
2. Firewall disabled
3. **Admin user created** (`ensureLocalUser`), granted WinSta0 desktop access
4. **pe-agent** started as the admin user (structured logging)
5. **gosshd** started as the admin user (SSH server on `:2222`)
6. **WSL1 bootstrap** (SYSTEM): catalog registration, kernel services, native Go `runBootstrapWSL1()` in `cmd/winkit-service/bootstrap_wsl1.go`
7. **s6-svscan** supervised inside the WSL1 distro (respawned on exit)

### Embedded binaries flow

`task build` cross-compiles three Windows binaries into `winpe/embedded/`:
- `gosshd_windows_{arch}.exe`
- `winkit-service_windows_{arch}.exe`
- `implorer_windows_{arch}.exe` (from the separate `implorer/` module via `task implorer:build`)

These are `//go:embed`ed into the winkit CLI binary. At image build time,
`CrossCompileGosshd`/`CrossCompileService`/`CrossCompileImplorer` extract them
and `BuildBaseImageFiles` writes them to the `X:\winkit\` payload directory on
the FAT boot volume.

### Structured logging

Guest scripts stream JSON events over a virtio-serial port (COM2). QEMU
writes this to `build.jsonl` (during builds) or `guest.jsonl` (during runs).
The `GuestEvent` type in `winpe/structlog.go` defines the wire format. Host-side
events go to `run.jsonl`.

## Debugging a running VM

### Start a VM
```sh
winkit build --file examples/pe-wsl1-nix-home-manager winkit-pe-wsl.qcow2
winkit start winkit-pe-wsl.qcow2
winkit status       # shows NAME, STATUS, PID, SSH/RDP/VNC ports, UPTIME
```

### How to tell the VM is controllable

Check if gosshd is reachable and bootstrap completed:
```sh
# from the host:
winkit ssh <name> -c "type X:\winkit\wsl1-bootstrap.ok"

# from within a container (devcell), use the gosshd Go client:
client, _ := gosshd.Dial(ctx, "host.docker.internal:20022")
stdout, _, code, _ := client.Run(ctx, `type X:\winkit\wsl1-bootstrap.ok`)
# code == 0 && stdout contains "WSL1_BOOTSTRAP_OK" means ready
```

You can also read the guest log directly (it's on a bind-mounted path):
```sh
grep 'wsl1-bootstrap-ok' <image-dir>/.winkit/run/<name>/guest.jsonl
```

### Connect via gosshd (the provisioning SSH)

gosshd listens on guest port 2222, forwarded to host port 20022 (default).
Credentials: `admin` / `admin` (`gosshd.DefaultUser` / `gosshd.DefaultPassword`).
It runs cmd.exe sessions. PowerShell is at `X:\winkit\pwsh\pwsh.exe`.

```sh
# from the host:
winkit ssh <name>
ssh -o StrictHostKeyChecking=no -p 20022 admin@127.0.0.1

# from within a container (devcell), use host.docker.internal:
ssh -o StrictHostKeyChecking=no -p 20022 admin@host.docker.internal
# or programmatically:
client, _ := gosshd.Dial(ctx, "host.docker.internal:20022")
```

### Copy files to a running VM
```sh
winkit cp ./tool.exe <name>:E:\apps\tool.exe
```
Uses `winkit-service recv` on the guest (pipes stdin through gosshd).

### View logs

Log files live in `<image-dir>/.winkit/run/<name>/`:
- `guest.jsonl`: structured JSONL from the guest (pe-agent, winkit-service, scripts)
- `serial.log`: QEMU serial console output (firmware, Windows boot messages)
- `run.jsonl`: host-side structured events (winkit start/stop lifecycle)

```sh
# from the host:
winkit logs <name>                   # guest.jsonl
winkit logs <name> -s serial         # serial.log
winkit logs <name> -f                # follow

# from a container, read the files directly:
cat <image-dir>/.winkit/run/<name>/guest.jsonl
```

During builds, the guest log is `build.jsonl` in the work directory.

### Guest filesystem layout
- `X:\winkit\`: payload directory (gosshd.exe, winkit-service.exe, implorer.exe, pwsh/, init.json, scripts)
- `X:\`: WinPE ramdisk (boot volume, FAT)
- `E:\`: writable NTFS data disk (WSL distro, pagefile, user profile)

### WSL commands through gosshd

Nix-based distros have no `/bin/` utilities. Use full nix store paths:
```sh
wsl -d winkit -u root -e /nix/var/nix/profiles/default/bin/uname -a
wsl --list --verbose
```

### WSL1 distro sshd (OpenSSH inside the distro)

The s6-supervised OpenSSH sshd inside the WSL1 distro listens on
**guest 127.0.0.1:2223** (localhost only). It provides real PTY sessions
with the nix environment, unlike gosshd which gives cmd.exe shells.

**Connectivity:** The sshd is not exposed to the host by default. To reach
it from outside the guest, add a port forward in `winkit.yaml`:
```yaml
ports:
  forward: ["22223:2223"]
```
Then connect: `ssh -p 22223 root@127.0.0.1` (or `host.docker.internal`
from a container).

Without a port forward, you can reach it from inside the guest by chaining
through gosshd:
```sh
# via wsl exec (through gosshd's cmd.exe shell):
wsl -d winkit -u root -e /nix/var/nix/profiles/default/bin/ssh \
  -o StrictHostKeyChecking=no -o BatchMode=yes \
  -p 2223 root@127.0.0.1 /nix/var/nix/profiles/default/bin/uname -a
```

**SCP/SFTP:** The sshd has `Subsystem sftp internal-sftp` enabled. SCP
works in both directions:
```sh
# upload to the distro:
wsl -d winkit -u root -e /nix/var/nix/profiles/default/bin/scp \
  -o StrictHostKeyChecking=no -o BatchMode=yes \
  -P 2223 /tmp/file.txt root@127.0.0.1:/tmp/file.txt

# download from the distro:
wsl -d winkit -u root -e /nix/var/nix/profiles/default/bin/scp \
  -o StrictHostKeyChecking=no -o BatchMode=yes \
  -P 2223 root@127.0.0.1:/tmp/file.txt /tmp/local-copy.txt
```

With a port forward configured, SCP/SFTP work directly from the host or
container using standard `scp -P 22223`.

**Auth:** Password auth and pubkey auth are both enabled. Root's shell is
the full nix bash; the `winkit` user's shell is `/bin/login-bash` which
loads the nix profile. Remote commands via SSH need full nix paths unless
the login profile adds them to PATH.

**Key detail:** gosshd does NOT support `direct-tcpip` (SSH tunneling), so
you cannot `ssh -J` or `ssh -L` through gosshd to reach the WSL1 sshd.
Use `wsl -e` from inside the guest or configure a port forward.

## Port mapping (defaults)

| Host port | Guest port | Service |
|-----------|------------|---------|
| 20022 | 2222 | gosshd (provisioning SSH, cmd.exe shell) |
| 20122 | 22 | Windows OpenSSH (full installs only) |
| 22223 | 2223 | WSL1 distro sshd (if configured in ports.forward) |
| 23389 | 3389 | RDP |
| 5900 | - | QEMU VNC display |
| 25900 | 5900 | x11vnc inside WSL1 (if configured in ports.forward) |

## Key conventions

- The `implorer/` directory is a **separate Go module** (Wails v3 dependency). It cannot be built with `go build` from the main module; use `task implorer:build`.
- gosshd exists because Win32-OpenSSH cannot serve sessions in WinPE (no user logon subsystem).
- `winkit-service` is the guest-side PID-1 supervisor. Its `init` verb drives the entire PE boot sequence. The `recv` verb receives files piped through gosshd stdin. The `run` verb wraps child processes with structured logging.
- Tests that need real Windows media (`_test.go` files with `//go:build integration`) are excluded from `task test`. Seed media with `task test:seed` first.
- Build artifacts land in `test/results/<timestamp>-<TestName>/`.
- PowerShell templates (`*.ps1.tmpl`) are in `unattend/` and rendered at build time. Lint them with `task test:powershell:lint`.
