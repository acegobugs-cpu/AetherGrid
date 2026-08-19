package identity

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreSaveAndLoad(t *testing.T) {
	store := NewStore(t.TempDir())

	if _, err := store.Load(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound before save, got %v", err)
	}

	if err := store.Save("f47ac10b-58cc-4372-a567-0e02b2c3d479"); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if got != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Errorf("expected stored id, got %q", got)
	}
}

func TestStoreOverwrites(t *testing.T) {
	store := NewStore(t.TempDir())

	if err := store.Save("id-one"); err != nil {
		t.Fatalf("first save failed: %v", err)
	}
	if err := store.Save("id-two"); err != nil {
		t.Fatalf("second save failed: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if got != "id-two" {
		t.Errorf("expected overwritten id, got %q", got)
	}
}

func TestStorePersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()

	first := NewStore(dir)
	if err := first.Save("persistent-id"); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	second := NewStore(dir)
	got, err := second.Load()
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if got != "persistent-id" {
		t.Errorf("expected persisted id, got %q", got)
	}
}

func TestStoreEmptyFileReportedClearly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("   \n"), 0o600); err != nil {
		t.Fatalf("writing empty file: %v", err)
	}

	store := NewStore(dir)
	if _, err := store.Load(); err == nil {
		t.Fatal("expected an error for an empty identity file")
	}
}

func TestStoreCorruptFileReportedClearly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("\x00\x01garbage"), 0o600); err != nil {
		t.Fatalf("writing corrupt file: %v", err)
	}

	store := NewStore(dir)
	got, err := store.Load()
	if err != nil {
		t.Fatalf("load of weird-but-present content should be reported as a value, got %v", err)
	}
	if got == "" {
		t.Error("expected a non-empty value")
	}
}

func TestStoreRejectsEmptySave(t *testing.T) {
	store := NewStore(t.TempDir())

	if err := store.Save("  "); err == nil {
		t.Fatal("expected error for empty identity")
	}
}
