package artifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StableID returns a deterministic 16-character hex ID derived from the SHA256 of the
// concatenation of parts joined by ":".
func StableID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, ":")))
	return hex.EncodeToString(h[:])[:16]
}

// Write persists the artifact to dir, creating the directory if needed.
// It writes two files:
//   - {id}.{type}.txt        — the artifact content
//   - {id}.provenance.json   — the raw provenance snapshot stored in a.Provenance
//
// Collision handling: if a content file with the same ID already exists, Write
// appends a version suffix (-v2, -v3, …) and updates a.ID to the resolved value.
// This prevents silent overwrites as required by EC-003.
func Write(dir string, a *Artifact) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("artifacts.Write: mkdir %q: %w", dir, err)
	}

	typeName := strings.ReplaceAll(string(a.Type), "_", ".")

	// Resolve a collision-free ID.
	baseID := a.ID
	resolvedID := baseID
	for version := 2; ; version++ {
		candidate := filepath.Join(dir, resolvedID+"."+typeName+".txt")
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			break
		}
		if version > 100 {
			return fmt.Errorf("artifacts.Write: too many collisions for id %q (>100 versions)", baseID)
		}
		resolvedID = fmt.Sprintf("%s-v%d", baseID, version)
	}
	// Update the artifact so callers see the actual ID that was persisted.
	a.ID = resolvedID

	contentPath := filepath.Join(dir, resolvedID+"."+typeName+".txt")
	if err := os.WriteFile(contentPath, []byte(a.Content), 0o644); err != nil {
		return fmt.Errorf("artifacts.Write: write content %q: %w", contentPath, err)
	}

	if a.Provenance != "" {
		provPath := filepath.Join(dir, resolvedID+".provenance.json")
		if err := os.WriteFile(provPath, []byte(a.Provenance), 0o644); err != nil {
			return fmt.Errorf("artifacts.Write: write provenance %q: %w", provPath, err)
		}
	}

	return nil
}
