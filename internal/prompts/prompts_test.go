package prompts

import (
	"strings"
	"testing"
)

func TestAllParsesNameTheoryAndPrompt(t *testing.T) {
	defs, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(defs) != 5 {
		t.Fatalf("personas = %d, want 5", len(defs))
	}
	for _, p := range defs {
		if p.Slug == "" || p.Name == "" || p.Theory == "" {
			t.Errorf("persona = %+v, want slug, name, and theory set", p)
		}
		if !strings.Contains(p.Prompt, "# "+p.Theory) {
			t.Errorf("%s prompt missing theory heading %q", p.Slug, p.Theory)
		}
		if !strings.Contains(p.Prompt, "## "+p.Name) {
			t.Errorf("%s prompt missing name heading %q", p.Slug, p.Name)
		}
	}
}

func TestPersonaResolvesSlugAndName(t *testing.T) {
	bySlug, err := Persona("realism")
	if err != nil {
		t.Fatalf("Persona(slug): %v", err)
	}
	if strings.TrimSpace(bySlug) == "" {
		t.Fatal("Persona(slug) is empty")
	}

	byName, err := Persona("  thalindra  ")
	if err != nil {
		t.Fatalf("Persona(name): %v", err)
	}
	if byName != bySlug {
		t.Errorf("Persona(name) != Persona(slug)")
	}
}

func TestPersonaRejectsUnknown(t *testing.T) {
	if _, err := Persona("nihilism"); err == nil {
		t.Fatal("unknown persona: want error, got nil")
	}
}

func TestNamesListsDisplayNames(t *testing.T) {
	names := Names()
	if len(names) != 5 {
		t.Fatalf("names = %d, want 5", len(names))
	}
	for _, n := range names {
		if n == "" || n == "realism" {
			t.Errorf("name %q, want a parsed display name", n)
		}
	}
}
