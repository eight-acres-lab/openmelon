// Copyright 2026 Point Eight AI Pte. Ltd.
// Licensed under the Apache License, Version 2.0

// Package engine provides the core Skill-Plus execution engine.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/pointeight/skillplus-engine/dispatcher"
	"github.com/pointeight/skillplus-engine/registry"
)

// BFace represents the Agent-facing metadata produced by running Skills.
type BFace struct {
	VisualDescription string                     `json:"visual_description,omitempty"`
	Entities          []Entity                   `json:"entities,omitempty"`
	Topics            []string                   `json:"topics,omitempty"`
	Sentiment         *Sentiment                 `json:"sentiment,omitempty"`
	RAGAnchors        []string                   `json:"rag_anchors,omitempty"`
	AgentPrompts      []string                   `json:"agent_prompts,omitempty"`
	Lang              string                     `json:"lang,omitempty"`
	Safety            *SafetyResult              `json:"safety,omitempty"`
	SkillOutputs      map[string]json.RawMessage `json:"skill_outputs,omitempty"`
	SkillsApplied     []SkillRunInfo             `json:"skills_applied"`
}

// Entity represents a detected entity in content.
type Entity struct {
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	Confidence float64 `json:"confidence"`
}

// Sentiment represents sentiment analysis results.
type Sentiment struct {
	Valence float64 `json:"valence"`
	Arousal float64 `json:"arousal"`
}

// SafetyResult represents safety check results.
type SafetyResult struct {
	Flags []string `json:"flags"`
}

// SkillRunInfo records metadata about a single skill execution.
type SkillRunInfo struct {
	Skill      string `json:"skill"`
	Version    string `json:"version"`
	DurationMs int64  `json:"duration_ms"`
	Status     string `json:"status"`
}

// PostInput represents the A-face content input for pipeline processing.
type PostInput struct {
	PostID    int64    `json:"post_id"`
	Text      string   `json:"text"`
	MediaURLs []string `json:"media_urls"`
	Lang      string   `json:"lang"`
}

// Runner executes a skill and returns raw JSON output.
type Runner interface {
	Run(ctx context.Context, skill registry.Skill, input map[string]any) ([]byte, error)
}

// Engine is the core Skill-Plus execution engine.
type Engine struct {
	dispatcher *dispatcher.Dispatcher
	runner     Runner
}

// New creates an Engine over a fixed skill list.
func New(skills []registry.Skill, runner Runner) *Engine {
	return &Engine{
		dispatcher: dispatcher.New(skills),
		runner:     runner,
	}
}

// Process runs all matching Skills against the given post input and returns B-face output.
func (e *Engine) Process(ctx context.Context, input *PostInput) (*BFace, error) {
	if input == nil {
		return nil, errors.New("post input is required")
	}

	bface := &BFace{
		Lang:          input.Lang,
		Safety:        &SafetyResult{Flags: []string{}},
		SkillOutputs:  map[string]json.RawMessage{},
		SkillsApplied: []SkillRunInfo{},
	}
	if e == nil || e.dispatcher == nil || e.runner == nil {
		return bface, nil
	}

	contentType := "text"
	if len(input.MediaURLs) > 0 {
		contentType = "mixed"
	}

	matches := e.dispatcher.Match(dispatcher.Input{
		ContentType: contentType,
		TextLength:  len([]rune(input.Text)),
		Lang:        input.Lang,
		HasMedia:    len(input.MediaURLs) > 0,
	})

	runnerInput := map[string]any{
		"post_id":    input.PostID,
		"text":       input.Text,
		"media_urls": input.MediaURLs,
		"lang":       input.Lang,
	}

	for _, skill := range matches {
		started := time.Now()
		out, err := e.runner.Run(ctx, skill, runnerInput)
		info := SkillRunInfo{
			Skill:      skill.Manifest.Slug,
			Version:    skill.Manifest.Version,
			DurationMs: time.Since(started).Milliseconds(),
			Status:     "ok",
		}
		if err != nil {
			info.Status = "error"
			bface.SkillsApplied = append(bface.SkillsApplied, info)
			continue
		}

		if !json.Valid(out) {
			info.Status = "error"
			bface.SkillsApplied = append(bface.SkillsApplied, info)
			continue
		}

		var raw json.RawMessage = append([]byte(nil), out...)
		bface.SkillOutputs[skill.Manifest.Slug] = raw
		applyKnownFields(bface, out)
		bface.SkillsApplied = append(bface.SkillsApplied, info)
	}

	if len(bface.SkillOutputs) == 0 {
		bface.SkillOutputs = nil
	}
	return bface, nil
}

func applyKnownFields(bface *BFace, out []byte) {
	var fields map[string]any
	if err := json.Unmarshal(out, &fields); err != nil {
		return
	}
	if detected, ok := fields["detected_lang"].(string); ok && detected != "" && detected != "und" {
		bface.Lang = detected
	}
	if visual, ok := fields["visual_description"].(string); ok && visual != "" {
		bface.VisualDescription = visual
	}
	if topics, ok := fields["topics"].([]any); ok {
		for _, topic := range topics {
			if value, ok := topic.(string); ok {
				bface.Topics = append(bface.Topics, value)
			}
		}
	}
	if anchors, ok := fields["rag_anchors"].([]any); ok {
		for _, anchor := range anchors {
			if value, ok := anchor.(string); ok {
				bface.RAGAnchors = append(bface.RAGAnchors, value)
			}
		}
	}
}
