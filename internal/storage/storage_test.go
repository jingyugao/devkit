package storage

import (
	"testing"

	"github.com/jingyugao/devkit/internal/task"
)

func TestResolveSupportsUniqueIDPrefix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := New()
	records := []task.Record{
		{Spec: task.Spec{ID: "abc123", Name: "first"}},
		{Spec: task.Spec{ID: "def456", Name: "second"}},
	}
	for _, record := range records {
		if err := store.Save(record); err != nil {
			t.Fatalf("Save returned error: %v", err)
		}
	}

	got, err := store.Resolve("abc")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got.Spec.ID != "abc123" {
		t.Fatalf("expected abc123, got %q", got.Spec.ID)
	}
}

func TestResolveRejectsAmbiguousPrefix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := New()
	records := []task.Record{
		{Spec: task.Spec{ID: "abc123"}},
		{Spec: task.Spec{ID: "abc456"}},
	}
	for _, record := range records {
		if err := store.Save(record); err != nil {
			t.Fatalf("Save returned error: %v", err)
		}
	}

	if _, err := store.Resolve("abc"); err == nil {
		t.Fatalf("expected ambiguous prefix error")
	}
}
