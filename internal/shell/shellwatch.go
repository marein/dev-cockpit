package shell

import (
	"bytes"
	"time"

	"github.com/local/dev-cockpit/internal/terminal"
)

// Prompt marks injected into every shell via shellMarkEnv: PS0 fires when
// bash starts executing a command line, PROMPT_COMMAND when the prompt
// returns.
const (
	markCommandStart = "133;C"
	markPromptReturn = "133;D"
)

// minCommandDuration keeps completion news to commands that ran long enough
// to be worth a notification, quick one shots stay silent. Bells are not
// filtered, a program that rings does so on purpose.
const minCommandDuration = 2 * time.Second

// shellMarkEnv is the environment injected into new shells so bash emits the
// OSC 133 prompt marks the command watcher times. Both variables survive the
// login rc chain on stock setups; an rc that overwrites them silently turns
// the marks (and the completion notifications) off.
func shellMarkEnv() map[string]string {
	return map[string]string{
		"PS0":            `\e]133;C\a`,
		"PROMPT_COMMAND": `printf '\033]133;D\007'`,
	}
}

// RunCommandWatch watches every live shell and reports news: a foreground
// command finished after running at least minCommandDuration (a prompt-return
// mark preceded by a command-start mark, so bare prompt redraws and quick
// commands stay silent), or the shell rang a terminal bell.
// Blocks; run it in a goroutine.
func (s *Shells) RunCommandWatch(interval time.Duration, onNews func(shellID string)) {
	terminal.RunWatch(interval, func() map[string]string {
		alive := map[string]string{}
		for _, sh := range s.List() {
			alive[sh.TmuxSession] = sh.Identifier
		}
		return alive
	}, func(tmuxName, id string) (chan struct{}, error) {
		var commandStart time.Time
		var lastBell time.Time
		return terminal.WatchOutput(tmuxName, func(out []byte, marks []string) {
			for _, mark := range marks {
				switch mark {
				case markCommandStart:
					commandStart = time.Now()
				case markPromptReturn:
					if !commandStart.IsZero() && time.Since(commandStart) >= minCommandDuration {
						onNews(id)
					}
					commandStart = time.Time{}
				}
			}
			if bytes.IndexByte(out, 0x07) >= 0 && time.Since(lastBell) > terminal.BellCooldown {
				lastBell = time.Now()
				onNews(id)
			}
		})
	})
}
