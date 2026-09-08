Prebuilt gosshd guest binaries embedded into the winkit CLI.

`task build` cross-compiles gosshd/cmd/gosshd to
gosshd_windows_<arch>.exe here before compiling winkit, so an installed
winkit can provision images from any directory. The .exe files are
build artifacts (gitignored); without them winkit falls back to
`go build`, which only works inside this source tree.
