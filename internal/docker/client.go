package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// apiVersion is the Engine API version every request is pinned to. It is old
// enough for any daemon still in use and carries everything this package
// asks for.
const apiVersion = "v1.41"

// maxBodySize caps what a single API answer may occupy, a guard against a
// runaway container list or log, not a limit a healthy answer reaches.
const maxBodySize = 8 << 20

// Client is a minimal Docker Engine API client over one host, enough for a
// container list, the lifecycle actions, logs and the event stream. It keeps
// no request timeout of its own, the event stream is a request that never
// ends, so every call runs under the caller's context.
type Client struct {
	host string
	base string
	http *http.Client
}

// NewClient prepares a client for host, which is a docker style endpoint:
// unix:// for a socket, tcp:// or http:// for a plain TCP daemon, https://
// for a TLS one.
func NewClient(host string) (*Client, error) {
	switch {
	case strings.HasPrefix(host, "unix://"):
		path := strings.TrimPrefix(host, "unix://")
		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", path)
			},
		}
		return &Client{host: host, base: "http://docker", http: &http.Client{Transport: transport}}, nil
	case strings.HasPrefix(host, "tcp://"):
		return &Client{host: host, base: "http://" + strings.TrimPrefix(host, "tcp://"), http: &http.Client{}}, nil
	case strings.HasPrefix(host, "http://"), strings.HasPrefix(host, "https://"):
		return &Client{host: host, base: host, http: &http.Client{}}, nil
	default:
		return nil, fmt.Errorf("unsupported docker host %q", host)
	}
}

// ValidateHost checks an address typed as the docker-host setting. It is
// stricter than NewClient on purpose: the same value travels to the docker CLI
// as DOCKER_HOST for every compose run and container shell, and the CLI reads
// unix:// and tcp:// but not http://, so an address only the API client could
// use would pass the form and then fail on every CLI surface.
func ValidateHost(host string) error {
	if !strings.HasPrefix(host, "unix://") && !strings.HasPrefix(host, "tcp://") {
		return fmt.Errorf("unsupported docker host %q", host)
	}
	_, err := NewClient(host)
	return err
}

// Host answers the endpoint the client talks to.
func (c *Client) Host() string { return c.host }

func (c *Client) do(ctx context.Context, method, path string, query url.Values) (*http.Response, error) {
	target := c.base + "/" + apiVersion + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 200 && res.StatusCode < 400 {
		return res, nil
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	var apiErr struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &apiErr) == nil && apiErr.Message != "" {
		return nil, fmt.Errorf("docker: %s", apiErr.Message)
	}
	return nil, fmt.Errorf("docker: %s %s answered %s", method, path, res.Status)
}

// Ping asks the daemon whether it is there at all.
func (c *Client) Ping(ctx context.Context) error {
	res, err := c.do(ctx, http.MethodGet, "/_ping", nil)
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

// Containers lists every container, running or not, reduced to what the
// cache carries.
func (c *Client) Containers(ctx context.Context) ([]Container, error) {
	query := url.Values{"all": {"1"}}
	res, err := c.do(ctx, http.MethodGet, "/containers/json", query)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var raw []struct {
		ID      string   `json:"Id"`
		Names   []string `json:"Names"`
		Image   string   `json:"Image"`
		State   string   `json:"State"`
		Status  string   `json:"Status"`
		Labels  map[string]string
		Created int64
		Ports   []struct {
			IP          string `json:"IP"`
			PrivatePort int    `json:"PrivatePort"`
			PublicPort  int    `json:"PublicPort"`
			Type        string `json:"Type"`
		}
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxBodySize)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("docker: reading container list: %w", err)
	}
	out := make([]Container, 0, len(raw))
	for _, entry := range raw {
		container := Container{
			ID:         entry.ID,
			Image:      entry.Image,
			State:      entry.State,
			Status:     entry.Status,
			Health:     healthOf(entry.Status),
			Project:    entry.Labels["com.docker.compose.project"],
			Service:    entry.Labels["com.docker.compose.service"],
			WorkingDir: entry.Labels["com.docker.compose.project.working_dir"],
			// The labels ride along whole: a link rule reads the container's
			// address out of them, and which rule that is may change without
			// the daemon saying anything.
			Labels:  entry.Labels,
			Created: entry.Created,
		}
		if len(entry.Names) > 0 {
			container.Name = strings.TrimPrefix(entry.Names[0], "/")
		}
		seen := map[Port]bool{}
		for _, raw := range entry.Ports {
			if raw.PublicPort == 0 {
				continue
			}
			port := Port{Public: raw.PublicPort, Private: raw.PrivatePort}
			if raw.Type != "" && raw.Type != "tcp" {
				port.Proto = raw.Type
			}
			if !seen[port] {
				seen[port] = true
				container.Ports = append(container.Ports, port)
			}
		}
		sort.Slice(container.Ports, func(i, j int) bool {
			return container.Ports[i].Public < container.Ports[j].Public
		})
		out = append(out, container)
	}
	sortContainers(out)
	return out, nil
}

// healthOf pulls the health word out of a status line like
// "Up 3 hours (healthy)", the list endpoint carries it nowhere else.
func healthOf(status string) string {
	open := strings.LastIndexByte(status, '(')
	if open < 0 || !strings.HasSuffix(status, ")") {
		return ""
	}
	word := status[open+1 : len(status)-1]
	switch word {
	case "healthy", "unhealthy", "health: starting":
		return word
	}
	return ""
}

// Start starts a container. A container already running answers 304, which
// the request layer treats as success.
func (c *Client) Start(ctx context.Context, id string) error {
	return c.lifecycle(ctx, id, "start")
}

// Stop stops a container, with the daemon's default grace period.
func (c *Client) Stop(ctx context.Context, id string) error {
	return c.lifecycle(ctx, id, "stop")
}

// Restart restarts a container.
func (c *Client) Restart(ctx context.Context, id string) error {
	return c.lifecycle(ctx, id, "restart")
}

func (c *Client) lifecycle(ctx context.Context, id, action string) error {
	if id == "" {
		return fmt.Errorf("docker: empty container id")
	}
	res, err := c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/"+action, nil)
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

// Events opens the daemon's event stream, filtered to container events. The
// caller owns the body and ends the stream by cancelling the context.
func (c *Client) Events(ctx context.Context) (io.ReadCloser, error) {
	filters, _ := json.Marshal(map[string][]string{"type": {"container"}})
	query := url.Values{"filters": {string(filters)}}
	res, err := c.do(ctx, http.MethodGet, "/events", query)
	if err != nil {
		return nil, err
	}
	return res.Body, nil
}
