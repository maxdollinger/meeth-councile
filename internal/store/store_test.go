package store

import (
	"path/filepath"
	"testing"
)

func TestOpenRequiresPath(t *testing.T) {
	if _, err := Open("  "); err == nil {
		t.Fatal("Open with blank path: want error, got nil")
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debate.db")
	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	second.Close()
}

func TestNewRepositoriesPanicOnNilDB(t *testing.T) {
	for name, fn := range map[string]func(){
		"memory": func() { NewMemory(nil) },
		"log":    func() { NewLog(nil) },
		"turns":  func() { NewTurns(nil) },
		"calls":  func() { NewCalls(nil) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("New with nil db did not panic")
				}
			}()
			fn()
		})
	}
}
