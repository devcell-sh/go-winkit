# WinPE with WSL1 Alpine

This is a standard winkit build. The library assembles the PE boot volume,
writable data disk, packaged WSL1 runtime, guest PowerShell provisioner, and
Alpine rootfs. The CLI only loads this directory's `winkit.yaml` and delegates
to the library.

```sh
winkit build --file examples/pe-wsl1-alpine winkit-core.qcow2
winkit start winkit-core.qcow2
ssh -p 20022 admin@127.0.0.1
```

Inside the guest, verify the imported distro and Alpine release through the
ordinary command shim:

```bat
wsl -l -v
wsl -d winkit --exec /bin/cat /etc/alpine-release
```

The build writes `winkit-core.qcow2.winkit.json`. `winkit start` reads that
manifest and attaches both the PE boot volume and `winkit-core-data.qcow2`.
