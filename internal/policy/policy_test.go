package policy

import (
	"context"
	"testing"

	"github.com/eight-acres-lab/openmelon/internal/projectx"
)

func TestDefaultEnforcerBashModes(t *testing.T) {
	ctx := context.Background()
	req := Request{Action: "bash.execute", Command: "ls", Description: "inspect", Binary: "ls"}

	if got := (DefaultEnforcer{}).Check(ctx, req); got.Decision != Ask {
		t.Fatalf("strict default = %s, want ask", got.Decision)
	}
	if got := (DefaultEnforcer{BashMode: projectx.BashModeTrusted}).Check(ctx, req); got.Decision != Allow || got.Reason != "trusted" {
		t.Fatalf("trusted = %+v, want allow/trusted", got)
	}
	if got := (DefaultEnforcer{
		BashMode: projectx.BashModeAuto,
		JudgeBash: func(context.Context, string, string) BashJudgement {
			return BashAuto
		},
	}).Check(ctx, req); got.Decision != Allow || got.Reason != "judge:auto" {
		t.Fatalf("auto judge = %+v, want allow/judge:auto", got)
	}
	if got := (DefaultEnforcer{
		JudgeBash: func(context.Context, string, string) BashJudgement {
			return BashBlock
		},
	}).Check(ctx, req); got.Decision != Deny {
		t.Fatalf("blocked = %+v, want deny", got)
	}
}

func TestDefaultEnforcerContinuityWriteAllowed(t *testing.T) {
	got := (DefaultEnforcer{}).Check(context.Background(), Request{Action: "continuity.write", Tool: "record_decision"})
	if got.Decision != Allow {
		t.Fatalf("continuity write = %+v, want allow", got)
	}
}
