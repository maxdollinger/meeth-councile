// Package prompts loads the debate's system prompts. Content is embedded so a
// built binary carries its prompts with it.
package prompts

import (
	"embed"
	"fmt"
	"path"
	"strings"
)

//go:embed common.md personas/*.md
var files embed.FS

// Common returns the shared system prompt that every persona builds on.
func Common() string {
	b, err := files.ReadFile("common.md")
	if err != nil {
		panic("prompts: common.md missing: " + err.Error())
	}
	return strings.TrimSpace(string(b))
}

// Persona returns the position-specific system prompt for name. name is a slug
// such as "realism"; lookups tolerate surrounding whitespace, case differences,
// spaces, and underscores.
func Persona(name string) (string, error) {
	slug := slugify(name)
	if slug == "" {
		return "", fmt.Errorf("prompts: empty persona name")
	}
	b, err := files.ReadFile(path.Join("personas", slug+".md"))
	if err != nil {
		return "", fmt.Errorf("prompts: unknown persona %q", name)
	}
	return strings.TrimSpace(string(b)), nil
}

// Names lists the available persona slugs.
func Names() []string {
	entries, err := files.ReadDir("personas")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	return names
}

func slugify(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '_' || r == '-':
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
