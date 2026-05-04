package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// minimalProjectJSON is a self-contained project.json used by these unit tests.
// It avoids any dependency on the examples/ directory.
const minimalProjectJSON = `{
	"id": "test-project",
	"name": "Test Project",
	"platform": "xiaohongshu",
	"audience": "testers",
	"persona": "test persona",
	"workflows": {
		"test_flow": {
			"id": "test_flow",
			"name": "Test Flow",
			"vertical": "food",
			"stages": [
				{
					"stage": "visual_concretization",
					"skillplus_package": "some/package.skillplus",
					"compile_target": "openmelon",
					"model_profile": "image_generator",
					"artifact_type": "image_prompt",
					"locale": "zh-CN",
					"vars": {"realism_level": "high"}
				}
			]
		}
	}
}`

func writeProjectFile(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "project.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadWorkflows_validJSON(t *testing.T) {
	dir := t.TempDir()
	path := writeProjectFile(t, dir, minimalProjectJSON)

	wfs, err := LoadWorkflows(path)
	if err != nil {
		t.Fatalf("LoadWorkflows error: %v", err)
	}
	if len(wfs) != 1 {
		t.Fatalf("expected 1 workflow, got %d", len(wfs))
	}
	wf, ok := wfs["test_flow"]
	if !ok {
		t.Fatal("expected key 'test_flow' in workflows map")
	}
	if wf.ID != "test_flow" {
		t.Errorf("ID = %q, want %q", wf.ID, "test_flow")
	}
	if wf.Vertical != "food" {
		t.Errorf("Vertical = %q, want %q", wf.Vertical, "food")
	}
	if len(wf.Stages) != 1 {
		t.Fatalf("stages len = %d, want 1", len(wf.Stages))
	}
	if wf.Stages[0].Stage != StageVisualConcretization {
		t.Errorf("stage[0] = %q, want %q", wf.Stages[0].Stage, StageVisualConcretization)
	}
}

func TestLoadWorkflows_fileNotFound(t *testing.T) {
	_, err := LoadWorkflows("/nonexistent/path/project.json")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadWorkflows_noWorkflows(t *testing.T) {
	dir := t.TempDir()
	// Valid project JSON but no workflows key.
	noWf := `{"id":"x","name":"x","platform":"x"}`
	path := writeProjectFile(t, dir, noWf)

	_, err := LoadWorkflows(path)
	if err == nil {
		t.Fatal("expected error for missing workflows, got nil")
	}
}

func TestLoadWorkflows_idInferredFromMapKey(t *testing.T) {
	// When workflow JSON has no "id" field, it should be set from the map key.
	noIDJSON := `{
		"id": "proj", "name": "proj", "platform": "x",
		"workflows": {
			"inferred_id": {
				"name": "No ID Flow",
				"vertical": "food",
				"stages": []
			}
		}
	}`
	dir := t.TempDir()
	path := writeProjectFile(t, dir, noIDJSON)

	wfs, err := LoadWorkflows(path)
	if err != nil {
		t.Fatalf("LoadWorkflows error: %v", err)
	}
	wf := wfs["inferred_id"]
	if wf == nil {
		t.Fatal("workflow not found")
	}
	if wf.ID != "inferred_id" {
		t.Errorf("ID = %q, want %q", wf.ID, "inferred_id")
	}
}

// TestLoadWorkflows_stagesVarsPreserved verifies that vars map entries survive round-trip.
func TestLoadWorkflows_stagesVarsPreserved(t *testing.T) {
	dir := t.TempDir()
	path := writeProjectFile(t, dir, minimalProjectJSON)

	wfs, _ := LoadWorkflows(path)
	stage := wfs["test_flow"].Stages[0]

	if stage.Vars["realism_level"] != "high" {
		t.Errorf("vars['realism_level'] = %q, want %q", stage.Vars["realism_level"], "high")
	}
}

// TestWorkflowDefinition_jsonRoundTrip verifies JSON serialisation of WorkflowDefinition.
func TestWorkflowDefinition_jsonRoundTrip(t *testing.T) {
	wf := &WorkflowDefinition{
		ID:       "round_trip",
		Name:     "Round Trip",
		Vertical: "test",
		Stages: []StageDefinition{
			{
				Stage:            StageCopywriting,
				SkillPlusPackage: "pkg.skillplus",
				CompileTarget:    "openmelon",
				ModelProfile:     "gpt-4",
				ArtifactType:     "copy_draft",
			},
		},
	}

	data, err := json.Marshal(wf)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out WorkflowDefinition
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ID != wf.ID || out.Stages[0].Stage != wf.Stages[0].Stage {
		t.Errorf("round-trip mismatch: got %+v", out)
	}
}
