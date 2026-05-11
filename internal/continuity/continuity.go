// Package continuity stores OpenMelon's long-running creative spaces.
//
// The first implementation is deliberately file-backed and boring:
// .openmelon/spaces/<slug>/ holds the model-readable assumptions, canon,
// decision log, feedback log, plan, assets, and episodes. The goal is to
// give the agent durable context it can inspect and update, not to
// introduce a database before the workflow is proven.
package continuity

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/eight-acres-lab/openmelon/internal/projectx"
)

const (
	SpacesDirName          = "spaces"
	SpaceFileName          = "space.json"
	AssumptionsFileName    = "assumptions.md"
	CanonFileName          = "canon.md"
	MemoryFileName         = "memory.md"
	PlanFileName           = "plan.md"
	DecisionsFile          = "decisions.jsonl"
	FeedbackFile           = "feedback.jsonl"
	EpisodesDirName        = "episodes"
	AssetsDirName          = "assets"
	DefaultAssumptionsBody = "# Assumptions\n\nModel-generated setup assumptions live here until the user confirms, rejects, or edits them. These are lower authority than canon and decisions.\n"
	DefaultCanonBody       = "# Canon\n\nConfirmed long-term rules live here. Do not infer new canon without user confirmation.\n\n## Voice\n- TBD\n\n## Visual Style\n- TBD\n\n## Episode Structure\n- TBD\n"
	DefaultMemoryBody      = "# Memory\n\n"
	DefaultPlanBody        = "# Plan\n\n## Backlog\n- TBD\n"
)

var slugRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

var (
	ErrNotFound      = errors.New("continuity: not found")
	ErrAlreadyExists = errors.New("continuity: already exists")
)

type Space struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Platform    string    `json:"platform,omitempty"`
	Audience    string    `json:"audience,omitempty"`
	Status      string    `json:"status,omitempty"`
	Description string    `json:"description,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateSpaceOptions struct {
	ID          string
	Name        string
	Platform    string
	Audience    string
	Status      string
	Description string
	Tags        []string
	Assumptions string
}

type Decision struct {
	ID        string    `json:"id"`
	Scope     string    `json:"scope,omitempty"`
	Target    string    `json:"target,omitempty"`
	Decision  string    `json:"decision"`
	Reason    string    `json:"reason,omitempty"`
	Weight    float64   `json:"weight,omitempty"`
	Status    string    `json:"status,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Feedback struct {
	ID             string             `json:"id"`
	EpisodeID      string             `json:"episode_id,omitempty"`
	Source         string             `json:"source,omitempty"`
	Signal         string             `json:"signal"`
	Evidence       string             `json:"evidence,omitempty"`
	Recommendation string             `json:"recommendation,omitempty"`
	WeightDelta    map[string]float64 `json:"weight_delta,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
}

type Episode struct {
	ID        string    `json:"id"`
	Title     string    `json:"title,omitempty"`
	Topic     string    `json:"topic,omitempty"`
	Status    string    `json:"status,omitempty"`
	Brief     string    `json:"brief,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Asset struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind,omitempty"`
	SpaceID     string    `json:"space_id,omitempty"`
	Status      string    `json:"status,omitempty"`
	Description string    `json:"description,omitempty"`
	ReusePolicy string    `json:"reuse_policy,omitempty"`
	Files       []string  `json:"files,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	Weight      float64   `json:"weight,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Hit struct {
	Space *Space `json:"space"`
	Score int    `json:"score"`
}

type ContextPacket struct {
	ProjectID       string     `json:"project_id"`
	Authority       string     `json:"authority"`
	Space           *Space     `json:"space"`
	Assumptions     string     `json:"assumptions,omitempty"`
	Canon           string     `json:"canon,omitempty"`
	Memory          string     `json:"memory,omitempty"`
	Plan            string     `json:"plan,omitempty"`
	RecentDecisions []Decision `json:"recent_decisions,omitempty"`
	RecentFeedback  []Feedback `json:"recent_feedback,omitempty"`
	RecentEpisodes  []Episode  `json:"recent_episodes,omitempty"`
	Assets          []Asset    `json:"assets,omitempty"`
}

