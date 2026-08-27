# go-winkit

Go packages for building Windows environments from code: get install media, turn it into something bootable, and talk to the machine that comes up. Windows tooling mostly assumes a human clicking through an installer; winkit is for when the whole path has to run unattended.

Each package works on its own. `uupdump` and `mctcatalog` download Windows builds (from UUP dump and the Microsoft Update Catalog respectively). `isokit` builds and inspects bootable ISOs. `hcsvm` boots Hyper-V VMs, including from inside WinPE. `gosshd` is a small SSH server meant to be cross-compiled into guests that ship without OpenSSH, WinPE being the main offender.

winkit is the Windows layer behind [devcell](https://github.com/DimmKirr/devcell), and the API follows what devcell needs. There is no v1 yet, so pin a version if you depend on it.

```sh
go get github.com/devcell-sh/go-winkit
```
