package statusline

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// UsageURL is where claude's own /usage reads the limits of the account.
const UsageURL = "https://api.anthropic.com/api/oauth/usage"

// usageMaxAge is how long the answer of the usage API stands before the line
// asks again. The stamp is touched before the call, so a machine without a
// network pays the timeout once every two minutes and not once per redraw.
const usageMaxAge = 2 * time.Minute

// usageTimeout is the ceiling of one call, the same as git's: the sources run
// side by side, so the call never makes the line later than a stalled
// repository already can.
const usageTimeout = 5 * time.Second

// usageMaxBody bounds what is read of an answer. The real one is a few hundred
// bytes, this only keeps a broken server from filling the memory of a process
// that runs on every redraw.
const usageMaxBody = 1 << 20

const usageCacheName = "usage.json"

// loadUsage answers the cached usage document, asking the API first when the
// cache is older than usageMaxAge. The cache outlives a refused call, the API
// answers 429 for long stretches, which is why weeklyScoped drops a bucket
// whose reset has passed.
func loadUsage(ctx context.Context, env Env) any {
	if env.CacheDir == "" {
		return nil
	}
	cache := filepath.Join(env.CacheDir, usageCacheName)
	stamp := cache + ".stamp"
	if info, err := os.Stat(stamp); err != nil || env.Now.Sub(info.ModTime()) > usageMaxAge {
		refreshUsage(ctx, env, cache, stamp)
	}
	data, err := os.ReadFile(cache)
	if err != nil {
		return nil
	}
	var doc any
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	return doc
}

// refreshUsage asks the API with the token of claude's own login. The token
// goes into the request header and nowhere else, no argument list and no
// output of the line.
func refreshUsage(ctx context.Context, env Env, cache, stamp string) {
	if err := os.MkdirAll(filepath.Dir(cache), 0o700); err != nil {
		return
	}
	if err := os.WriteFile(stamp, nil, 0o600); err != nil {
		return
	}
	_ = os.Chtimes(stamp, env.Now, env.Now)
	token := oauthToken(env.Home)
	if token == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, usageTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.UsageURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, usageMaxBody))
	if err != nil || resp.StatusCode != http.StatusOK {
		return
	}
	var doc map[string]any
	if json.Unmarshal(body, &doc) != nil || doc["limits"] == nil {
		return
	}
	tmp := cache + ".tmp"
	if os.WriteFile(tmp, body, 0o600) != nil {
		return
	}
	if os.Rename(tmp, cache) != nil {
		_ = os.Remove(tmp)
	}
}

// oauthToken reads the access token of claude's login out of its credentials
// file. A login kept elsewhere, the macOS keychain for one, answers nothing and
// the entry stays away.
func oauthToken(home string) string {
	if home == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		return ""
	}
	var creds struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(data, &creds) != nil {
		return ""
	}
	return creds.ClaudeAiOauth.AccessToken
}

// weeklyScoped is the weekly limit that belongs to the named model, by the
// name the usage API gives it, or the first such limit where no model is
// named, as long as its week is not over: a bucket past its reset belongs to a
// week that is gone and would otherwise stand red beside the fresh weekly
// percentage.
func weeklyScoped(usage any, now int64, model string) (float64, bool) {
	model = strings.TrimSpace(model)
	limits, _ := field(usage, "limits").([]any)
	for _, limit := range limits {
		if kind, _ := str(field(limit, "kind")); kind != "weekly_scoped" {
			continue
		}
		if name, _ := str(field(limit, "scope", "model", "display_name")); model != "" && !strings.EqualFold(name, model) {
			continue
		}
		if resets, ok := epoch(field(limit, "resets_at")); ok && resets <= now {
			continue
		}
		return number(field(limit, "percent"))
	}
	return 0, false
}
