package cli

import (
	"strings"
	"testing"
)

// compose-list prints the stacks with where their newest run stands and the
// commands by the id compose-start takes; the root stack reads as a dot.
func TestComposeListReadsStacksAndCommands(t *testing.T) {
	out := formatComposeList(map[string]any{
		"project":   "shop",
		"available": true,
		"stacks": []any{
			map[string]any{"label": "", "running": 2.0, "total": 3.0, "busy": false,
				"run": map[string]any{"id": "r1", "action": "Compose up", "status": "Exit status 0"}},
			map[string]any{"label": "ops", "running": 0.0, "total": 0.0, "busy": true},
		},
		"actions": []any{
			map[string]any{"id": "up", "label": "Compose up", "command": "docker compose up -d", "timeout": "10m0s", "confirm": false},
			map[string]any{"id": "down-volumes", "label": "Compose down with volumes", "command": "docker compose down -v", "timeout": "5m0s", "confirm": true},
		},
	})
	for _, want := range []string{
		"Stacks of shop:",
		"  .: 2 of 3 containers running; newest run r1: Compose up, exit status 0",
		"  ops: 0 of 0 containers running, a command runs right now",
		"  up: Compose up (docker compose up -d, up to 10m0s)",
		"  down-volumes: Compose down with volumes (docker compose down -v, up to 5m0s, asks the user first)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if out := formatComposeList(map[string]any{"project": "shop", "available": false}); !strings.Contains(out, "no reachable Docker host") {
		t.Fatalf("no daemon reads as:\n%s", out)
	}
	if out := formatComposeList(map[string]any{"project": "shop", "available": true, "stacks": []any{}, "actions": []any{}}); !strings.Contains(out, "no compose file") || !strings.Contains(out, "No compose command is configured") {
		t.Fatalf("an empty project reads as:\n%s", out)
	}
}

// compose-start says one line: the run id and where it runs, or that it
// waits for the user, which is the line a turn quotes to the user.
func TestComposeStartLineSaysWhetherItWaits(t *testing.T) {
	started := startedRunLine(map[string]any{"run": "r1", "action": "Compose up", "stack": "", "project": "shop"})
	if !strings.HasPrefix(started, "run r1 started: Compose up on . in shop.") {
		t.Fatalf("started reads %q", started)
	}
	parked := startedRunLine(map[string]any{"run": "r2", "action": "Compose down with volumes", "stack": "ops", "project": "shop", "pending": true})
	if !strings.HasPrefix(parked, "run r2 waits for the user's approval: Compose down with volumes on ops in shop.") || !strings.Contains(parked, "do not start it again") {
		t.Fatalf("parked reads %q", parked)
	}
}

// compose-show prints the facts and the tail of the output, the tail capped
// by --lines and said to be one, everything with 0.
func TestComposeShowCapsTheTail(t *testing.T) {
	answer := map[string]any{
		"id": "r1", "action": "Compose up", "stack": "", "command": "docker compose up -d",
		"status": "Exit status 0", "running": false, "exited": true, "exit": 0.0,
		"startedAt": "2026-09-25T10:00:00Z", "endedAt": "2026-09-25T10:00:09Z",
		"output": "one\ntwo\nthree\nfour\n",
	}
	out := formatComposeShow(answer, 2)
	for _, want := range []string{"run r1: Compose up on .", "command: docker compose up -d", "status: done, exit status 0", "exit code: 0", "output, the last 2 of 4 lines:", "  three\n  four\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "  one\n") {
		t.Fatalf("the cap did not hold:\n%s", out)
	}
	whole := formatComposeShow(answer, 0)
	if !strings.Contains(whole, "output:\n  one\n  two\n  three\n  four\n") {
		t.Fatalf("0 did not print everything:\n%s", whole)
	}
	for _, tc := range []struct {
		fields map[string]any
		want   string
	}{
		{map[string]any{"pending": true, "status": "Awaiting approval"}, "status: waits for the user's approval"},
		{map[string]any{"running": true, "status": "Running"}, "status: running"},
		{map[string]any{"failed": true, "failure": "exit status 1", "exited": true, "exit": 1.0}, "status: failed: exit status 1"},
		{map[string]any{"failed": true, "declined": true, "failure": "declined by the user"}, "status: declined, never ran: declined by the user"},
	} {
		if out := formatComposeShow(tc.fields, 5); !strings.Contains(out, tc.want) || !strings.Contains(out, "output: nothing yet") {
			t.Fatalf("want %q in:\n%s", tc.want, out)
		}
	}
}

// The stack a start posts: the one named, the only one where none is named,
// and a refusal naming the choices where there are several, the root stack
// reading as a dot in both directions.
func TestPickStackReadsTheRootStackAsADot(t *testing.T) {
	one := map[string]any{"stacks": []any{map[string]any{"label": ""}}}
	if label, err := chooseStack("shop", "", one); err != nil || label != "" {
		t.Fatalf("the only stack answered %q, %v", label, err)
	}
	if label, err := chooseStack("shop", ".", one); err != nil || label != "" {
		t.Fatalf("the root named as a dot answered %q, %v", label, err)
	}
	two := map[string]any{"stacks": []any{map[string]any{"label": ""}, map[string]any{"label": "ops"}}}
	if _, err := chooseStack("shop", "", two); err == nil || !strings.Contains(err.Error(), `".", "ops"`) {
		t.Fatalf("several stacks without a name answered %v", err)
	}
	if label, err := chooseStack("shop", "ops", two); err != nil || label != "ops" {
		t.Fatalf("the named stack answered %q, %v", label, err)
	}
	if _, err := chooseStack("shop", "nope", two); err == nil || !strings.Contains(err.Error(), `no stack "nope"`) {
		t.Fatalf("an unknown stack answered %v", err)
	}
	if _, err := chooseStack("shop", "", map[string]any{"stacks": []any{}}); err == nil || !strings.Contains(err.Error(), "no compose stack") {
		t.Fatalf("no stack answered %v", err)
	}
}
