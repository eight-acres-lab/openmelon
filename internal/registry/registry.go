// Package registry is the on-disk store for openmelon's project-scoped
// content libraries: characters, references, and materials.
//
// All three share the same shape:
//   - a directory under <project>/.openmelon/<kind>/<slug>/
//   - a JSON metadata file (character.json / reference.json / material.json)
//   - a `.search` file with description + tags (mirror of skillplus' format)
//   - one or more attached image files
//
// "Kind" enumerates the three. They differ only in metadata semantics
// (a character has age/role/style, a reference has a scene description, a
// material has just a hash + source) — operations are uniform.
//
// The package is intentionally narrow: list / get / add / remove. Search
// (cross-kind grep) lives in package search; vision auto-describe lives
// in package runtime. Both consume registry's outputs.
package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/eight-acres-lab/openmelon/internal/projectx"
)

// Kind is a content library: characters / references / materials.
type Kind string

const (
	KindCharacter Kind = "character"
	KindReference Kind = "reference"
	KindMaterial  Kind = "material"
)

// dirFor returns the disk subdirectory for a given kind.
func dirFor(kind Kind) string {
	switch kind {
	case KindCharacter:
		return "characters"
	case KindReference:
		return "references"
	case KindMaterial:
		return "materials"
	}
	return ""
}

// metaFileFor returns the metadata filename for a given kind.
func metaFileFor(kind Kind) string {
	switch kind {
	case KindCharacter:
		return "character.json"
	case KindReference:
		return "reference.json"
	case KindMaterial:
		return "material.json"
	}
	return ""
}

// SearchFileName is the file holding description + tags. Same name across
// all three kinds, so search can grep them uniformly.
const SearchFileName = ".search"

// Errors.
var (
	ErrInvalidKind  = errors.New("registry: unknown kind")
	ErrNotFound     = errors.New("registry: not found")
	ErrAlreadyExists = errors.New("registry: already exists")
)

var slugRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ValidateSlug applies the same kebab-case rule as projectx + skillplus.
func ValidateSlug(slug string) error {
	if len(slug) < 2 || len(slug) > 64 {
		return fmt.Errorf("registry: slug %q must be 2..64 chars", slug)
	}
	if !slugRe.MatchString(slug) {
		return fmt.Errorf("registry: slug %q must be kebab-case ([a-z][a-z0-9-]*)", slug)
	}
	if strings.HasSuffix(slug, "-") || strings.Contains(slug, "--") {
		return fmt.Errorf("registry: slug %q must not have trailing or doubled hyphens", slug)
	}
	return nil
}

