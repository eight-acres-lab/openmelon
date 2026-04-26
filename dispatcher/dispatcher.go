// Copyright 2026 Point Eight AI Pte. Ltd.
// Licensed under the Apache License, Version 2.0

// Package dispatcher implements Skill dispatch based on skill.yaml dispatch_hints.
package dispatcher

import "github.com/pointeight/skillplus-engine/registry"

// Input is the content summary used for dispatch decisions.
type Input struct {
	ContentType string
	TextLength  int
	Lang        string
	HasMedia    bool
}

// Dispatcher selects which Skills to run for a given content input.
type Dispatcher struct {
	skills []registry.Skill
}

// New creates a dispatcher over a fixed skill list.
func New(skills []registry.Skill) *Dispatcher {
	copied := make([]registry.Skill, len(skills))
	copy(copied, skills)
	return &Dispatcher{skills: copied}
}

// Match returns the skills that should run for input.
func (d *Dispatcher) Match(input Input) []registry.Skill {
	if d == nil {
		return nil
	}

	var matched []registry.Skill
	for _, skill := range d.skills {
		hints := skill.Manifest.DispatchHints
		if matchesAny(hints.DoNotInvokeWhen, input) {
			continue
		}
		if len(hints.InvokeWhen) == 0 || matchesAny(hints.InvokeWhen, input) {
			matched = append(matched, skill)
		}
	}
	return matched
}

func matchesAny(conditions []registry.Condition, input Input) bool {
	for _, condition := range conditions {
		if matchesCondition(condition, input) {
			return true
		}
	}
	return false
}

func matchesCondition(condition registry.Condition, input Input) bool {
	if condition.ContentType != "" && condition.ContentType != input.ContentType {
		return false
	}
	if condition.MinLength != nil && input.TextLength < *condition.MinLength {
		return false
	}
	if condition.MaxLength != nil && input.TextLength > *condition.MaxLength {
		return false
	}
	if condition.Language != "" && condition.Language != input.Lang {
		return false
	}
	if condition.HasMedia != nil && *condition.HasMedia != input.HasMedia {
		return false
	}
	return true
}
