package terminal

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// NewKey returns a fresh UUID-shaped tmux session name for coder panes and
// shells.
func NewKey() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	id := hex.EncodeToString(raw[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", id[0:8], id[8:12], id[12:16], id[16:20], id[20:32]), nil
}