// Item is the unified shape returned by List / Get. Per-kind extras
// live in the Extra map (round-tripped through metadata.json).
type Item struct {
	Kind        Kind              `json:"kind"`
	Slug        string            `json:"slug"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Images      []string          `json:"images,omitempty"`   // basenames inside the item dir
	Extra       map[string]string `json:"extra,omitempty"`    // kind-specific scalar metadata
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// itemDir is <workdir>/.openmelon/<kind-dir>/<slug>/.
func itemDir(workdir string, kind Kind, slug string) string {
	return filepath.Join(projectx.StateDir(workdir), dirFor(kind), slug)
}

// itemMetaPath is <itemDir>/<kind>.json.
func itemMetaPath(workdir string, kind Kind, slug string) string {
	return filepath.Join(itemDir(workdir, kind, slug), metaFileFor(kind))
}

// itemSearchPath is <itemDir>/.search.
func itemSearchPath(workdir string, kind Kind, slug string) string {
	return filepath.Join(itemDir(workdir, kind, slug), SearchFileName)
}

// List returns all items of a given kind in slug order.
func List(workdir string, kind Kind) ([]*Item, error) {
	if dirFor(kind) == "" {
		return nil, fmt.Errorf("%w: %q", ErrInvalidKind, kind)
	}
	root := filepath.Join(projectx.StateDir(workdir), dirFor(kind))
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Item{}, nil
		}
		return nil, fmt.Errorf("registry: list %s: %w", kind, err)
	}
	out := make([]*Item, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		item, err := Get(workdir, kind, e.Name())
		if err != nil {
			// Skip half-written items but don't fail the whole list.
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// Get reads a single item.
func Get(workdir string, kind Kind, slug string) (*Item, error) {
	if dirFor(kind) == "" {
		return nil, fmt.Errorf("%w: %q", ErrInvalidKind, kind)
	}
	if err := ValidateSlug(slug); err != nil {
		return nil, err
	}
	metaPath := itemMetaPath(workdir, kind, slug)
	b, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s/%s", ErrNotFound, kind, slug)
		}
		return nil, fmt.Errorf("registry: read %s: %w", metaPath, err)
	}
	var item Item
	if err := json.Unmarshal(b, &item); err != nil {
		return nil, fmt.Errorf("registry: parse %s: %w", metaPath, err)
	}
	// Re-fill from .search (source of truth for description + tags so
	// search package and registry agree on a single bytes-on-disk view).
	desc, tags, err := readSearch(itemSearchPath(workdir, kind, slug))
	if err == nil {
		item.Description = desc
		item.Tags = tags
	}
	// Re-list images on disk.
	dir := itemDir(workdir, kind, slug)
	if entries, err := os.ReadDir(dir); err == nil {
		images := []string{}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			n := e.Name()
			if isImage(n) {
				images = append(images, n)
			}
		}
		sort.Strings(images)
		item.Images = images
	}
	return &item, nil
}

// AddOptions describes a new item being added to the registry.
type AddOptions struct {
	Kind Kind
	Slug string
	Name string

	// Description is one to a few sentences describing the item. Kept
	// together with Tags in .search so search can grep them.
	Description string
	Tags        []string

	// Extra is per-kind scalar metadata (e.g. character age, reference
	// scene type). Stored verbatim in the item's metadata JSON.
	Extra map[string]string

	// ImagePath, if non-empty, is copied into the item directory as
	// "image-001<ext>" (or whatever ImageName supplies). Multiple Add
	// calls with --append-image can stack additional images.
	ImagePath string
	// ImageName overrides the destination basename (without extension).
	ImageName string

	// AllowExists, when true, makes Add idempotent — re-adding the same
	// slug merges in new metadata + appends the image instead of
	// returning ErrAlreadyExists. Used by `... add --update`.
	AllowExists bool
}

// Add creates or updates an item.
//
// Returns ErrAlreadyExists if the item already exists and AllowExists is
// false.
func Add(workdir string, opts AddOptions) (*Item, error) {
	if dirFor(opts.Kind) == "" {
		return nil, fmt.Errorf("%w: %q", ErrInvalidKind, opts.Kind)
	}
	if err := ValidateSlug(opts.Slug); err != nil {
		return nil, err
	}
	if strings.TrimSpace(opts.Name) == "" {
		opts.Name = opts.Slug
	}
	dir := itemDir(workdir, opts.Kind, opts.Slug)
	metaPath := itemMetaPath(workdir, opts.Kind, opts.Slug)

	now := time.Now().UTC()
	var item Item
	if existing, err := os.ReadFile(metaPath); err == nil {
		if !opts.AllowExists {
			return nil, fmt.Errorf("%w: %s/%s", ErrAlreadyExists, opts.Kind, opts.Slug)
		}
		if err := json.Unmarshal(existing, &item); err != nil {
			return nil, fmt.Errorf("registry: parse existing %s: %w", metaPath, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("registry: stat %s: %w", metaPath, err)
	} else {
		item = Item{Kind: opts.Kind, Slug: opts.Slug, CreatedAt: now}
	}

	if opts.Name != "" {
		item.Name = opts.Name
	}
	if opts.Description != "" {
		item.Description = opts.Description
	}
	if len(opts.Tags) > 0 {
		item.Tags = opts.Tags
	}
	if len(opts.Extra) > 0 {
		if item.Extra == nil {
			item.Extra = map[string]string{}
		}
		for k, v := range opts.Extra {
			item.Extra[k] = v
		}
	}
	item.UpdatedAt = now

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("registry: mkdir %s: %w", dir, err)
	}

	// Copy image if supplied.
	if opts.ImagePath != "" {
		if err := copyImageInto(dir, opts.ImagePath, opts.ImageName); err != nil {
			return nil, err
		}
	}

	// Write .search (source of truth for description + tags).
	if err := writeSearch(itemSearchPath(workdir, opts.Kind, opts.Slug), item.Description, item.Tags); err != nil {
		return nil, err
	}

	// Persist metadata JSON. Strip Description+Tags+Images: those live
	// in .search / on disk respectively, so we don't store them twice.
	persisted := item
	persisted.Description = ""
	persisted.Tags = nil
	persisted.Images = nil
	b, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("registry: marshal: %w", err)
	}
	if err := os.WriteFile(metaPath, append(b, '\n'), 0o644); err != nil {
		return nil, fmt.Errorf("registry: write %s: %w", metaPath, err)
	}

	return Get(workdir, opts.Kind, opts.Slug)
}

// SetSearch updates only description + tags for an item. Used by the
// vision auto-describe path so it doesn't have to re-read all of meta.
func SetSearch(workdir string, kind Kind, slug, description string, tags []string) error {
	if dirFor(kind) == "" {
		return fmt.Errorf("%w: %q", ErrInvalidKind, kind)
	}
	if err := ValidateSlug(slug); err != nil {
		return err
	}
	if _, err := os.Stat(itemMetaPath(workdir, kind, slug)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s/%s", ErrNotFound, kind, slug)
		}
		return err
	}
	if err := writeSearch(itemSearchPath(workdir, kind, slug), description, tags); err != nil {
		return err
	}
	// Bump UpdatedAt.
	item, err := Get(workdir, kind, slug)
	if err != nil {
		return err
	}
	item.UpdatedAt = time.Now().UTC()
	persisted := *item
	persisted.Description = ""
	persisted.Tags = nil
	persisted.Images = nil
	b, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(itemMetaPath(workdir, kind, slug), append(b, '\n'), 0o644)
}

// Remove deletes the item directory and everything in it.
func Remove(workdir string, kind Kind, slug string) error {
	if dirFor(kind) == "" {
		return fmt.Errorf("%w: %q", ErrInvalidKind, kind)
	}
	if err := ValidateSlug(slug); err != nil {
		return err
	}
	dir := itemDir(workdir, kind, slug)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s/%s", ErrNotFound, kind, slug)
		}
		return err
	}
	return os.RemoveAll(dir)
}

// AddMaterial is a thin wrapper for the material-pool flow, where the
// slug is the sha256 of the file (so duplicates collapse).
//
// Returns the resulting item; if a material with the same hash already
// exists, the call is a no-op and the existing item is returned.
func AddMaterial(workdir, srcPath string, tags []string) (*Item, error) {
	hash, err := fileSHA256(srcPath)
	if err != nil {
		return nil, err
	}
	// Prefix with "m-" so the hex hash satisfies ValidateSlug (which
	// requires a leading [a-z]). 16 hex chars = 64 bits of entropy,
	// which is plenty for a per-project pool.
	slug := "m-" + hash[:16]
	return Add(workdir, AddOptions{
		Kind:        KindMaterial,
		Slug:        slug,
		Name:        slug,
		Tags:        tags,
		Extra:       map[string]string{"sha256": hash},
		ImagePath:   srcPath,
		ImageName:   "image",
		AllowExists: true,
	})
}

// --- helpers ---

// .search is a tiny line-oriented text format. We control both ends so
// avoid a YAML parser dep. Format:
//
//	description: <single line>
//	tags: tag-a, tag-b, tag-c
//
// readSearch returns ("", nil, nil) when the file is missing.
func readSearch(path string) (string, []string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, nil
		}
		return "", nil, err
	}
	desc := ""
	var tags []string
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "description":
			desc = val
		case "tags":
			for _, t := range strings.Split(val, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					tags = append(tags, t)
				}
			}
		}
	}
	return desc, tags, nil
}

func writeSearch(path, desc string, tags []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("description: ")
	// Collapse newlines so the format stays single-line per field.
	b.WriteString(strings.ReplaceAll(strings.TrimSpace(desc), "\n", " "))
	b.WriteString("\n")
	if len(tags) > 0 {
		b.WriteString("tags: ")
		b.WriteString(strings.Join(tags, ", "))
		b.WriteString("\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// copyImageInto copies src into dir. If destBaseName is "", the src
// basename is preserved; otherwise destBaseName + src extension is used.
// If a file with the chosen name already exists, a numeric suffix is
// appended (-2, -3, ...).
func copyImageInto(dir, src, destBaseName string) error {
	srcBase := filepath.Base(src)
	ext := filepath.Ext(srcBase)
	if !isImageExt(ext) {
		return fmt.Errorf("registry: %q is not an image (ext %q)", src, ext)
	}
	base := destBaseName
	if base == "" {
		base = strings.TrimSuffix(srcBase, ext)
	}
	candidate := filepath.Join(dir, base+ext)
	if _, err := os.Stat(candidate); err == nil {
		for i := 2; ; i++ {
			candidate = filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, i, ext))
			if _, err := os.Stat(candidate); os.IsNotExist(err) {
				break
			}
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("registry: open %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.Create(candidate)
	if err != nil {
		return fmt.Errorf("registry: create %s: %w", candidate, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("registry: copy %s -> %s: %w", src, candidate, err)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func isImage(name string) bool { return isImageExt(filepath.Ext(name)) }

func isImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
		return true
	}
	return false
}
