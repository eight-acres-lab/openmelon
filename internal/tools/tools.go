// Package tools is openmelon's runtime tool registry.
//
// A Tool is one callable function the model can invoke. The runtime
// (package runtime) wires the registry to an LLM, drives the
// chat-with-tools loop, and dispatches tool calls back to handlers in
// this package.
//
// Tool design rules:
//
//   - Every tool has a JSON-schema parameter spec (kept hand-written for
//     control over what the model sees in the wire prompt).
//   - Every tool returns a JSON-encoded result. Errors are returned as
//     JSON too, so the model can read the error and recover instead of
//     crashing the loop.
//   - No tool spawns subprocesses or makes network calls beyond what's
//     already authorized for openmelon (skillplus subprocess, image
//     generator, vbox-cli for publish).
//   - Tools see the project workdir and registry — no escape to other
//     paths on disk.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// Spec is a tool's machine-readable contract: name, description, JSON
// schema for parameters. Sent to the model as part of every Chat call.
type Spec struct {
	Name        string
	Description string
	// Parameters is a JSON Schema object. Keep it small.
	Parameters json.RawMessage
}

// Handler is the Go side of a tool call. It receives the raw arguments
// the model emitted (a JSON object as produced by the vendor's tool-call
// wire format) and returns a JSON-marshalable value or an error.
//
// Errors should be returned as values for "the model gave us bad input"
// situations — the runtime relays them back as a tool message so the
// model can self-correct. Non-recoverable errors (file IO failure,
// network blow-up) should be returned as the second return value.
type Handler func(ctx context.Context, raw json.RawMessage) (any, error)

// Tool ties a Spec and a Handler together.
type Tool struct {
	Spec    Spec
	Handler Handler
}

// Registry collects tools by name. The runtime asks the registry for
// (a) the list of Specs to send to the model, and (b) the Handler for a
// given name when dispatching a tool call.
type Registry struct {
	tools map[string]Tool
	order []string // preserves insertion order for stable wire prompts
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register adds a tool. Names must be unique; re-registering panics so
// nobody silently shadows a tool.
func (r *Registry) Register(t Tool) {
	if _, ok := r.tools[t.Spec.Name]; ok {
		panic(fmt.Sprintf("tools: duplicate registration: %s", t.Spec.Name))
	}
	r.tools[t.Spec.Name] = t
	r.order = append(r.order, t.Spec.Name)
}

// Specs returns all registered specs in registration order.
func (r *Registry) Specs() []Spec {
	out := make([]Spec, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.tools[name].Spec)
	}
	return out
}

// Names returns all registered tool names in registration order.
// Useful for tests and for the system prompt that lists what's
// available.
func (r *Registry) Names() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Dispatch calls the handler for name with the raw arguments. Returns
// ErrUnknownTool if the name isn't registered.
func (r *Registry) Dispatch(ctx context.Context, name string, args json.RawMessage) (any, error) {
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q (available: %v)", ErrUnknownTool, name, r.Names())
	}
	return t.Handler(ctx, args)
}

// ErrUnknownTool is returned by Dispatch when the requested tool name
// is not in the registry.
var ErrUnknownTool = errInf("unknown tool")

type errInf string

func (e errInf) Error() string { return string(e) }
