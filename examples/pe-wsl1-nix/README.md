# WinPE with WSL1 Nix

Same PE+WSL1 pipeline as `pe-wsl1-alpine`, but the rootfs is a
Nix/home-manager distro built via Docker. Requires Docker for the
rootfs build step.

```sh
winkit build --file examples/pe-wsl1-nix winkit-core.qcow2
winkit start winkit-core.qcow2
ssh -p 20022 admin@127.0.0.1
```

Inside the guest, verify nix is available:

```bat
wsl -d winkit --exec nix --version
```

## Nixhome

The home-manager configuration baked into the rootfs defaults to the
embedded flake at `wsl/templates/nixhome/` (bash, git, coreutils,
ripgrep, jq). To use your own:

```sh
# Local directory containing flake.nix + home.nix:
WINKIT_NIXHOME=./my-nixhome winkit build --file examples/pe-wsl1-nix out.qcow2

# Remote flake ref:
WINKIT_NIXHOME=github:org/home winkit build --file examples/pe-wsl1-nix out.qcow2
```

Library consumers set `build.Config.NixHome` directly.