func SpacesDir(workdir string) string {
	return filepath.Join(projectx.StateDir(workdir), SpacesDirName)
}

func SpaceDir(workdir, id string) string {
	return filepath.Join(SpacesDir(workdir), id)
}

func ValidateID(id string) error {
	if len(id) < 2 || len(id) > 64 {
		return fmt.Errorf("continuity: id %q must be 2..64 chars", id)
	}
	if !slugRe.MatchString(id) {
		return fmt.Errorf("continuity: id %q must be kebab-case ([a-z][a-z0-9-]*)", id)
	}
	if strings.HasSuffix(id, "-") || strings.Contains(id, "--") {
		return fmt.Errorf("continuity: id %q must not have trailing or doubled hyphens", id)
	}
	return nil
}

func CreateSpace(workdir string, opts CreateSpaceOptions) (*Space, error) {
	if err := ValidateID(opts.ID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(opts.Name) == "" {
		opts.Name = opts.ID
	}
	status := strings.TrimSpace(opts.Status)
	if status == "" {
		status = "draft"
	}
	dir := SpaceDir(workdir, opts.ID)
	if _, err := os.Stat(filepath.Join(dir, SpaceFileName)); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrAlreadyExists, opts.ID)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, EpisodesDirName), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, AssetsDirName), 0o755); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	sp := &Space{
		ID:          opts.ID,
		Name:        opts.Name,
		Platform:    strings.TrimSpace(opts.Platform),
		Audience:    strings.TrimSpace(opts.Audience),
		Status:      status,
		Description: strings.TrimSpace(opts.Description),
		Tags:        cleanTags(opts.Tags),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := writeJSON(filepath.Join(dir, SpaceFileName), sp); err != nil {
		return nil, err
	}
	assumptions := strings.TrimSpace(opts.Assumptions)
	if assumptions == "" {
		assumptions = DefaultAssumptionsBody
	} else if !strings.HasSuffix(assumptions, "\n") {
		assumptions += "\n"
	}
	for path, body := range map[string]string{
		filepath.Join(dir, AssumptionsFileName): assumptions,
		filepath.Join(dir, CanonFileName):       DefaultCanonBody,
		filepath.Join(dir, MemoryFileName):      DefaultMemoryBody,
		filepath.Join(dir, PlanFileName):        DefaultPlanBody,
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return nil, err
		}
	}
	return sp, nil
}

