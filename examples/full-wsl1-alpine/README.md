# wsl-alpine — winkit as a standalone consumer

Builds a Windows 11 ARM64 image with an Alpine WSL distro, one custom
supervised service, and one post-deployment package. No nix, no
devcell — this is the contract: **winkit does the verbs (fetch,
install, import, execute, package); you supply the nouns (which image,
which services, which packages).**

## What it exercises

| Surface | Here |
|---|---|
| Opaque `wsl.image` | `alpine` docker ref → rootfs tarball → imported as distro `winkit` |
| `wsl.services` | `./services/myagent` s6 dir → supervised under `/etc/s6/services` |
| `commands.wsl` | `apk add htop` over gosshd after import |

## Run it

```sh
cd examples/wsl-alpine
winkit build            # ~1-2h on TCG; fetches media on first run
winkit start
ssh -p 20022 session@127.0.0.1
```

## Verify

Inside the guest (or over that SSH session):

```powershell
wsl -d winkit -- s6-svstat /etc/s6/services/myagent   # up (…)
wsl -d winkit -- cat /tmp/myagent-heartbeat           # recent timestamp
wsl -d winkit -- htop --version
```

## Make it yours

- Different distro: `image: ubuntu`, a tarball URL, or `./rootfs.wsl`.
- More services: one subdirectory per service under `services/`, each
  with an executable `run` starting with `#!`. A service named like a
  built-in (`sshd`) overrides it.
- Heavier provisioning: prefer baking into the image (Dockerfile) and
  keep `commands.wsl` for the last mile — same trade-off as Docker
  `RUN` vs post-start scripts. Per-command `timeout`, `retries`,
  `valid-exit-codes`, `reboot` are available (see `winkit init`'s
  annotated config).
