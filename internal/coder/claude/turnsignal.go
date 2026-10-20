package claude

import "github.com/marein/dev-cockpit/internal/coder"

// TurnSignal reads one of claude's hooks for the working mark. A hook fired
// from a subagent, claude's own helpers included, says nothing about the main
// thread.
//
// A prompt taken and a tool returned are work, a tool called is work inside
// it. Streamed text is a sign of life only: claude does not wait for that
// hook, so its file can land after the turn's Stop. The turn's end, an error
// end and the session's end close it, and so does a session that came up,
// fresh, resumed or cleared, because claude starts idle. The start after a
// compaction says nothing, it happens inside whatever ran the compaction.
//
// A compaction is a turn of its own only when the person asked for it: an
// automatic one runs inside a turn whose own hooks carry the mark, or while
// idle, where a summary that fails fires no PostCompact and would leave
// open what its PreCompact opened.
//
// A permission ask waits on one key, and its notification, sent six seconds
// later while the ask still stands, says the same. A question waits on a
// dialog the person fills in, from the moment claude calls the tool, and so
// do an elicitation and the plan claude asks to carry out: one of its options
// opens a field for the person's feedback, where claude waits without a word,
// while an approval and a submitted feedback each fire a hook at once. Every
// other notification, the idle one included, says nothing about the turn.
func (p *Coder) TurnSignal(ev coder.HookEvent) coder.TurnSignal {
	if ev.AgentID != "" {
		return coder.TurnSignalNone
	}
	switch ev.Name {
	case "UserPromptSubmit", "PostToolUse", "PostToolUseFailure", "PostToolBatch":
		return coder.TurnSignalOpen
	case "PreToolUse":
		if dialogTool(ev.ToolName) {
			return coder.TurnSignalQuestion
		}
		return coder.TurnSignalTool
	case "MessageDisplay":
		return coder.TurnSignalAlive
	case "Stop", "StopFailure", "SessionEnd":
		return coder.TurnSignalOver
	case "SessionStart":
		switch ev.Source {
		case "startup", "resume", "clear", "fork":
			return coder.TurnSignalOver
		}
	case "PreCompact":
		if ev.Trigger == "manual" {
			return coder.TurnSignalOpen
		}
	case "PostCompact":
		if ev.Trigger == "manual" {
			return coder.TurnSignalOver
		}
	case "PermissionRequest":
		if dialogTool(ev.ToolName) {
			return coder.TurnSignalQuestion
		}
		return coder.TurnSignalWaiting
	case "Notification":
		switch ev.NotificationType {
		case "permission_prompt":
			return coder.TurnSignalWaiting
		case "elicitation_dialog":
			return coder.TurnSignalQuestion
		}
	}
	return coder.TurnSignalNone
}

// planTool is the tool claude asks its person to carry out a plan with.
const planTool = "ExitPlanMode"

// dialogTool reports whether a tool is a dialog the person fills in rather
// than one key's permission, see TurnSignal.
func dialogTool(name string) bool {
	return name == askTool || name == planTool
}

// turnHooks are the hooks every cockpit session reports into the inbox. Stop
// and Notification are the news, all of them together the turn's account,
// see TurnSignal. claude 2.1.152 is the first release that knows every one;
// an older one skips a name it does not know or drops the whole settings
// file.
var turnHooks = []string{
	"Stop", "Notification", "StopFailure", "SessionStart", "SessionEnd",
	"UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "PostToolBatch",
	"PermissionRequest", "MessageDisplay", "PreCompact", "PostCompact",
}