func ListSpaces(workdir string) ([]*Space, error) {
	root := SpacesDir(workdir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Space{}, nil
		}
		return nil, err
	}
	out := make([]*Space, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sp, err := GetSpace(workdir, e.Name())
		if err == nil {
			out = append(out, sp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func GetSpace(workdir, id string) (*Space, error) {
	if err := ValidateID(id); err != nil {
		return nil, err
	}
	path := filepath.Join(SpaceDir(workdir, id), SpaceFileName)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: space %s", ErrNotFound, id)
		}
		return nil, err
	}
	var sp Space
	if err := json.Unmarshal(b, &sp); err != nil {
		return nil, err
	}
	return &sp, nil
}

func SearchSpaces(workdir, query string) ([]Hit, error) {
	spaces, err := ListSpaces(workdir)
	if err != nil {
		return nil, err
	}
	terms := strings.Fields(strings.ToLower(query))
	var hits []Hit
	for _, sp := range spaces {
		score := 0
		hay := strings.ToLower(strings.Join([]string{
			sp.ID, sp.Name, sp.Description, sp.Platform, sp.Audience, strings.Join(sp.Tags, " "),
		}, "\n"))
		if strings.TrimSpace(query) == "" {
			score = 1
		}
		for _, term := range terms {
			switch {
			case sp.ID == term:
				score += 10
			case strings.Contains(hay, term):
				score += 2
			default:
				score = -1
			}
			if score < 0 {
				break
			}
		}
		if sp.Status == "active" && score >= 0 {
			score += 3
		}
		if score >= 0 {
			hits = append(hits, Hit{Space: sp, Score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Space.ID < hits[j].Space.ID
	})
	return hits, nil
}

func ReadCanon(workdir, id string) (string, error) {
	return readText(filepath.Join(SpaceDir(workdir, id), CanonFileName))
}

func ReadAssumptions(workdir, id string) (string, error) {
	return readText(filepath.Join(SpaceDir(workdir, id), AssumptionsFileName))
}

func WriteAssumptions(workdir, id, body string) error {
	if _, err := GetSpace(workdir, id); err != nil {
		return err
	}
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return os.WriteFile(filepath.Join(SpaceDir(workdir, id), AssumptionsFileName), []byte(body), 0o644)
}

func WriteCanon(workdir, id, body string) error {
	if _, err := GetSpace(workdir, id); err != nil {
		return err
	}
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return os.WriteFile(filepath.Join(SpaceDir(workdir, id), CanonFileName), []byte(body), 0o644)
}

func ActivateSpace(workdir, id string, d Decision) (*Space, *Decision, error) {
	sp, err := GetSpace(workdir, id)
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(d.Decision) == "" {
		return nil, nil, fmt.Errorf("continuity: activation decision is required")
	}
	if d.Scope == "" {
		d.Scope = "space"
	}
	if d.Target == "" {
		d.Target = "space_activation"
	}
	dec, err := RecordDecision(workdir, id, d)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC()
	sp.Status = "active"
	sp.UpdatedAt = now
	if err := writeJSON(filepath.Join(SpaceDir(workdir, id), SpaceFileName), sp); err != nil {
		return nil, nil, err
	}
	return sp, dec, nil
}

func RecordDecision(workdir, spaceID string, d Decision) (*Decision, error) {
	if _, err := GetSpace(workdir, spaceID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(d.Decision) == "" {
		return nil, fmt.Errorf("continuity: decision is required")
	}
	now := time.Now().UTC()
	if d.ID == "" {
		d.ID = "dec-" + now.Format("20060102-150405")
	}
	if d.Scope == "" {
		d.Scope = "space"
	}
	if d.Status == "" {
		d.Status = "active"
	}
	if d.Weight == 0 {
		d.Weight = 1.0
	}
	d.CreatedAt = now
	if err := appendJSONL(filepath.Join(SpaceDir(workdir, spaceID), DecisionsFile), d); err != nil {
		return nil, err
	}
	return &d, nil
}

func RecordFeedback(workdir, spaceID string, f Feedback) (*Feedback, error) {
	if _, err := GetSpace(workdir, spaceID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(f.Signal) == "" {
		return nil, fmt.Errorf("continuity: signal is required")
	}
	now := time.Now().UTC()
	if f.ID == "" {
		f.ID = "fb-" + now.Format("20060102-150405")
	}
	if f.Source == "" {
		f.Source = "user"
	}
	f.CreatedAt = now
	if err := appendJSONL(filepath.Join(SpaceDir(workdir, spaceID), FeedbackFile), f); err != nil {
		return nil, err
	}
	return &f, nil
}

func CreateEpisode(workdir, spaceID string, ep Episode) (*Episode, error) {
	sp, err := GetSpace(workdir, spaceID)
	if err != nil {
		return nil, err
	}
	if sp.Status == "draft" {
		return nil, fmt.Errorf("continuity: space %s is draft; ask the user to confirm core assumptions and activate the space before creating durable episodes", spaceID)
	}
	if strings.TrimSpace(ep.ID) == "" {
		ep.ID = slugFromText(firstNonEmpty(ep.Topic, ep.Title, "episode"))
	}
	if err := ValidateID(ep.ID); err != nil {
		return nil, err
	}
	if ep.Status == "" {
		ep.Status = "draft"
	}
	now := time.Now().UTC()
	ep.CreatedAt = now
	ep.UpdatedAt = now
	dir := filepath.Join(SpaceDir(workdir, spaceID), EpisodesDirName, ep.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(dir, "episode.json"), ep); err != nil {
		return nil, err
	}
	if ep.Brief != "" {
		if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte(ensureNL(ep.Brief)), 0o644); err != nil {
			return nil, err
		}
	}
	return &ep, nil
}

func RegisterAsset(workdir, spaceID string, a Asset) (*Asset, error) {
	if _, err := GetSpace(workdir, spaceID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.ID) == "" {
		a.ID = slugFromText(firstNonEmpty(a.Description, a.Kind, "asset"))
	}
	if err := ValidateID(a.ID); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	a.SpaceID = spaceID
	if a.Status == "" {
		a.Status = "active"
	}
	if a.Weight == 0 {
		a.Weight = 1.0
	}
	a.CreatedAt = now
	a.UpdatedAt = now
	dir := filepath.Join(SpaceDir(workdir, spaceID), AssetsDirName, a.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(dir, "asset.json"), a); err != nil {
		return nil, err
	}
	return &a, nil
}

func BuildContextPacket(workdir, projectID, spaceID string) (*ContextPacket, error) {
	sp, err := GetSpace(workdir, spaceID)
	if err != nil {
		return nil, err
	}
	p := &ContextPacket{
		ProjectID: projectID,
		Authority: "canon and recent_decisions are confirmed/high-authority; assumptions are provisional/low-authority and must be confirmed before becoming long-term rules",
		Space:     sp,
	}
	p.Assumptions, _ = readText(filepath.Join(SpaceDir(workdir, spaceID), AssumptionsFileName))
	p.Canon, _ = readText(filepath.Join(SpaceDir(workdir, spaceID), CanonFileName))
	p.Memory, _ = readText(filepath.Join(SpaceDir(workdir, spaceID), MemoryFileName))
	p.Plan, _ = readText(filepath.Join(SpaceDir(workdir, spaceID), PlanFileName))
	p.RecentDecisions, _ = readJSONL[Decision](filepath.Join(SpaceDir(workdir, spaceID), DecisionsFile), 8)
	p.RecentFeedback, _ = readJSONL[Feedback](filepath.Join(SpaceDir(workdir, spaceID), FeedbackFile), 8)
	p.RecentEpisodes, _ = listEpisodes(workdir, spaceID, 8)
	p.Assets, _ = listAssets(workdir, spaceID, 20)
	return p, nil
}

func listEpisodes(workdir, spaceID string, limit int) ([]Episode, error) {
	root := filepath.Join(SpaceDir(workdir, spaceID), EpisodesDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Episode
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var ep Episode
		if err := readJSON(filepath.Join(root, e.Name(), "episode.json"), &ep); err == nil {
			out = append(out, ep)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func listAssets(workdir, spaceID string, limit int) ([]Asset, error) {
	root := filepath.Join(SpaceDir(workdir, spaceID), AssetsDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Asset
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var a Asset
		if err := readJSON(filepath.Join(root, e.Name(), "asset.json"), &a); err == nil {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Weight != out[j].Weight {
			return out[i].Weight > out[j].Weight
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func appendJSONL(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

func readJSONL[T any](path string, limit int) ([]T, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) == 1 && strings.TrimSpace(lines[0]) == "" {
		return nil, nil
	}
	start := 0
	if limit > 0 && len(lines) > limit {
		start = len(lines) - limit
	}
	out := make([]T, 0, len(lines)-start)
	for _, line := range lines[start:] {
		var v T
		if err := json.Unmarshal([]byte(line), &v); err == nil {
			out = append(out, v)
		}
	}
	return out, nil
}

func readText(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func cleanTags(tags []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func slugFromText(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevHy := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHy = false
		case r == ' ' || r == '_' || r == '-' || r == '.':
			if !prevHy && b.Len() > 0 {
				b.WriteByte('-')
				prevHy = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" || out[0] < 'a' || out[0] > 'z' {
		out = "item-" + out
		out = strings.TrimRight(out, "-")
	}
	if len(out) > 64 {
		out = strings.TrimRight(out[:64], "-")
	}
	if len(out) < 2 {
		out = "item"
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func ensureNL(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
