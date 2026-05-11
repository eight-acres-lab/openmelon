package tui

// tui.go — public entry point. Run() builds a Bubbletea Program around
// the Model in model.go, hooks the runtime's Tracer to it, and blocks
// until the user exits.

import (
	"context"
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/eight-acres-lab/openmelon/internal/hooks"
	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/projectx"
	"github.com/eight-acres-lab/openmelon/internal/runtime"
	"github.com/eight-acres-lab/openmelon/internal/session"
	"github.com/eight-acres-lab/openmelon/internal/tools"
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

	// Provider info + hot-swap callbacks for the /model and
	// /model-image selectors.
	Provider          string
	ImageProvider     string
	LLMModel          string
	ImageModel        string
	RebuildLLM        func(model string) (string, error)
	RebuildImageModel func(provider, model string) (string, error)
	BashMode          projectx.BashPermissionMode
	ReasoningEffort   string
	SaveSettings      func(s projectx.Settings) error

	// ResumedFrom, when non-empty, is the session id this TUI is
	// continuing. Recorded in the new session's meta.json so the
	// chain of resumes is traceable.
	ResumedFrom string

	// InitialHistory, if non-empty, pre-populates the conversation.
	// First message is treated as the system prompt; everything else
	// renders into the transcript and seeds m.history so the next
	// user turn includes the prior context.
	InitialHistory []llm.Message

	// InstallApprove, if non-nil, is called by tui.Run with the
	// approval function the TUI provides. The caller installs it on
	// tools.Env.Approve so the bash tool can ask for confirmation.
	InstallApprove func(approve func(req tools.ApprovalRequest) tools.ApprovalDecision)
}

// Run starts the TUI. Blocks until the user exits.
func Run(_ context.Context, opts Options) error {
	if opts.Runtime == nil {
		return errors.New("tui: Runtime is required")
	}
	if opts.Project == nil {
		return errors.New("tui: Project is required")
	}

	sess, err := session.NewResume(opts.Workdir, opts.Project.ID, opts.SessionIntent, opts.ResumedFrom)
	if err != nil {
		return fmt.Errorf("tui: session: %w", err)
	}
	defer sess.Close()
	_ = sess.SetRuntimeInfo(opts.Provider, opts.LLMModel)
	opts.Runtime.Hooks = hooks.ChainManagers(opts.Runtime.Hooks, sess.HookRecorder())

	if opts.WireSession != nil {
		opts.WireSession(sess.Dir)
	}

	// Build the model with a runner closure. The runner is what the
	// worker goroutine calls; it captures the runtime + tracer.
	mInit := modelInit{
		Workdir:           opts.Workdir,
		Project:           opts.Project,
		Runtime:           opts.Runtime,
		SystemPrompt:      opts.SystemPrompt,
		Session:           sess,
		LLMTag:            opts.LLMTag,
		ImageTag:          opts.ImageTag,
		Provider:          opts.Provider,
		ImageProvider:     opts.ImageProvider,
		LLMModel:          opts.LLMModel,
		ImageModel:        opts.ImageModel,
		RebuildLLM:        opts.RebuildLLM,
		RebuildImageModel: opts.RebuildImageModel,
		BashMode:          opts.BashMode,
		ReasoningEffort:   opts.ReasoningEffort,
		SaveSettings:      opts.SaveSettings,
		InitialHistory:    opts.InitialHistory,
		ResumedFrom:       opts.ResumedFrom,
	}
	model := newModel(mInit)

	prog := tea.NewProgram(
		model,
	)

	// Wire the Tracer now that we have a Program.
	tracer := newProgramTracer(prog)
	opts.Runtime.Tracer = tracer

	// Install the approval bridge. The bash tool calls this from the
	// runtime worker goroutine; we send a tea.Msg into the program,
	// the user picks one of the approval options in the modal, the
	// model writes back to Reply, and we unblock here.
	if opts.InstallApprove != nil {
		opts.InstallApprove(func(req tools.ApprovalRequest) tools.ApprovalDecision {
			reply := make(chan tools.ApprovalDecision, 1)
			prog.Send(approvalRequestMsg{
				Tool:        req.Tool,
				Command:     req.Command,
				Description: req.Description,
				Binary:      req.Binary,
				Reply:       reply,
			})
			return <-reply
		})
	}

	// runner sends turn events through the tracer (which sends to the
	// program). The function itself blocks until runtime.Run returns.
	model.runner = func(ctx context.Context, in runtime.RunInput) (*runtime.RunResult, error) {
		return opts.Runtime.Run(ctx, in)
	}

	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	// Resume hint — printed AFTER alt-screen restores so the user
	// sees it in their normal shell scrollback.
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "session saved at "+sess.Dir)
	fmt.Fprintln(os.Stderr, "to resume:    openmelon resume "+sess.ID)
	return nil
}
