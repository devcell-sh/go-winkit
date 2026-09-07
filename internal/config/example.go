package config

const Example = `# winkit.yaml — Windows image build configuration
#
# Run "winkit build" in the same directory as this file, or pass
# "winkit build -f path/to/winkit.yaml" explicitly.

# Media source. Determines where the Windows installer comes from.
#   windows/<edition>-<arch>  — fetch from Microsoft (MCT/UUP dump)
#   ./path/to/file.iso        — use a local ISO
#   ./path/to/install.wim     — use a local WIM
# Default: windows/11-pro-arm64
from: windows/11-pro-arm64

# Build mode (pick one, or omit both for a full disk install):
#
# pe: true
#   WinPE boot volume (~4 GB, ephemeral). Features and files are baked
#   into the WIM offline. Only "boot" phase commands are available.
#
# wsl:
#   image: alpine
#   Full disk install with WSL enabled and a distro imported.
#   Cannot be combined with pe. Supported images:
#     alpine  — docker-built Alpine with WSL plumbing (default)
#     nix     — docker-built Nix + home-manager environment
#     <docker ref> — any docker image (ubuntu:24.04, ghcr.io/org/img:tag),
#       docker-built with universal WSL plumbing (best-effort per distro)
#     https://…/name.wsl — a published WSL image, downloaded and cached
#       (e.g. https://cdimages.ubuntu.com/ubuntu-wsl/noble/daily-live/current/noble-wsl-arm64.wsl);
#       the publisher owns the in-distro setup (default user, wsl.conf)
#     ./name.wsl — a local rootfs tarball, shipped verbatim

# pe: true

# wsl:
#   image: alpine
#   # Directory of s6 service dirs, copied verbatim into /etc/s6/services
#   # of the distro and supervised by the boot-time s6-svscan task.
#   # Layout: <dir>/<name>/run (plus optional finish and data files),
#   # i.e. a standard s6 scan dir. A subdir named like a built-in
#   # service (sshd) overrides it. Docker-built images only.
#   services: ./s6

# Windows features to enable. Applied via DISM (PE mode) or
# Add-WindowsCapability (full install).
# features:
#   - OpenSSH.Server
#   - Containers

# Files to inject into the image. In PE mode they are written into the
# WIM at build time; in full-install mode they are uploaded over SSH
# after the OS boots.
# files:
#   - src: ./tools.ps1
#     dest: C:/scripts/tools.ps1
#   - src: ./config.json
#     dest: C:/ProgramData/myapp/config.json

# Commands to run at each build phase. If you only need post-boot
# commands, use a flat list (they default to the "boot" phase):
#
#   commands:
#     - choco install git -y
#     - powershell C:/scripts/setup.ps1
#
# For multi-phase control, use phase keys:
#
# commands:
#   specialize:
#     # Runs during Windows setup in SYSTEM context.
#     # Injected into unattend.xml <specialize> pass.
#     - reg add HKLM\SOFTWARE\MyApp /v Key /d Value /f
#
#   oobe:
#     # Runs at first logon, elevated.
#     # Injected into unattend.xml <oobeSystem> pass.
#     - powershell -Command "Set-ExecutionPolicy Bypass"
#
#   boot:
#     # Runs after the OS is up and SSH is reachable.
#     # Executed over gosshd from the host.
#     - powershell C:/scripts/tools.ps1
#     - choco install git -y
#
#     # Per-command options (all optional):
#     # - cmd: "powershell C:/scripts/long-task.ps1"
#     #   timeout: 10m        # default: 5m
#     #   retries: 2          # default: 0
#     #   valid-exit-codes:    # default: [0]
#     #     - 0
#     #     - 3010
#     #   reboot: true         # default: false
#
#   wsl:
#     # Runs after WSL is enabled and the distro is imported.
#     # Requires "wsl:" to be set above.
#     - wsl -d winkit -- apk add curl git

# Directory-based hooks (optional, alternative to inline commands).
# Place scripts in hooks/<phase>/ next to this file:
#
#   hooks/
#     specialize/
#       01_enable_containers.ps1
#     boot/
#       01_install_tools.ps1
#       02_configure_network.ps1
#     wsl/
#       01_setup_packages.sh
#
# Scripts run in lexicographic order, after any inline commands.
`
