package forge

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// The bounds of a token, and how much of one a page may show.
const (
	MaxTokenLength = 1024
	hintFrom       = 16
	hintLength     = 4
)

var (
	errTokensUnread   = errors.New("The tokens could not be read, nothing was changed.")
	errTokensNotSaved = errors.New("The token could not be saved.")
)

// tokenFile holds the tokens by host id, a file of its own beside the hosts
// so the backup carries it in a section of its own.
type tokenFile struct {
	Tokens map[string]string `json:"tokens"`
}

func (s *Service) tokensPath() string {
	return filepath.Join(s.dir, "tokens.json")
}

// checkToken reads a token as typed, a header value of visible ASCII. Typing
// one and removing the one there is at once is no clear wish.
func checkToken(raw string, remove bool) (string, error) {
	token := strings.TrimSpace(raw)
	switch {
	case token != "" && remove:
		return "", errors.New("Type a new token or remove the one there is, not both.")
	case len(token) > MaxTokenLength:
		return "", fmt.Errorf("A token has at most %d characters.", MaxTokenLength)
	case strings.IndexFunc(token, func(r rune) bool { return r < '!' || r > '~' }) >= 0:
		return "", errors.New("A token is one line of letters, digits and signs, without spaces.")
	}
	return token, nil
}

// ChangeToken gives the host with that id, a built in one included, a new
// token, or takes its token away.
func (s *Service) ChangeToken(id, token string, remove bool) error {
	token, err := checkToken(token, remove)
	if err != nil {
		return err
	}
	if _, err := s.Host(id); err != nil {
		return err
	}
	err = s.storeToken(id, token, remove)
	s.poll.hostChanged(id)
	return err
}

// storeToken writes a host's token, or takes it away. Neither leaves the
// file as it was.
func (s *Service) storeToken(id, token string, remove bool) error {
	if token == "" && !remove {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var f tokenFile
	if err := statefile.Read(s.tokensPath(), &f); err != nil {
		return errTokensUnread
	}
	if remove {
		if _, ok := f.Tokens[id]; !ok {
			return nil
		}
		delete(f.Tokens, id)
	} else {
		if f.Tokens == nil {
			f.Tokens = map[string]string{}
		}
		f.Tokens[id] = token
	}
	if err := s.write(s.tokensPath(), f); err != nil {
		return errTokensNotSaved
	}
	return nil
}

func (s *Service) token(id string) (string, error) {
	tokens, err := s.tokens()
	return tokens[id], err
}

func (s *Service) tokens() (map[string]string, error) {
	var f tokenFile
	if err := statefile.Read(s.tokensPath(), &f); err != nil {
		return nil, errTokensUnread
	}
	return f.Tokens, nil
}

// TokenHints answers, by host id, which hosts have a token and its last
// characters, which only a long token shows.
func (s *Service) TokenHints() (map[string]string, error) {
	var f tokenFile
	if err := statefile.Read(s.tokensPath(), &f); err != nil {
		return nil, errTokensUnread
	}
	hints := make(map[string]string, len(f.Tokens))
	for id, token := range f.Tokens {
		hints[id] = ""
		if len(token) >= hintFrom {
			hints[id] = token[len(token)-hintLength:]
		}
	}
	return hints, nil
}

// Test asks the host with that id who its token belongs to and which server
// answers, each cut to what a notice can carry: the server wrote them. What
// it found stands in Tested, in memory, until the host or its token
// changes, a failed test takes it away.
func (s *Service) Test(ctx context.Context, id string) (Connection, error) {
	h, err := s.Host(id)
	if err != nil {
		return Connection{}, err
	}
	turn := s.poll.turn(id)
	conn, err := s.connect(ctx, h)
	p := s.poll
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		delete(p.tested, id)
		return Connection{}, err
	}
	conn = Connection{Account: quoted(conn.Account), Name: quoted(conn.Name), Server: quoted(conn.Server)}
	if p.turns[id] == turn {
		p.tested[id] = conn
	}
	return conn, nil
}

// TokenPage is the page of h's server that creates a token for the cockpit,
// "" where its provider knows none.
func (s *Service) TokenPage(h Host) string {
	p, ok := s.provider(h.Kind.ID)
	if !ok {
		return ""
	}
	pager, ok := p.(TokenPager)
	if !ok {
		return ""
	}
	return pager.TokenPage(h, "dev-cockpit")
}

// Tested answers, by host id, what the last test of each host's token found
// since the host and its token last changed.
func (s *Service) Tested() map[string]Connection {
	s.poll.mu.Lock()
	defer s.poll.mu.Unlock()
	return maps.Clone(s.poll.tested)
}

// connect asks h through its provider who its token belongs to and which
// server answers, as the server wrote them.
func (s *Service) connect(ctx context.Context, h Host) (Connection, error) {
	token, err := s.token(h.ID)
	if err != nil {
		return Connection{}, err
	}
	if token == "" {
		return Connection{}, errors.New("The host has no token to test.")
	}
	p, ok := s.provider(h.Kind.ID)
	if !ok {
		return Connection{}, ErrNotFound
	}
	return p.Test(ctx, s.client, h, token)
}
