# pe-minimal — the WinPE path

Builds the smallest winkit artifact: a WinPE boot volume (~4GB FAT qcow2) with gosshd and PowerShell 7 injected. No full
Windows install runs — the volume is assembled offline and boots straight into WinPE.

## PE vs full install

|             | PE (`pe: true`)                      | Full install                                  |
|-------------|--------------------------------------|-----------------------------------------------|
| Build time  | minutes (no VM)                      | hours (unattended Setup)                      |
| Output      | ~4GB FAT volume, ephemeral           | ~64GB qcow2, persistent OS                    |
| `features:` | baked offline into the WIM           | applied in the running OS                     |
| `commands:` | `boot` phase only (flat list = boot) | all phases (`specialize`/`oobe`/`boot`/`wsl`) |
| WSL         | impossible (WinPE can't run it)      | `wsl:` block                                  |

## Run it

```sh
cd examples/pe-minimal
winkit build
winkit start winkit-core.qcow2
ssh -p 20022 session@127.0.0.1 # gosshd inside WinPE

```

Use cases: WIM servicing experiments, driver-injection testing, CI smoke images — anywhere you need "Windows-ish and
bootable in minutes"
rather than a real installation.
