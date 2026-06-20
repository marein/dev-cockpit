// Package config defines runtime configuration defaults.
package config

import (
	"errors"
	"strings"
	"time"

	"github.com/local/dev-cockpit/internal/filesystem"
)

const (
	DefaultProvider          = "copilot"
	DefaultHTTPAddr          = "0.0.0.0:80"
	DefaultProjectsDir       = "~/projects"
	DefaultAuthUsername      = "admin"
	DefaultAuthPasswordHash  = "$2a$10$EcwjmJDytWtfOs4anrB/gu6A0Gryb9r43SvSwqMr/Y.bPbRPFeyW."
	DefaultSessionCookieName = "session"
	DefaultSessionCookieKey  = "ThisKeyIsNotSecret"
	DefaultTrustedProxies    = "127.0.0.1/8,::1/128"
	DefaultTLSCertFile       = ""
	DefaultTLSKeyFile        = ""
	DefaultMaxRequestBody    = 100 * 1024 * 1024
)

// Config holds runtime settings.
type Config struct {
	// Network
	HTTPAddr           string
	MaxRequestBodySize int64
	TrustedProxies     []string
	TLSCertFile        string
	TLSKeyFile         string

	// Authentication
	AuthUsername        string
	AuthPasswordHash    string
	AuthSessionCookie   string
	AuthSessionLifetime time.Duration
	AuthCookieKey       []byte

	// Login rate limiting (per client IP)
	LoginRateMaxAttempts int
	LoginRateWindow      time.Duration
	LoginRateBlock       time.Duration

	// Filesystem locations
	ProjectsRoot string

	// Terminal stream tuning
	StreamHeartbeatInterval time.Duration
	StreamMinFrameInterval  time.Duration
	TerminalHistoryLimit    int
	MinTerminalCols         int
	MinTerminalRows         int
	MaxTerminalCols         int
	MaxTerminalRows         int
	SnapshotCacheTTL        time.Duration
}

// Options carries the raw startup flag values before normalization.
type Options struct {
	HTTPAddr           string
	ProjectsDir        string
	AuthUsername       string
	AuthPasswordHash   string
	SessionCookieName  string
	SessionCookieKey   string
	TrustedProxies     string
	TLSCertFile        string
	TLSKeyFile         string
	MaxRequestBodySize int64
}

// Load applies sensible defaults and normalizes startup values.
func Load(opts Options) (Config, error) {
	var err error

	authUsername := strings.TrimSpace(opts.AuthUsername)
	if authUsername == "" {
		authUsername = DefaultAuthUsername
	}

	authPasswordHash := strings.TrimSpace(opts.AuthPasswordHash)
	if authPasswordHash == "" {
		authPasswordHash = DefaultAuthPasswordHash
	}

	sessionCookieName := strings.TrimSpace(opts.SessionCookieName)
	if sessionCookieName == "" {
		sessionCookieName = DefaultSessionCookieName
	}

	sessionCookieKey := strings.TrimSpace(opts.SessionCookieKey)
	if sessionCookieKey == "" {
		sessionCookieKey = DefaultSessionCookieKey
	}

	httpAddr := strings.TrimSpace(opts.HTTPAddr)
	if httpAddr == "" {
		httpAddr = DefaultHTTPAddr
	}

	tlsCertFile := strings.TrimSpace(opts.TLSCertFile)
	tlsKeyFile := strings.TrimSpace(opts.TLSKeyFile)
	if (tlsCertFile == "") != (tlsKeyFile == "") {
		return Config{}, errors.New("both TLS cert file and TLS key file must be set")
	}
	if tlsCertFile != "" {
		tlsCertFile, err = filesystem.ExpandHome(tlsCertFile)
		if err != nil {
			return Config{}, err
		}
		tlsKeyFile, err = filesystem.ExpandHome(tlsKeyFile)
		if err != nil {
			return Config{}, err
		}
	}

	projectsDir := strings.TrimSpace(opts.ProjectsDir)
	if projectsDir == "" {
		projectsDir = DefaultProjectsDir
	}
	if opts.MaxRequestBodySize <= 0 {
		return Config{}, errors.New("max request body size must be greater than zero")
	}

	projectsRoot, err := filesystem.ExpandHome(projectsDir)
	if err != nil {
		return Config{}, err
	}
	return Config{
		HTTPAddr:                httpAddr,
		MaxRequestBodySize:      opts.MaxRequestBodySize,
		TrustedProxies:          parseTrustedProxies(opts.TrustedProxies),
		TLSCertFile:             tlsCertFile,
		TLSKeyFile:              tlsKeyFile,
		AuthUsername:            authUsername,
		AuthPasswordHash:        authPasswordHash,
		AuthSessionCookie:       sessionCookieName,
		AuthSessionLifetime:     time.Hour * 24 * 365,
		AuthCookieKey:           []byte(sessionCookieKey),
		LoginRateMaxAttempts:    3,
		LoginRateWindow:         5 * time.Minute,
		LoginRateBlock:          15 * time.Second,
		ProjectsRoot:            projectsRoot,
		StreamHeartbeatInterval: 1 * time.Second,
		StreamMinFrameInterval:  33 * time.Millisecond, // ~30fps coalescing cap
		TerminalHistoryLimit:    10000,
		MinTerminalCols:         2,
		MinTerminalRows:         30,
		MaxTerminalCols:         1000,
		MaxTerminalRows:         1000,
		SnapshotCacheTTL:        1500 * time.Millisecond,
	}, nil
}

func parseTrustedProxies(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	proxies := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		proxies = append(proxies, part)
	}

	return proxies
}
