package opencode

import "path/filepath"

// The cockpit keeps opencode's TUI on the web terminal's palette with two
// generated files, both instance free like the notify plugin: a theme under
// the config directory's themes folder, and a pin config the sessions load
// through the environment. The theme names almost no colors of its own, it
// references the terminal: the base background is "none" (the TUI then
// paints the default background, which the terminal fills from the pane
// style and the xterm palette) and the foregrounds are ANSI slot numbers,
// which the web terminal themes live. A palette change in the cockpit therefore
// reaches a running TUI immediately, the way it reaches copilot, without any
// update path for the file; the light and dark variants only separate where
// normal and bright slots differ, picked live through the mode 2031 report
// the cockpit already injects. The exceptions are "text" and "textMuted",
// real colors per variant. Text because the TUI derives several colors by
// blending text and background (the transcript body, the model name, the
// shortcut hints), and a blend has no RGB to work with from a slot number or
// "none", it rendered those texts invisible. Muted because it also sits on
// the raised panel and element surfaces below, and a palette's slot 8 is
// tuned against that palette's background, not against those: on more than
// one family it drowned there (the mode's auto suffix was the report). All of this is verified against opencode 1.18.23:
// valid theme values are hex colors, ANSI numbers, "none" and defs
// references, an unresolved reference crashes the TUI at start, which is why
// the file is a compiled constant and never assembled from parts.
const themeEnv = "OPENCODE_CONFIG"

// themeName is the generated theme's name: the file name under themes/ and
// the value the pin config selects. The two belong together, a pin without
// the theme file leaves opencode on its own default.
const themeName = "dev-cockpit"

// pinConfigFile sits at the top of opencode's config directory, where no
// loader picks it up by name (the config chain reads config.json and
// opencode.json(c) only, verified on 1.18.23): it reaches a session only
// through OPENCODE_CONFIG in the tmux environment, and opencode merges that
// file over the global configuration, so the pin wins in cockpit sessions
// and only there. A session somebody starts by hand keeps the user's own
// theme.
const pinConfigFile = "dev-cockpit-config.json"

const pinConfig = `{
  "$schema": "https://opencode.ai/config.json",
  "theme": "` + themeName + `"
}
`

// tuiEnv names the second generated file. The TUI reads a configuration
// chain of its own (tui.json files, loaded apart from the main config; a
// tui key in the main config is silently ignored, verified on 1.18.23), and
// OPENCODE_TUI_CONFIG adds one file to that chain, after the user's global
// tui.json and before a project's own, so a project choice still wins.
const tuiEnv = "OPENCODE_TUI_CONFIG"

const tuiConfigFile = "dev-cockpit-tui.json"

// tuiConfig pins the scroll speed. opencode's TUI moves three lines per
// wheel event by default (the hardcoded fallback), and through the web
// terminal's wheel that lands as a jump; one line is what a terminal feels
// like. Measured on 1.18.23 by injecting wheel events: three lines without
// this file, one with it.
const tuiConfig = `{
  "scroll_speed": 1
}
`

// terminalTheme is the generated theme. Slots do not fit where a surface has
// to stand out next to the default background: the diff line tints, and the
// panel and element surfaces behind the typed prompt, the composer and the
// dialogs, which opencode's own themes raise the same way. Those values are
// fixed 256 indexes, near neutral so they sit on every palette, chosen so
// the quantized rendering keeps them exact, and split dark and light.
const terminalTheme = `{
  "$schema": "https://opencode.ai/theme.json",
  "theme": {
    "primary": {"dark": 12, "light": 4},
    "secondary": {"dark": 13, "light": 5},
    "accent": {"dark": 11, "light": 3},
    "error": {"dark": 9, "light": 1},
    "warning": {"dark": 11, "light": 3},
    "success": {"dark": 10, "light": 2},
    "info": {"dark": 14, "light": 6},
    "text": {"dark": "#e6e6e6", "light": "#202020"},
    "textMuted": {"dark": "#9c9c9c", "light": "#6a6a6a"},
    "background": "none",
    "backgroundPanel": {"dark": 236, "light": 255},
    "backgroundElement": {"dark": 238, "light": 252},
    "border": 8,
    "borderActive": {"dark": 7, "light": 0},
    "borderSubtle": 8,
    "diffAdded": {"dark": 10, "light": 2},
    "diffRemoved": {"dark": 9, "light": 1},
    "diffContext": 8,
    "diffHunkHeader": 8,
    "diffHighlightAdded": {"dark": 10, "light": 2},
    "diffHighlightRemoved": {"dark": 9, "light": 1},
    "diffAddedBg": {"dark": 22, "light": 194},
    "diffRemovedBg": {"dark": 52, "light": 224},
    "diffContextBg": "none",
    "diffLineNumber": 8,
    "diffAddedLineNumberBg": {"dark": 22, "light": 194},
    "diffRemovedLineNumberBg": {"dark": 52, "light": 224},
    "markdownText": "none",
    "markdownHeading": {"dark": 13, "light": 5},
    "markdownLink": {"dark": 12, "light": 4},
    "markdownLinkText": {"dark": 14, "light": 6},
    "markdownCode": {"dark": 10, "light": 2},
    "markdownBlockQuote": 8,
    "markdownEmph": {"dark": 11, "light": 3},
    "markdownStrong": {"dark": 13, "light": 5},
    "markdownHorizontalRule": 8,
    "markdownListItem": {"dark": 12, "light": 4},
    "markdownListEnumeration": {"dark": 14, "light": 6},
    "markdownImage": {"dark": 13, "light": 5},
    "markdownImageText": {"dark": 14, "light": 6},
    "markdownCodeBlock": "none",
    "syntaxComment": 8,
    "syntaxKeyword": {"dark": 13, "light": 5},
    "syntaxFunction": {"dark": 12, "light": 4},
    "syntaxVariable": {"dark": 14, "light": 6},
    "syntaxString": {"dark": 10, "light": 2},
    "syntaxNumber": {"dark": 11, "light": 3},
    "syntaxType": {"dark": 14, "light": 6},
    "syntaxOperator": {"dark": 13, "light": 5},
    "syntaxPunctuation": "none"
  }
}
`

// ensureSessionConfig puts the generated files in place and answers the two
// paths the session environment names. The theme file comes first on
// purpose: a theme write that failed must also withhold the pin, because a
// pin selecting a theme that is not in place crashes the TUI at start.
// Nothing removes the files at stop, without the environment variables they
// are inert.
func ensureSessionConfig() (pin, tui string, err error) {
	if err := ensureGeneratedFile(filepath.Join(configDir(), "themes", themeName+".json"), terminalTheme); err != nil {
		return "", "", err
	}
	pin = filepath.Join(configDir(), pinConfigFile)
	if err := ensureGeneratedFile(pin, pinConfig); err != nil {
		return "", "", err
	}
	tui = filepath.Join(configDir(), tuiConfigFile)
	if err := ensureGeneratedFile(tui, tuiConfig); err != nil {
		return "", "", err
	}
	return pin, tui, nil
}
