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
	MemoryItemsFile        = "memory.jsonl"
	CompactionFile         = "compactions.jsonl"
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
	Selection       *Selection `json:"selection,omitempty"`
	Assumptions     string     `json:"assumptions,omitempty"`
	Canon           string     `json:"canon,omitempty"`
	Memory          string     `json:"memory,omitempty"`
	Plan            string     `json:"plan,omitempty"`
	RecentDecisions []Decision `json:"recent_decisions,omitempty"`
	RecentFeedback  []Feedback `json:"recent_feedback,omitempty"`
	RecentEpisodes  []Episode  `json:"recent_episodes,omitempty"`
	Assets          []Asset    `json:"assets,omitempty"`
}

type SelectionOptions struct {
	Query         string
	MaxDecisions  int
	MaxFeedback   int
	MaxEpisodes   int
	MaxAssets     int
	IncludeDrafts bool
}

type Selection struct {
	Query         string   `json:"query,omitempty"`
	DecisionLimit int      `json:"decision_limit"`
	FeedbackLimit int      `json:"feedback_limit"`
	EpisodeLimit  int      `json:"episode_limit"`
	AssetLimit    int      `json:"asset_limit"`
	Reasons       []string `json:"reasons,omitempty"`
	Truncated     []string `json:"truncated,omitempty"`
}

type MemoryItem struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind,omitempty"`
	Scope     string    `json:"scope,omitempty"`
	Target    string    `json:"target,omitempty"`
	Content   string    `json:"content"`
	Source    string    `json:"source,omitempty"`
	Weight    float64   `json:"weight,omitempty"`
	Status    string    `json:"status,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type MemoryPromotion struct {
	ItemID   string `json:"item_id"`
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
	Target   string `json:"target,omitempty"`
}

type SpaceCompaction struct {
	ID        string    `json:"id"`
	Summary   string    `json:"summary"`
	Scope     string    `json:"scope,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type WorkflowPlan struct {
	Intent            string         `json:"intent"`
	Mode              string         `json:"mode"`
	SpaceID           string         `json:"space_id,omitempty"`
	NeedsConfirmation bool           `json:"needs_confirmation"`
	Reason            string         `json:"reason"`
	Steps             []WorkflowStep `json:"steps"`
}

type WorkflowStep struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Tool   string `json:"tool,omitempty"`
	Reason string `json:"reason"`
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
	terms := searchTerms(query)
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

