# Architecture and boundaries

winkit builds Windows VM images from code. One sentence of contract:
**winkit owns the verbs, consumers own the nouns.**

- Verbs (winkit): fetch install media, service the WIM, run the
  unattended install, import a WSL rootfs, execute hooks, package the
  image, control the QEMU guest (QMP, serial, stall detection,
  screenshots).
- Nouns (consumer): which rootfs image, which s6 services, which
  packages, which hook scripts. Supplied via `winkit.yaml` (CLI) or
  `buildopts.BuildOpts` (library).

## What winkit will never own

- **Nix / home-manager content.** A nix-flavored WSL rootfs is consumer
  content: build it outside, pass it as `wsl.image`, activate it with
  `wsl`-phase hooks. (devcell does exactly this.)
- **Runtime session management.** `winkit start/stop/status` is a thin
  debug CLI over the public `winkit.Start`/`Status` API — proof the API
  is sufficient. Cells, sessions, restart policy, project sync belong to
  the consumer (devcell's engine).
- **Linux guests.** winkit is a Windows kit. The QEMU control
  primitives in `vm/qemu` are kept separable (generic QMP/serial/stall
  vs Windows install driver) so a generic layer could be extracted, but
  winkit itself never grows Linux guest knowledge.

## Public API surface

Consumers embed the root package:

| Entry point | What it does |
|---|---|
| `winkit.Build(ctx, build.Config)` | Full build, dispatching PE / WSL / base on `Config.Opts` |
| `winkit.Start(ctx, StartOpts)` / `winkit.Status(dir)` | Boot and track a built image (`vm.VM` handle) |
| `build/buildopts` | The consumer-facing nouns: `From`, `Features`, `Hooks` (specialize/oobe/boot/wsl), `WSLConfig`, `Ports` |
| `vm/qemu.Monitor` + `ScreenStatePoller` | Observability loop: stall/poll callbacks instead of log parsing |
| `s6` | Service-dir model for `wsl.services` |

Everything under `internal/` is not API. The `vm`, `vm/qemu`,
`vm/vmstate`, `winpe`, `unattend`, `media/*`, `wsl`, `s6`, `gosshd`,
`sftpshare`, `build` packages are importable; treat exported symbols as
semver-governed from the next tagged release. Consumers pinning
pseudo-versions (devcell) should move to tags.

## Layering

```
consumer content (rootfs image, hook scripts, s6 service dirs)
        │  winkit.yaml / BuildOpts
        ▼
winkit: media → unattend/WIM → QEMU install → hooks over gosshd → package
        ▼
qcow2 / UTM / Vagrant box            (winkit start | vagrant up | devcell engine)
```

Examples of both consumer surfaces live in `examples/`:
`full-wsl1-alpine` (YAML/CLI), `library-consumer` (Go API), `pe-minimal`
(WinPE path).
