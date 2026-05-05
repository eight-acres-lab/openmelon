package registry

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/eight-acres-lab/openmelon/internal/projectx"
)

// initProject sets up a fresh project under a tmpdir and returns the workdir.
func initProject(t *testing.T) string {
	t.Helper()
	wd := t.TempDir()
	if _, err := projectx.Init(wd, "ai-talks", "AI Talks"); err != nil {
		t.Fatalf("project init: %v", err)
	}
	return wd
}

// writePNG writes a minimal valid PNG header to path. The file is small
// but real enough that registry can copy it and read its SHA256.
func writePNG(t *testing.T, path string) {
	t.Helper()
	// PNG signature + IHDR + IDAT + IEND for a 1x1 RGBA. The bytes
	// below are not strictly a decodable image but registry only cares
	// about extension + bytes copy.
	data := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}
}

func TestAddCharacter(t *testing.T) {
	wd := initProject(t)
	src := filepath.Join(t.TempDir(), "lao-wang-portrait.png")
	writePNG(t, src)

	item, err := Add(wd, AddOptions{
		Kind:        KindCharacter,
		Slug:        "lao-wang",
		Name:        "Lao Wang",
		Description: "Mid-50s street vendor with a quiet smile.",
		Tags:        []string{"character", "vendor", "elder"},
		Extra:       map[string]string{"role": "host"},
		ImagePath:   src,
		ImageName:   "portrait",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if item.Slug != "lao-wang" || item.Name != "Lao Wang" {
		t.Errorf("item mismatch: %+v", item)
	}
	if len(item.Images) != 1 || item.Images[0] != "portrait.png" {
		t.Errorf("images: %v", item.Images)
	}
	if got := item.Extra["role"]; got != "host" {
		t.Errorf("extra.role: got %q want host", got)
	}
	if item.Description != "Mid-50s street vendor with a quiet smile." {
		t.Errorf("description not persisted: %q", item.Description)
	}
	if len(item.Tags) != 3 {
		t.Errorf("tags: %v", item.Tags)
	}
}

func TestAddTwiceWithoutAllowExistsErrors(t *testing.T) {
	wd := initProject(t)
	if _, err := Add(wd, AddOptions{Kind: KindCharacter, Slug: "lao-wang", Name: "Lao Wang"}); err != nil {
		t.Fatalf("Add #1: %v", err)
	}
	_, err := Add(wd, AddOptions{Kind: KindCharacter, Slug: "lao-wang", Name: "Lao Wang"})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("expected ErrAlreadyExists, got %v", err)
	}
}

func TestAddWithAllowExistsMergesAndAppendsImage(t *testing.T) {
	wd := initProject(t)
	src1 := filepath.Join(t.TempDir(), "p1.png")
	src2 := filepath.Join(t.TempDir(), "p2.png")
	writePNG(t, src1)
	writePNG(t, src2)

	if _, err := Add(wd, AddOptions{
		Kind: KindCharacter, Slug: "lao-wang", Name: "Lao Wang",
		ImagePath: src1, ImageName: "portrait",
	}); err != nil {
		t.Fatalf("Add #1: %v", err)
	}
	updated, err := Add(wd, AddOptions{
		Kind: KindCharacter, Slug: "lao-wang", Description: "Updated bio.",
		Tags: []string{"vendor"}, ImagePath: src2, ImageName: "portrait",
		AllowExists: true,
	})
	if err != nil {
		t.Fatalf("Add #2 with AllowExists: %v", err)
	}
	if updated.Description != "Updated bio." {
		t.Errorf("desc not merged: %q", updated.Description)
	}
	// Same destination basename → second copy gets a -2 suffix.
	if len(updated.Images) != 2 {
		t.Errorf("expected 2 images, got %v", updated.Images)
	}
}

func TestListReturnsItemsInSlugOrder(t *testing.T) {
	wd := initProject(t)
	for _, slug := range []string{"zoe", "alice", "bob"} {
		if _, err := Add(wd, AddOptions{Kind: KindCharacter, Slug: slug, Name: slug}); err != nil {
			t.Fatalf("Add %s: %v", slug, err)
		}
	}
	items, err := List(wd, KindCharacter)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("len: %d", len(items))
	}
	want := []string{"alice", "bob", "zoe"}
	for i, s := range want {
		if items[i].Slug != s {
			t.Errorf("[%d]: %q want %q", i, items[i].Slug, s)
		}
	}
}

func TestGetReadsSearchFile(t *testing.T) {
	wd := initProject(t)
	if _, err := Add(wd, AddOptions{
		Kind: KindReference, Slug: "kitchen-night",
		Description: "Warm-tone neon kitchen at 22:00, steam from a wok.",
		Tags:        []string{"scene", "kitchen", "night"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	item, err := Get(wd, KindReference, "kitchen-night")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if item.Description != "Warm-tone neon kitchen at 22:00, steam from a wok." {
		t.Errorf("desc: %q", item.Description)
	}
	if len(item.Tags) != 3 || item.Tags[0] != "scene" {
		t.Errorf("tags: %v", item.Tags)
	}
}

func TestSetSearchUpdatesOnlyDescriptionAndTags(t *testing.T) {
	wd := initProject(t)
	if _, err := Add(wd, AddOptions{
		Kind: KindCharacter, Slug: "lao-wang", Name: "Lao Wang",
		Description: "Old.", Tags: []string{"v1"},
		Extra: map[string]string{"role": "host"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := SetSearch(wd, KindCharacter, "lao-wang", "New.", []string{"v2"}); err != nil {
		t.Fatalf("SetSearch: %v", err)
	}
	item, err := Get(wd, KindCharacter, "lao-wang")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if item.Description != "New." {
		t.Errorf("desc: %q", item.Description)
	}
	if len(item.Tags) != 1 || item.Tags[0] != "v2" {
		t.Errorf("tags: %v", item.Tags)
	}
	if item.Extra["role"] != "host" {
		t.Errorf("extra clobbered: %v", item.Extra)
	}
}

func TestRemoveDeletesItemDir(t *testing.T) {
	wd := initProject(t)
	if _, err := Add(wd, AddOptions{Kind: KindCharacter, Slug: "lao-wang", Name: "Lao Wang"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := Remove(wd, KindCharacter, "lao-wang"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	_, err := Get(wd, KindCharacter, "lao-wang")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestAddMaterialIsHashAddressed(t *testing.T) {
	wd := initProject(t)
	src := filepath.Join(t.TempDir(), "raw.png")
	writePNG(t, src)
	first, err := AddMaterial(wd, src, []string{"raw"})
	if err != nil {
		t.Fatalf("AddMaterial #1: %v", err)
	}
	second, err := AddMaterial(wd, src, []string{"raw"})
	if err != nil {
		t.Fatalf("AddMaterial #2: %v", err)
	}
	if first.Slug != second.Slug {
		t.Errorf("hash collision missed: %q vs %q", first.Slug, second.Slug)
	}
	items, err := List(wd, KindMaterial)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 deduped material, got %d", len(items))
	}
}

func TestValidateSlug(t *testing.T) {
	for _, ok := range []string{"a1", "lao-wang", "kitchen-night-001"} {
		if err := ValidateSlug(ok); err != nil {
			t.Errorf("ValidateSlug(%q) unexpected error: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "a", "AI", "9foo", "-x", "x-", "x--y", "with space"} {
		if err := ValidateSlug(bad); err == nil {
			t.Errorf("ValidateSlug(%q) expected error", bad)
		}
	}
}
