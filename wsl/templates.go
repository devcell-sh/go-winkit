package wsl

import (
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

// renderDockerfile renders one of the packaged Dockerfile templates with
// its FROM base. The rendered text participates in the cache key (it is
// Recipe.Dockerfile), so a template or base change invalidates the cache.
func renderDockerfile(name, base string) (string, error) {
	t, err := template.ParseFS(templatesFS, "templates/"+name)
	if err != nil {
		return "", fmt.Errorf("wsl: parsing %s: %w", name, err)
	}
	var b strings.Builder
	if err := t.Execute(&b, struct{ Base string }{Base: base}); err != nil {
		return "", fmt.Errorf("wsl: rendering %s: %w", name, err)
	}
	return b.String(), nil
}

func mustRenderDockerfile(name, base string) string {
	s, err := renderDockerfile(name, base)
	if err != nil {
		panic(err)
	}
	return s
}

// imageSlug reduces a docker image ref to a tag/filename-safe token.
func imageSlug(ref string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '-'
		}
	}, ref)
}
