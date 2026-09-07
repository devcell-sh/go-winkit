# go-winkit

Go packages for building Windows environments from code: get install media, turn it into something bootable, and talk to the machine that comes up. Windows tooling mostly assumes a human clicking through an installer; winkit is for when the whole path has to run unattended.

The packages follow that path. `media/uupdump`, `media/mctcatalog`, and `media/virtio` download install media (Windows builds from UUP dump and the Microsoft Update Catalog, plus the virtio-win driver ISO), and `media/isokit` builds and inspects the bootable ISOs they assemble. `winpe` prepares the image and drives the WinPE provisioning pass that applies it. `wsl` builds WSL rootfs tarballs from Docker images so the guest gets a Linux userland. `build` is the library entry point that ties those together, one call from install media to a ready disk image. `vm` (with `vm/qemu` and `vm/vz` backends) boots the result. Standalone utilities: `gosshd` is a small SSH server meant to be cross-compiled into guests that ship without OpenSSH (WinPE being the main offender), `unattend` renders answer files, and `cache` is the shared media cache.

winkit is the Windows layer behind [devcell](https://github.com/DimmKirr/devcell), and the API follows what devcell needs. There is no v1 yet, so pin a version if you depend on it.

```sh
go get github.com/devcell-sh/go-winkit
```

## Testing

Tests come in tiers by what they need. Only the first runs anywhere:

| Tier | Command | Needs |
| --- | --- | --- |
| Unit | `task test` | nothing |
| WIM | `go test -tags wimlib ./winpe/` | libwim |
| Real media | `go test ./winpe/ ./test/e2e/` | seeded cache |
| Boot | `task test:integration` | seeded cache, libwim, QEMU |

Tiers past the first read their install media from a cache rather than
building it, so a boot test is re-runnable in minutes instead of re-paying
a multi-gigabyte download. Seed it once:

```sh
task test:seed          # fetch + assemble the Windows and virtio-win ISOs
task test:seed:where    # print the paths, and what is missing
```

Seeding goes through `winkit fetch`, so it doubles as a test of the fetch
path. Both fetches are idempotent: a cached ISO that still validates is
left alone. Tests skip with the path they wanted when the cache is empty,
so an unseeded checkout reports what to run rather than failing.

The cache lives under the user cache dir (`~/.cache/winkit` on Linux).
`WINKIT_CACHE_DIR` overrides it for both `winkit fetch` and the tests, which
resolve the location through the same code and so cannot drift apart.