func searchTerms(query string) []string {
	stop := map[string]bool{
		"continue":  true,
		"again":     true,
		"yesterday": true,
		"today":     true,
		"tomorrow":  true,
		"next":      true,
		"series":    true,
		"episode":   true,
		"post":      true,
		"the":       true,
		"a":         true,
		"an":        true,
	}
	var terms []string
	for _, raw := range strings.Fields(strings.ToLower(query)) {
		term := strings.Trim(raw, " \t\r\n.,;:!?()[]{}\"'")
		if term == "" || stop[term] {
			continue
		}
		terms = append(terms, term)
	}
	return terms
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

func RecordMemoryItem(workdir, spaceID string, item MemoryItem) (*MemoryItem, error) {
	if _, err := GetSpace(workdir, spaceID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(item.Content) == "" {
		return nil, fmt.Errorf("continuity: memory content is required")
	}
	now := time.Now().UTC()
	if item.ID == "" {
		item.ID = "mem-" + now.Format("20060102-150405")
	}
	if err := ValidateID(item.ID); err != nil {
		return nil, err
	}
	if item.Kind == "" {
		item.Kind = "observation"
	}
	if item.Source == "" {
		item.Source = "model"
	}
	if item.Status == "" {
		item.Status = "provisional"
	}
	if item.Weight == 0 {
		item.Weight = 0.5
	}
	item.CreatedAt = now
	item.UpdatedAt = now
	if err := appendJSONL(filepath.Join(SpaceDir(workdir, spaceID), MemoryItemsFile), item); err != nil {
		return nil, err
	}
	return &item, nil
}

func PromoteMemoryItem(workdir, spaceID string, p MemoryPromotion) (*Decision, error) {
	if strings.TrimSpace(p.ItemID) == "" {
		return nil, fmt.Errorf("continuity: memory item_id is required")
	}
	if strings.TrimSpace(p.Decision) == "" {
		return nil, fmt.Errorf("continuity: promotion decision is required")
	}
	reason := strings.TrimSpace(p.Reason)
	if reason == "" {
		reason = "Promoted from memory item " + p.ItemID
	}
	return RecordDecision(workdir, spaceID, Decision{
		Scope:    "memory",
		Target:   firstNonEmpty(p.Target, p.ItemID),
		Decision: p.Decision,
		Reason:   reason,
		Weight:   1.0,
	})
}

func RecordSpaceCompaction(workdir, spaceID string, c SpaceCompaction) (*SpaceCompaction, error) {
	if _, err := GetSpace(workdir, spaceID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.Summary) == "" {
		return nil, fmt.Errorf("continuity: compaction summary is required")
	}
	now := time.Now().UTC()
	if c.ID == "" {
		c.ID = "cmp-" + now.Format("20060102-150405")
	}
	if err := ValidateID(c.ID); err != nil {
		return nil, err
	}
	if c.Scope == "" {
		c.Scope = "space"
	}
	c.CreatedAt = now
	if err := appendJSONL(filepath.Join(SpaceDir(workdir, spaceID), CompactionFile), c); err != nil {
		return nil, err
	}
	return &c, nil
}

func PlanWorkflow(workdir, intent string) (*WorkflowPlan, error) {
	intent = strings.TrimSpace(intent)
	hits, err := SearchSpaces(workdir, intent)
	if err != nil {
		return nil, err
	}
	p := &WorkflowPlan{
		Intent: intent,
		Mode:   "new_space",
		Reason: "No matching active creative space was found; start with provisional assumptions and ask for confirmation.",
		Steps: []WorkflowStep{
			{ID: "find-context", Action: "search existing spaces", Tool: "list_spaces", Reason: "Avoid creating duplicate continuity spaces."},
			{ID: "draft-space", Action: "create draft space", Tool: "create_space", Reason: "Store provisional assumptions without polluting canon."},
			{ID: "ask-confirmation", Action: "ask concise confirmation questions", Reason: "Confirmed direction is required before durable episodes or decisions."},
		},
		NeedsConfirmation: true,
	}
	if len(hits) == 0 {
		return p, nil
	}
	best := hits[0].Space
	p.SpaceID = best.ID
	if best.Status == "draft" {
		p.Mode = "confirm_space"
		p.Reason = "A draft space matches; confirm or correct assumptions before production."
		p.Steps = []WorkflowStep{
			{ID: "load-context", Action: "load selected context", Tool: "get_context_packet", Reason: "Review assumptions and open context."},
			{ID: "ask-confirmation", Action: "ask user to confirm or correct core direction", Reason: "Draft spaces cannot create durable episodes."},
			{ID: "activate", Action: "activate after confirmation", Tool: "activate_space", Reason: "Promotion requires explicit confirmation."},
		}
		p.NeedsConfirmation = true
		return p, nil
	}
	p.Mode = "continue_space"
	p.Reason = "An active creative space matches; load selected context and continue production."
	p.Steps = []WorkflowStep{
		{ID: "load-context", Action: "load selected context", Tool: "get_context_packet", Reason: "Reuse canon, decisions, feedback, recent episodes, and ranked assets."},
		{ID: "adapt", Action: "adapt plan using feedback and memory", Reason: "Keep continuity while responding to recent performance or user direction."},
		{ID: "produce", Action: "create or update episode/assets", Tool: "create_episode", Reason: "Record durable production units after context is loaded."},
		{ID: "finish", Action: "summarize updates", Tool: "finish", Reason: "Return concise output and updated continuity state."},
	}
	p.NeedsConfirmation = false
	return p, nil
}

func BuildCompactionDraft(workdir, projectID, spaceID string) (string, error) {
	p, err := BuildSelectedContextPacket(workdir, projectID, spaceID, SelectionOptions{
		MaxDecisions: 12,
		MaxFeedback:  12,
		MaxEpisodes:  12,
		MaxAssets:    12,
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s Compaction\n\n", p.Space.Name)
	fmt.Fprintf(&b, "Space: %s (%s)\n", p.Space.ID, p.Space.Status)
	if strings.TrimSpace(p.Canon) != "" {
		b.WriteString("\n## Canon\n")
		b.WriteString(strings.TrimSpace(p.Canon))
		b.WriteString("\n")
	}
	if len(p.RecentDecisions) > 0 {
		b.WriteString("\n## Confirmed Decisions\n")
		for _, d := range p.RecentDecisions {
			fmt.Fprintf(&b, "- %s", d.Decision)
			if d.Target != "" {
				fmt.Fprintf(&b, " [%s]", d.Target)
			}
			b.WriteString("\n")
		}
	}
	if len(p.RecentFeedback) > 0 {
		b.WriteString("\n## Feedback Signals\n")
		for _, f := range p.RecentFeedback {
			fmt.Fprintf(&b, "- %s", f.Signal)
			if f.Recommendation != "" {
				fmt.Fprintf(&b, ": %s", f.Recommendation)
			}
			b.WriteString("\n")
		}
	}
	if len(p.Assets) > 0 {
		b.WriteString("\n## Reusable Assets\n")
		for _, a := range p.Assets {
			fmt.Fprintf(&b, "- %s (%s, weight %.2f): %s\n", a.ID, a.Status, a.Weight, a.Description)
		}
	}
	if len(p.RecentEpisodes) > 0 {
		b.WriteString("\n## Recent Episodes\n")
		for _, ep := range p.RecentEpisodes {
			fmt.Fprintf(&b, "- %s: %s\n", ep.ID, firstNonEmpty(ep.Topic, ep.Title))
		}
	}
	return strings.TrimSpace(b.String()) + "\n", nil
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

func UpdateAssetWeight(workdir, spaceID, assetID string, weight float64, status string) (*Asset, error) {
	if _, err := GetSpace(workdir, spaceID); err != nil {
		return nil, err
	}
	if err := ValidateID(assetID); err != nil {
		return nil, err
	}
	path := filepath.Join(SpaceDir(workdir, spaceID), AssetsDirName, assetID, "asset.json")
	var a Asset
	if err := readJSON(path, &a); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: asset %s", ErrNotFound, assetID)
		}
		return nil, err
	}
	a.Weight = weight
	if strings.TrimSpace(status) != "" {
		a.Status = strings.TrimSpace(status)
	}
	a.UpdatedAt = time.Now().UTC()
	if err := writeJSON(path, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func BuildContextPacket(workdir, projectID, spaceID string) (*ContextPacket, error) {
	return BuildSelectedContextPacket(workdir, projectID, spaceID, SelectionOptions{})
}

func BuildSelectedContextPacket(workdir, projectID, spaceID string, opts SelectionOptions) (*ContextPacket, error) {
	sp, err := GetSpace(workdir, spaceID)
	if err != nil {
		return nil, err
	}
	opts = normalizeSelectionOptions(opts)
	p := &ContextPacket{
		ProjectID: projectID,
		Authority: "canon and recent_decisions are confirmed/high-authority; assumptions are provisional/low-authority and must be confirmed before becoming long-term rules",
		Space:     sp,
		Selection: &Selection{
			Query:         strings.TrimSpace(opts.Query),
			DecisionLimit: opts.MaxDecisions,
			FeedbackLimit: opts.MaxFeedback,
			EpisodeLimit:  opts.MaxEpisodes,
			AssetLimit:    opts.MaxAssets,
			Reasons:       []string{"canon and decisions have highest authority", "feedback and asset weights influence future production", "recent episodes preserve continuity"},
		},
	}
	p.Assumptions, _ = readText(filepath.Join(SpaceDir(workdir, spaceID), AssumptionsFileName))
	p.Canon, _ = readText(filepath.Join(SpaceDir(workdir, spaceID), CanonFileName))
	p.Memory, _ = readText(filepath.Join(SpaceDir(workdir, spaceID), MemoryFileName))
	p.Plan, _ = readText(filepath.Join(SpaceDir(workdir, spaceID), PlanFileName))
	p.RecentDecisions, _ = readJSONL[Decision](filepath.Join(SpaceDir(workdir, spaceID), DecisionsFile), opts.MaxDecisions)
	p.RecentFeedback, _ = readJSONL[Feedback](filepath.Join(SpaceDir(workdir, spaceID), FeedbackFile), opts.MaxFeedback)
	p.RecentEpisodes, _ = listEpisodes(workdir, spaceID, opts.MaxEpisodes)
	p.Assets, _ = listAssets(workdir, spaceID, opts.MaxAssets)
	if opts.Query != "" {
		p.Assets = rankAssetsForQuery(p.Assets, opts.Query)
	}
	p.Selection.Truncated = detectTruncation(workdir, spaceID, opts, p)
	return p, nil
}

func normalizeSelectionOptions(opts SelectionOptions) SelectionOptions {
	if opts.MaxDecisions <= 0 {
		opts.MaxDecisions = 8
	}
	if opts.MaxFeedback <= 0 {
		opts.MaxFeedback = 8
	}
	if opts.MaxEpisodes <= 0 {
		opts.MaxEpisodes = 8
	}
	if opts.MaxAssets <= 0 {
		opts.MaxAssets = 20
	}
	return opts
}

func rankAssetsForQuery(in []Asset, query string) []Asset {
	terms := searchTerms(query)
	out := append([]Asset(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		si := assetQueryScore(out[i], terms)
		sj := assetQueryScore(out[j], terms)
		if si != sj {
			return si > sj
		}
		if out[i].Weight != out[j].Weight {
			return out[i].Weight > out[j].Weight
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

func assetQueryScore(a Asset, terms []string) int {
	if len(terms) == 0 {
		return 0
	}
	hay := strings.ToLower(strings.Join([]string{
		a.ID, a.Kind, a.Status, a.Description, a.ReusePolicy, strings.Join(a.Tags, " "),
	}, "\n"))
	score := 0
	for _, term := range terms {
		if strings.Contains(hay, term) {
			score += 3
		}
	}
	if a.Status == "canonical" {
		score++
	}
	return score
}

func detectTruncation(workdir, spaceID string, opts SelectionOptions, p *ContextPacket) []string {
	var out []string
	if countJSONLLines(filepath.Join(SpaceDir(workdir, spaceID), DecisionsFile)) > len(p.RecentDecisions) {
		out = append(out, "recent_decisions")
	}
	if countJSONLLines(filepath.Join(SpaceDir(workdir, spaceID), FeedbackFile)) > len(p.RecentFeedback) {
		out = append(out, "recent_feedback")
	}
	if countDirs(filepath.Join(SpaceDir(workdir, spaceID), EpisodesDirName)) > len(p.RecentEpisodes) {
		out = append(out, "recent_episodes")
	}
	if countDirs(filepath.Join(SpaceDir(workdir, spaceID), AssetsDirName)) > len(p.Assets) {
		out = append(out, "assets")
	}
	return out
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

func countJSONLLines(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

func countDirs(path string) int {
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
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
