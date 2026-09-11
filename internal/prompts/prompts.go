// Package prompts loads the debate's system prompts. Content is embedded so a
// built binary carries its prompts with it.
package prompts

import (
	"embed"
	"fmt"
	"path"
	"strings"
	"sync"
)

//go:embed common.md personas/*.md
var files embed.FS

// Definition is one parsed position prompt. Name and Theory are read from the
// persona file's headings: the first leading "# " line is the Theory and the
// first leading "## " line is the Name. Prompt is the full file, headings
// included, as sent to the model.
type Definition struct {
	Slug   string // filename stem, e.g. "realism"
	Name   string // display name, e.g. "THALINDRA"
	Theory string // position label, e.g. "Moralischer Realismus"
	Prompt string
}

// Common returns the shared system prompt that every persona builds on.
func Common() string {
	b, err := files.ReadFile("common.md")
	if err != nil {
		panic("prompts: common.md missing: " + err.Error())
	}
	return strings.TrimSpace(string(b))
}

// All returns every persona, in filename order. Name and Theory are parsed from
// each file's headings; a file missing either is an error.
func All() ([]Definition, error) {
	entries, err := files.ReadDir("personas")
	if err != nil {
		return nil, fmt.Errorf("prompts: read personas: %w", err)
	}
	out := make([]Definition, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		slug := strings.TrimSuffix(e.Name(), ".md")
		p, err := parse(slug)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// Persona returns the position-specific system prompt for name. name may be a
// slug such as "realism" or a display name such as "THALINDRA"; lookups tolerate
// surrounding whitespace, case differences, spaces, and underscores.
func Persona(name string) (string, error) {
	p, ok := lookup(slugify(name))
	if !ok {
		return "", fmt.Errorf("prompts: unknown persona %q", name)
	}
	return p.Prompt, nil
}

// Names lists the available persona display names, in filename order.
func Names() []string {
	defs, err := All()
	if err != nil {
		return nil
	}
	names := make([]string, len(defs))
	for i, p := range defs {
		names[i] = p.Name
	}
	return names
}

var (
	indexOnce sync.Once
	index     map[string]Definition
	indexErr  error
)

// lookup resolves a slugified key — either a persona's slug or its lower-cased
// display name — to its Definition.
func lookup(key string) (Definition, bool) {
	indexOnce.Do(func() {
		index = map[string]Definition{}
		indexErr = buildIndex(index)
	})
	if indexErr != nil || key == "" {
		return Definition{}, false
	}
	p, ok := index[key]
	return p, ok
}

func buildIndex(dst map[string]Definition) error {
	defs, err := All()
	if err != nil {
		return err
	}
	for _, p := range defs {
		dst[p.Slug] = p
		if n := slugify(p.Name); n != "" {
			dst[n] = p
		}
	}
	return nil
}

// parse reads one persona file and extracts its Theory and Name headings.
func parse(slug string) (Definition, error) {
	b, err := files.ReadFile(path.Join("personas", slug+".md"))
	if err != nil {
		return Definition{}, fmt.Errorf("prompts: unknown persona %q", slug)
	}
	raw := strings.TrimSpace(string(b))
	p := Definition{Slug: slug, Prompt: raw}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case p.Theory == "" && strings.HasPrefix(line, "# "):
			p.Theory = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		case p.Name == "" && strings.HasPrefix(line, "## "):
			p.Name = strings.TrimSpace(strings.TrimPrefix(line, "## "))
		}
	}
	if p.Theory == "" {
		return Definition{}, fmt.Errorf("prompts: persona %q has no \"# THEORY\" heading", slug)
	}
	if p.Name == "" {
		return Definition{}, fmt.Errorf("prompts: persona %q has no \"## NAME\" heading", slug)
	}
	return p, nil
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
