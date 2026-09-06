/*
 * OFD lock shim for WSL1.
 *
 * WSL1's lxcore.sys does not implement F_OFD_SETLK (fcntl 37/38/36).
 * Nix's C SQLite (3.49+) uses OFD locks when compiled on Linux, and
 * fails with EINVAL → "unable to open database file" on any nix-env,
 * nix build, or nix profile command inside a running WSL1 distro.
 *
 * This LD_PRELOAD shim intercepts fcntl() and downgrades OFD lock
 * requests to classic POSIX locks (F_SETLK/F_SETLKW/F_GETLK), which
 * WSL1 does support. The semantic difference (OFD locks are per-open-
 * file-description, POSIX locks are per-process) is irrelevant for
 * single-process nix commands.
 *
 * Build:  gcc -shared -fPIC -o ofd-shim.so ofd-shim.c -ldl
 * Use:    LD_PRELOAD=/usr/lib/ofd-shim.so nix-env -iA nixpkgs.foo
 */
#define _GNU_SOURCE
#include <dlfcn.h>
#include <stdarg.h>
#include <fcntl.h>

#ifndef F_OFD_SETLK
#define F_OFD_SETLK  37
#define F_OFD_SETLKW 38
#define F_OFD_GETLK  36
#endif

typedef int (*real_fcntl_t)(int fd, int cmd, ...);

int fcntl(int fd, int cmd, ...) {
    real_fcntl_t real_fcntl = (real_fcntl_t)dlsym(RTLD_NEXT, "fcntl");
    va_list ap;
    va_start(ap, cmd);

    switch (cmd) {
    case F_OFD_SETLK:  cmd = F_SETLK;  break;
    case F_OFD_SETLKW: cmd = F_SETLKW; break;
    case F_OFD_GETLK:  cmd = F_GETLK;  break;
    }

    void *arg = va_arg(ap, void *);
    va_end(ap);
    return real_fcntl(fd, cmd, arg);
}
