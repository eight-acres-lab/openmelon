package tui

// tui.go — public entry point. Run() builds a Bubbletea Program around
// the Model in model.go, hooks the runtime's Tracer to it, and blocks
// until the user exits.

import (
	"context"
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/eight-acres-lab/openmelon/internal/projectx"
	"github.com/eight-acres-lab/openmelon/internal/runtime"
	"github.com/eight-acres-lab/openmelon/internal/session"
)

// Options matches repl.Options where it makes sense; the TUI consumes
// them after the caller has wired up project + runtime + (optionally)
// the session-aware tool registry rebuild callback.
type Options struct {
	Workdir       string
	Project       *projectx.Project
	Runtime       *runtime.Runtime
	WireSession   func(sessionDir string)
	SystemPrompt  string
	SessionIntent string
	LLMTag        string
	ImageTag      string
}

// Run starts the TUI. Blocks until the user exits.
func Run(_ context.Context, opts Options) error {
	if opts.Runtime == nil {
		return errors.New("tui: Runtime is required")
	}
	if opts.Project == nil {
		return errors.New("tui: Project is required")
	}

	sess, err := session.New(opts.Workdir, opts.Project.ID, opts.SessionIntent)
	if err != nil {
		return fmt.Errorf("tui: session: %w", err)
	}
	defer sess.Close()

	if opts.WireSession != nil {
		opts.WireSession(sess.Dir)
	}

	// Build the model with a runner closure. The runner is what the
	// worker goroutine calls; it captures the runtime + tracer.
	mInit := modelInit{
		Workdir:      opts.Workdir,
		Project:      opts.Project,
		Runtime:      opts.Runtime,
		SystemPrompt: opts.SystemPrompt,
		Session:      sess,
		LLMTag:       opts.LLMTag,
		ImageTag:     opts.ImageTag,
	}
	model := newModel(mInit)

	prog := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	// Wire the Tracer now that we have a Program.
	tracer := newProgramTracer(prog)
	opts.Runtime.Tracer = tracer

	// runner sends turn events through the tracer (which sends to the
	// program). The function itself blocks until runtime.Run returns.
	model.runner = func(ctx context.Context, in runtime.RunInput) (*runtime.RunResult, error) {
		return opts.Runtime.Run(ctx, in)
	}

	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}
