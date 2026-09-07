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
#   Supported images: alpine, ubuntu, debian, or a path to a .wsl tarball.
#   Cannot be combined with pe.

# pe: true

# wsl:
#   image: alpine

# Windows features to enable. Applied via DISM (PE mode) or
# Add-WindowsCapability (full install).
# features:
#   - OpenSSH.Server
#   - Containers
#   - Microsoft-Hyper-V-All

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
#     - wsl -d alpine -- apk add curl git

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
