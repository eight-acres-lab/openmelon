package policy

import (
	"context"
	"strings"

	"github.com/eight-acres-lab/openmelon/internal/projectx"
)

type Decision string

const (
	Allow Decision = "allow"
	Ask   Decision = "ask"
	Deny  Decision = "deny"
)

type Request struct {
	Action      string
	Tool        string
	Workdir     string
	SpaceID     string
	TargetPath  string
	Command     string
	Description string
	Binary      string
}

type Response struct {
	Decision Decision
	Reason   string
}

type Enforcer interface {
	Check(context.Context, Request) Response
}

type DefaultEnforcer struct {
	BashMode      projectx.BashPermissionMode
	IsBashAllowed func(binary string) bool
	JudgeBash     func(ctx context.Context, command, description string) BashJudgement
}

type BashJudgement int

const (
	BashAsk BashJudgement = iota
	BashAuto
	BashBlock
)

func (e DefaultEnforcer) Check(ctx context.Context, req Request) Response {
	switch req.Action {
	case "bash.execute":
		return e.checkBash(ctx, req)
	case "continuity.write":
		return Response{Decision: Allow}
	default:
		return Response{Decision: Ask, Reason: "unknown action requires approval"}
	}
}

func (e DefaultEnforcer) checkBash(ctx context.Context, req Request) Response {
	mode := e.BashMode
	if mode == "" {
		mode = projectx.BashModeStrict
	}
	if mode == projectx.BashModeTrusted {
		return Response{Decision: Allow, Reason: "trusted"}
	}
	if e.IsBashAllowed != nil && e.IsBashAllowed(req.Binary) {
		return Response{Decision: Allow, Reason: "allowlisted"}
	}
	verdict := BashAsk
	if e.JudgeBash != nil {
		verdict = e.JudgeBash(ctx, req.Command, req.Description)
	}
	if verdict == BashBlock {
		return Response{Decision: Deny, Reason: "blocked by safety judge"}
	}
	if mode == projectx.BashModeAuto && verdict == BashAuto {
		return Response{Decision: Allow, Reason: "judge:auto"}
	}
	return Response{Decision: Ask}
}

func NormalizeDecision(d Decision) Decision {
	switch d {
	case Allow, Ask, Deny:
		return d
	default:
		return Ask
	}
}

func ReasonOrDefault(reason, fallback string) string {
	if strings.TrimSpace(reason) != "" {
		return reason
	}
	return fallback
}
