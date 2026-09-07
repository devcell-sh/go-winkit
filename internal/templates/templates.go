// Package templates holds the guest PowerShell scripts shipped by winkit.
//
// Guest scripts live as files under this directory, never as Go raw strings.
//
// PowerShell's escape character is the backtick — the same character that
// delimits a Go raw string — so a script needing `n, `t or a line continuation
// cannot be written inline at all. That is not hypothetical: writing `Staged`
// inside a comment terminated a raw string and broke the build on 2026-07-31.
// Splicing Go constants also forced scripts to close and reopen their string
// mid-line, which is how a `"$env:USERNAME:(R)"` interpolation bug survived
// review. Template fields remove both hazards, and the scripts become
// syntax-highlightable, greppable files.
//
// Layout: <domain>/<name>.ps1.tmpl — provision/ for build-time provisioning,
// devenv/ for dev-environment setup, partials/ for shared fragments.
package templates

import (
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed *.tmpl devenv partials provision
var files embed.FS

// Render renders a guest script by path relative to this package.
//
// A template that fails to parse or execute is a programming error, not a
// runtime condition: the data comes from our own config structs, so there is
// no input a user could supply to trigger it. Panicking keeps every caller's
// signature free of an error that cannot happen in practice.
func Render(path string, data any) string {
	raw, err := files.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("guest template %s: %v", path, err))
	}
	// Partials are parsed alongside every script, so a shared fragment (the
	// virtio CD probe, say) is written once and pulled in with
	// {{template "name"}} rather than spliced from a Go constant.
	tmpl, err := template.New(path).Parse(string(raw))
	if err != nil {
		panic(fmt.Sprintf("parsing guest template %s: %v", path, err))
	}
	if _, err := tmpl.ParseFS(files, "partials/*.tmpl"); err != nil {
		panic(fmt.Sprintf("parsing guest template partials: %v", err))
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		panic(fmt.Sprintf("rendering guest template %s: %v", path, err))
	}
	return buf.String()
}
