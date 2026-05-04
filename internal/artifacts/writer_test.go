package artifacts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStableID_deterministic(t *testing.T) {
	id1 := StableID("proj", "workflow", "stage")
	id2 := StableID("proj", "workflow", "stage")
	if id1 != id2 {
		t.Errorf("StableID is not deterministic: %q != %q", id1, id2)
	}
}

func TestStableID_differentParts(t *testing.T) {
	a := StableID("a", "b", "c")
	b := StableID("a", "b", "d")
	if a == b {
		t.Error("StableID should differ for different parts")
	}
}

func TestStableID_length(t *testing.T) {
	id := StableID("x")
	if len(id) != 16 {
		t.Errorf("StableID length = %d, want 16", len(id))
	}
}

func TestWrite_createsFiles(t *testing.T) {
	dir := t.TempDir()
	a := &Artifact{
		ID:         "abcd1234",
		Type:       TypeImagePrompt,
		Content:    "some prompt content",
		Provenance: `{"artifact_id":"abcd1234"}`,
	}

	if err := Write(dir, a); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	contentPath := filepath.Join(dir, "abcd1234.image.prompt.txt")
	data, err := os.ReadFile(contentPath)
	if err != nil {
		t.Fatalf("content file not found: %v", err)
	}
	if string(data) != "some prompt content" {
		t.Errorf("content = %q, want %q", string(data), "some prompt content")
	}

	provPath := filepath.Join(dir, "abcd1234.provenance.json")
	pData, err := os.ReadFile(provPath)
	if err != nil {
		t.Fatalf("provenance file not found: %v", err)
	}
	if !strings.Contains(string(pData), "abcd1234") {
		t.Errorf("provenance file missing artifact_id, got: %q", string(pData))
	}
}

func TestWrite_noProvenanceField(t *testing.T) {
	dir := t.TempDir()
	a := &Artifact{
		ID:      "ef567890",
		Type:    TypeCopyDraft,
		Content: "copy draft content",
	}

	if err := Write(dir, a); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	provPath := filepath.Join(dir, "ef567890.provenance.json")
	if _, err := os.Stat(provPath); !os.IsNotExist(err) {
		t.Error("provenance file should NOT be written when Provenance is empty")
	}
}

// TestWrite_collision verifies EC-003: a second Write with the same ID
// must not silently overwrite the first file; it must use a -v2 suffix.
func TestWrite_collision(t *testing.T) {
	dir := t.TempDir()

	a1 := &Artifact{ID: "col12345", Type: TypeImagePrompt, Content: "first content"}
	if err := Write(dir, a1); err != nil {
		t.Fatalf("first Write error: %v", err)
	}
	if a1.ID != "col12345" {
		t.Errorf("first write: ID should be unchanged, got %q", a1.ID)
	}

	// Second write with same original ID — should get -v2 suffix.
	a2 := &Artifact{ID: "col12345", Type: TypeImagePrompt, Content: "second content"}
	if err := Write(dir, a2); err != nil {
		t.Fatalf("second Write error: %v", err)
	}
	if a2.ID != "col12345-v2" {
		t.Errorf("second write: expected ID %q, got %q", "col12345-v2", a2.ID)
	}

	// Original file content must be preserved (not overwritten).
	data, err := os.ReadFile(filepath.Join(dir, "col12345.image.prompt.txt"))
	if err != nil {
		t.Fatalf("original file missing: %v", err)
	}
	if string(data) != "first content" {
		t.Errorf("original file overwritten: got %q", string(data))
	}

	// -v2 file must contain the second content.
	data2, err := os.ReadFile(filepath.Join(dir, "col12345-v2.image.prompt.txt"))
	if err != nil {
		t.Fatalf("v2 file missing: %v", err)
	}
	if string(data2) != "second content" {
		t.Errorf("v2 file wrong content: got %q", string(data2))
	}
}
