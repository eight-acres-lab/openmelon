// Copyright 2026 Point Eight AI Pte. Ltd.
// Licensed under the Apache License, Version 2.0

// Package registry loads Skill-Plus skill manifests from local directories.
package registry

// Skill is a locally available Skill-Plus skill.
type Skill struct {
	Dir      string
	Manifest Manifest
}

// Manifest is the subset of skill.yaml needed by the engine MVP.
type Manifest struct {
	Slug          string        `yaml:"slug"`
	Version       string        `yaml:"version"`
	Name          string        `yaml:"name"`
	Description   string        `yaml:"description"`
	Author        string        `yaml:"author"`
	License       string        `yaml:"license"`
	Runtime       string        `yaml:"runtime"`
	Entrypoint    string        `yaml:"entrypoint"`
	DispatchHints DispatchHints `yaml:"dispatch_hints"`
	Resources     Resources     `yaml:"resources"`
	CostProfile   CostProfile   `yaml:"cost_profile"`
	Safety        Safety        `yaml:"safety"`
	Output        Output        `yaml:"output"`
}

type DispatchHints struct {
	InvokeWhen      []Condition `yaml:"invoke_when"`
	DoNotInvokeWhen []Condition `yaml:"do_not_invoke_when"`
}

type Condition struct {
	ContentType string `yaml:"content_type"`
	MinLength   *int   `yaml:"min_length"`
	MaxLength   *int   `yaml:"max_length"`
	Language    string `yaml:"language"`
	HasMedia    *bool  `yaml:"has_media"`
}

type Resources struct {
	ContextWindow   int    `yaml:"context_window"`
	ModelCapability string `yaml:"model_capability"`
	LatencyBudgetMS int    `yaml:"latency_budget_ms"`
	MemoryMB        int    `yaml:"memory_mb"`
	GPURequired     bool   `yaml:"gpu_required"`
	NetworkRequired bool   `yaml:"network_required"`
}

type CostProfile struct {
	AvgTokens    int `yaml:"avg_tokens"`
	P50LatencyMS int `yaml:"p50_latency_ms"`
	P99LatencyMS int `yaml:"p99_latency_ms"`
}

type Safety struct {
	DataRetention       string   `yaml:"data_retention"`
	UserConsentRequired bool     `yaml:"user_consent_required"`
	ContentCategories   []string `yaml:"content_categories"`
}

type Output struct {
	MaxSizeBytes int    `yaml:"max_size_bytes"`
	Format       string `yaml:"format"`
}
