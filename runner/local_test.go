package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pointeight/skillplus-engine/registry"
)

func TestLocalRunnerRunsGoSkill(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module fake-skill\n\ngo 1.25.4\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

import (
	"encoding/json"
	"os"
)

func main() {
	var input map[string]any
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		panic(err)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"echo": input["text"]})
}
`)

	skill := registry.Skill{Dir: dir, Manifest: registry.Manifest{Slug: "fake-go", Runtime: "go", Entrypoint: "main", Output: registry.Output{MaxSizeBytes: 8192}}}
	out, err := NewLocal(2*time.Second).Run(context.Background(), skill, map[string]any{"text": "hello"})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(string(out), `"echo":"hello"`) {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestLocalRunnerTimesOut(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module slow-skill\n\ngo 1.25.4\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

import "time"

func main() {
	time.Sleep(2 * time.Second)
}
`)

	skill := registry.Skill{Dir: dir, Manifest: registry.Manifest{Slug: "slow-go", Runtime: "go", Entrypoint: "main", Output: registry.Output{MaxSizeBytes: 8192}}}
	_, err := NewLocal(100*time.Millisecond).Run(context.Background(), skill, map[string]any{"text": "hello"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestLocalRunnerRejectsEscapingEntrypoint(t *testing.T) {
	dir := t.TempDir()
	skill := registry.Skill{Dir: dir, Manifest: registry.Manifest{Slug: "bad-python", Runtime: "python", Entrypoint: "../main.py"}}

	_, err := NewLocal(2*time.Second).Run(context.Background(), skill, map[string]any{"text": "hello"})
	if err == nil {
		t.Fatal("expected entrypoint error")
	}
	if !strings.Contains(err.Error(), "escapes skill directory") {
		t.Fatalf("expected escaping entrypoint error, got %v", err)
	}
}

func TestLocalRunnerEnforcesOutputLimit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module noisy-skill\n\ngo 1.25.4\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

import "fmt"

func main() {
	fmt.Print("abcdef")
}
`)

	skill := registry.Skill{Dir: dir, Manifest: registry.Manifest{Slug: "noisy-go", Runtime: "go", Entrypoint: "main", Output: registry.Output{MaxSizeBytes: 3}}}
	_, err := NewLocal(2*time.Second).Run(context.Background(), skill, map[string]any{"text": "hello"})
	if err == nil {
		t.Fatal("expected output limit error")
	}
	if !strings.Contains(err.Error(), "output exceeded 3 bytes") {
		t.Fatalf("expected output limit error, got %v", err)
	}
}

func TestLocalRunnerRunsTypeScriptSkillWhenJavaScriptEntryExists(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.js"), `const chunks = [];
process.stdin.on("data", (chunk) => chunks.push(chunk));
process.stdin.on("end", () => {
  const input = JSON.parse(Buffer.concat(chunks).toString("utf8"));
  process.stdout.write(JSON.stringify({ mention: input.text.includes("@berry") }));
});
`)

	skill := registry.Skill{Dir: dir, Manifest: registry.Manifest{Slug: "fake-ts", Runtime: "typescript", Entrypoint: "main", Output: registry.Output{MaxSizeBytes: 8192}}}
	out, err := NewLocal(2*time.Second).Run(context.Background(), skill, map[string]any{"text": "hi @berry"})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(string(out), `"mention":true`) {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestLocalRunnerRejectsCustomGoEntrypoint(t *testing.T) {
	dir := t.TempDir()
	skill := registry.Skill{Dir: dir, Manifest: registry.Manifest{Slug: "bad-go", Runtime: "go", Entrypoint: "../main.go"}}

	_, err := NewLocal(2*time.Second).Run(context.Background(), skill, map[string]any{"text": "hello"})
	if err == nil {
		t.Fatal("expected unsupported go entrypoint error")
	}
	if !strings.Contains(err.Error(), "unsupported go entrypoint") {
		t.Fatalf("expected unsupported go entrypoint error, got %v", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
