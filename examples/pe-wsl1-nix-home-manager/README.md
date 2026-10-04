# WinPE + WSL1 + home-manager from devcell-sh/home

A disposable Windows machine with a Linux desktop. WinPE boots, WSL1 runs
a Nix rootfs, and the home-manager configuration comes from
[devcell-sh/home](https://github.com/devcell-sh/home): the same `desktop`
module devcell uses, so you get IceWM, Xvfb, x11vnc, fonts and the tray
plumbing without writing any of it here.

```sh
winkit build --file examples/pe-wsl1-nix-home-manager winkit-core.qcow2
winkit start winkit-core.qcow2
winkit ssh winkit-core                      # Windows side, gosshd
open vnc://127.0.0.1:25900                  # the IceWM desktop
```

Docker must be running for the rootfs build. The first build also
evaluates the home flake and pulls the desktop closure, so expect it to
take a while and the data disk to be larger than the Alpine example.

## Boot order

```
winpeshl.ini
  winkit-service init                      PE supervisor, stays foreground
    wpeinit, drivers, gosshd, winkit user
    bootstrap-wsl1.ps1 (SYSTEM)
      import distro.wsl, probe it
    winkit-service run winkit-s6 (as user winkit, respawned by init)
      wsl.exe -d winkit -u root -e /bin/s6-init
        s6-svscan /etc/s6/services
            sshd            built in, port 2223 inside the guest
            xvfb            X display :99
            dbus-session    session bus for tray and GTK apps
            window-manager  icewm-session
            snixembed       SNI to XEmbed tray proxy
            x11vnc          VNC on guest port 5900, forwarded to host 25900
```

Everything under s6 is a plain service dir in `./s6/<name>/run`. Add a
directory and it is supervised on the next build; a directory named like
a built-in (`sshd`) replaces it.

## Ports

`ports.forward: ["25900:5900"]` in `winkit.yaml` adds a QEMU forward from
host 25900 to guest 5900. WSL1 shares the Windows network stack, so the
x11vnc listener inside the distro is what that forward reaches. The
QEMU console VNC on host 5900 still shows the WinPE text screen; the
desktop is on 25900. `winkit start --forward host:guest` adds more.

## Logs

Every s6 service writes to stdout. s6-svscan runs under winkit-service
with `--log-serial`, which wraps each line as a JSON event tagged with
the service name and sends it over the serial port, so `winkit logs`
shows them next to the host events. Keep services quiet: x11vnc runs
with `-q` for that reason.

## The flake

`nixhome/flake.nix` takes `github:devcell-sh/home` as an input and
composes `homeConfigurations.winkit` from its modules, following home's
own nixpkgs and home-manager pins. winkit requires that attribute name:
the PE distro user is `winkit` with home `/home/winkit`, and Home Manager
refuses to activate when the user or home directory disagree. That is
why `github:devcell-sh/home` cannot be passed directly as `nixhome`: its
outputs are named after devcell stacks and built for user `devcell`.

To pull in more of home, add modules to the list in the flake. Modules
that need `pkgsUnstable`, `pkgsEdge` or `mcp-nixos` (mise, llm) also need
those passed through `extraSpecialArgs`, as home's own `mkHome` does.

## Verify

```sh
winkit ssh winkit-core -- 'wsl -d winkit --exec s6-svstat /etc/s6/services/window-manager'
winkit ssh winkit-core -- 'wsl -d winkit --exec sh -lc "DISPLAY=:99 xdotool getdisplaygeometry"'
```

Service logs go to the serial console that `winkit logs` reads, and the
Windows side of the service writes `E:\winkit\winkit-s6.log`.

## Status

Boot-tested on QEMU/HVF (ARM64 WinPE): all six services come up under s6,
IceWM is reachable over VNC, fonts resolve through home-manager's
fontconfig, and the session bus answers. Things learned on the way that
are now baked into the templates: glibc needs `/etc/nsswitch.conf` or
`s6-setuidgid` fails group lookups; sshd needs its privilege separation
user; nixpkgs dbus wants an explicit `--config-file`; WSL1 under WinPE
writes a `resolv.conf` with no nameservers, so `s6-init` fills it from the
DNS list the bootstrap exports to the data disk.

## Windows apps on the PE console

ARM64 Win32 binaries run from either shell and draw on the PE console
(QEMU display, host port 5900), not on the IceWM display. Put them on the
data disk, which survives reboots, and run them by path:

```sh
winkit cp putty.exe E:\apps\putty.exe       # from the host
winkit ssh winkit-pe-wsl -- E:\apps\putty.exe
/mnt/e/apps/putty.exe                        # from the WSL shell, via interop
```

No x64 or x86 emulation exists in ARM64 WinPE, and there is no Windows
Installer service: use portable builds, or installers with silent flags
(`7z2501-arm64.exe /S /D=E:\apps\7zip` works). init grants the winkit
account access to WinSta0 and the Default desktop; without that ACE,
windows created by processes it spawns never paint.
