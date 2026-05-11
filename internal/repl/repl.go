// Package repl is openmelon's interactive read-eval-print loop.
//
// Mirrors the Claude Code interaction model at the headless level: type
// a message, watch the model stream a reply (and any tool calls it
// makes), then keep typing. Multi-turn state lives entirely in memory
// for the lifetime of the process; everything is also persisted into
// the session directory so a future `--session <id>` can resume.
//
// We deliberately do NOT pull in bubbletea here — keeping zero non-test
// deps. Trade-offs vs a real TUI:
//
//   - No fancy line editor (no history scrollback, no cursor movement
//     mid-line). bufio.Scanner reads whole lines.
//   - No alternate-screen rendering, no spinners. Streaming text just
//     prints to stdout as it arrives.
//   - No mid-stream Ctrl-C interrupt yet — Ctrl-C exits the process.
//
// The full TUI is on the 0.4 roadmap. For 0.3 the interaction works:
// you can have a multi-turn conversation, watch the agent think, see
// tool calls render with status, and run slash commands.
package repl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/eight-acres-lab/openmelon/internal/hooks"
	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/projectx"
	"github.com/eight-acres-lab/openmelon/internal/runtime"
	"github.com/eight-acres-lab/openmelon/internal/session"
)

// Options configures a REPL.
type Options struct {
	// Workdir is the project root.
	Workdir string

	// Project is the loaded project config (used in the system prompt).
	Project *projectx.Project

	// Runtime carries the LLM. The REPL sets Tracer + (optionally)
	// rebuilds Registry via WireSession after creating the session.
	Runtime *runtime.Runtime

	// WireSession is called once after the REPL creates a session, with
	// the session directory. Implementations should rebuild any tools
	// that need to write into the session (most notably generate_image,
	// which writes images into <session>/) and assign the new registry
	// onto Runtime.Registry. Optional — leave nil if the runtime's
	// registry is already self-contained.
	WireSession func(sessionDir string)

	// SystemPrompt is sent on the first turn.
	SystemPrompt string

	// SessionIntent is recorded into the session's meta.json. The REPL
	// has many turns, so use a short label (e.g. "interactive REPL").
	SessionIntent string

	// In / Out / Err default to os.Stdin / os.Stdout / os.Stderr.
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Run enters the REPL. Returns when the user exits with /exit, EOF
// (Ctrl-D), or SIGTERM.
func Run(ctx context.Context, opts Options) error {
	if opts.In == nil {
		opts.In = os.Stdin
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Err == nil {
		opts.Err = os.Stderr
	}
	if opts.Runtime == nil {
		return errors.New("repl: Runtime is required")
	}
	if opts.Project == nil {
		return errors.New("repl: Project is required")
	}

	sess, err := session.New(opts.Workdir, opts.Project.ID, opts.SessionIntent)
	if err != nil {
		return fmt.Errorf("repl: session: %w", err)
	}
	defer sess.Close()
	_ = sess.SetRuntimeInfo("", "")
	opts.Runtime.Hooks = hooks.ChainManagers(opts.Runtime.Hooks, sess.HookRecorder())

	if opts.WireSession != nil {
		opts.WireSession(sess.Dir)
	}

	tr := newTerminalTracer(opts.Out)
	opts.Runtime.Tracer = tr

	printBanner(opts.Out, opts.Project, sess)

	scanner := bufio.NewScanner(opts.In)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// First Run() call seeds the system prompt; subsequent calls feed
	// the prior history back via RunInput.History.
	var history []llm.Message
	persistedUpTo := 0
	turn := 0

	// Trap Ctrl-C: the first one cancels the in-flight turn, the second
	// one exits. Without this, Ctrl-C kills the whole process — fine
	// for one-shot CLIs, awful in a REPL.
	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	for {
		fmt.Fprint(opts.Out, "\n> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("repl: read input: %w", err)
			}
			fmt.Fprintln(opts.Out)
			return nil
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			done, hist, err := handleSlash(line, history, sess, opts)
			if err != nil {
				fmt.Fprintf(opts.Err, "openmelon: %v\n", err)
			}
			if hist != nil {
				history = hist
			}
			if done {
				return nil
			}
			continue
		}

		turn++
		_ = sess.AppendPrompt("user", line)
		in := runtime.RunInput{UserInput: line}
		if len(history) == 0 {
			in.SystemPrompt = opts.SystemPrompt
		} else {
			in.History = history
		}

		// Per-turn context so Ctrl-C only cancels this turn.
		turnCtx, cancelTurn := context.WithCancel(sigCtx)
		go func() {
			<-sigCtx.Done()
			cancelTurn()
		}()
		res, runErr := opts.Runtime.Run(turnCtx, in)
		cancelTurn()

		if res != nil {
			history = res.Messages
			if persistedUpTo < len(history) {
				_ = sess.AppendMessages(history[persistedUpTo:])
				persistedUpTo = len(history)
			}
			if res.FinishSummary != "" {
				fmt.Fprintf(opts.Out, "\n%s\n", res.FinishSummary)
			}
			for _, p := range res.FinishArtifacts {
				fmt.Fprintf(opts.Out, "  artifact: %s\n", p)
			}
		}
		if runErr != nil {
			if errors.Is(runErr, context.Canceled) {
				fmt.Fprintln(opts.Err, "\n[interrupted]")
			} else {
				fmt.Fprintf(opts.Err, "\nopenmelon: %v\n", runErr)
			}
		}
	}
}

// printBanner writes the REPL welcome line.
func printBanner(w io.Writer, p *projectx.Project, sess *session.Session) {
	fmt.Fprintf(w, "openmelon — project %s (%s)\n", p.ID, p.Name)
	fmt.Fprintf(w, "session: %s\n", filepath.Base(sess.Dir))
	fmt.Fprintln(w, "Type your request, or /help for commands. Ctrl-C interrupts a turn; Ctrl-D exits.")
}

// handleSlash processes a /<command> line. Returns (done, newHistory, err).
// done=true means exit the REPL.
func handleSlash(line string, history []llm.Message, sess *session.Session, opts Options) (bool, []llm.Message, error) {
	parts := strings.Fields(line)
	cmd := parts[0]
	switch cmd {
	case "/exit", "/quit", "/q":
		fmt.Fprintln(opts.Out, "bye.")
		return true, nil, nil
	case "/help", "/?":
		fmt.Fprintln(opts.Out, "  /clear              forget the conversation history")
		fmt.Fprintln(opts.Out, "  /history            print the message log so far")
		fmt.Fprintln(opts.Out, "  /save <path>        write the conversation to a file (jsonl)")
		fmt.Fprintln(opts.Out, "  /session            show the session directory")
		fmt.Fprintln(opts.Out, "  /exit | /quit | /q  exit")
		return false, nil, nil
	case "/clear":
		fmt.Fprintln(opts.Out, "(history cleared)")
		return false, []llm.Message{}, nil
	case "/history":
		for i, m := range history {
			label := string(m.Role)
			if len(m.ToolCalls) > 0 {
				label += " → tool_calls"
			}
			body := m.Content
			if len(body) > 200 {
				body = body[:200] + "…"
			}
			body = strings.ReplaceAll(body, "\n", " ")
			fmt.Fprintf(opts.Out, "  [%d] %s: %s\n", i, label, body)
		}
		return false, nil, nil
	case "/save":
		if len(parts) < 2 {
			return false, nil, errors.New("/save: usage: /save <path>")
		}
		f, err := os.Create(parts[1])
		if err != nil {
			return false, nil, fmt.Errorf("/save: %w", err)
		}
		defer f.Close()
		enc := newJSONLEncoder(f)
		for _, m := range history {
			if err := enc.encode(m); err != nil {
				return false, nil, err
			}
		}
		fmt.Fprintf(opts.Out, "saved %d messages → %s\n", len(history), parts[1])
		return false, nil, nil
	case "/session":
		fmt.Fprintln(opts.Out, sess.Dir)
		return false, nil, nil
	}
	return false, nil, fmt.Errorf("unknown command: %s (try /help)", cmd)
}
