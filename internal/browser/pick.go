// Package browser holds the scripts the browser extension runs in pages.
package browser

import _ "embed"

var (
	//go:embed pick.js
	pickScript []byte
	//go:embed console.js
	consoleScript []byte
	//go:embed draw.js
	drawScript []byte
)

func PickScript() []byte {
	return pickScript
}

func ConsoleScript() []byte {
	return consoleScript
}

func DrawScript() []byte {
	return drawScript
}
