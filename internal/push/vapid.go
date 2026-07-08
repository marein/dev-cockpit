package push

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// defaultSubscriber is the VAPID contact claim push services may use to
// reach the operator. The configured value lives in the channel config.
const defaultSubscriber = "mailto:dev-cockpit@example.com"

// vapidState is the server identity for Web Push. Generated once and then
// reused, rotating the keys would invalidate every stored subscription.
type vapidState struct {
	PublicKey  string `json:"publicKey"`
	PrivateKey string `json:"privateKey"`
}

func loadOrCreateVAPID(path string) (vapidState, error) {
	var state vapidState
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if json.Unmarshal(data, &state) == nil && state.PublicKey != "" && state.PrivateKey != "" {
			// The file is never rewritten, so tighten a pre-existing loose mode here.
			if err := os.Chmod(path, 0o600); err != nil {
				log.Printf("push: tighten mode of %s: %v", path, err)
			}
			return state, nil
		}
		broken := path + ".broken"
		if err := os.Rename(path, broken); err != nil {
			return vapidState{}, fmt.Errorf("%s is invalid and could not be quarantined: %w", path, err)
		}
		log.Printf("push: %s is invalid, quarantined as %s; generating new keys, registered devices must be enabled again", path, broken)
	case errors.Is(err, fs.ErrNotExist):
	default:
		// A transient read failure must not rotate the identity, that would
		// invalidate every registered device although the file is intact.
		return vapidState{}, fmt.Errorf("read %s: %w", path, err)
	}
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return vapidState{}, fmt.Errorf("generate keys: %w", err)
	}
	state = vapidState{PublicKey: publicKey, PrivateKey: privateKey}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return vapidState{}, err
	}
	out, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return vapidState{}, err
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return vapidState{}, err
	}
	return state, nil
}
