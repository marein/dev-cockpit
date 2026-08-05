package docker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContainersMapsTheListEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+apiVersion+"/containers/json" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("all") != "1" {
			t.Errorf("stopped containers not requested")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"Id":"bbb","Names":["/app-web-1"],"Image":"nginx","State":"exited","Status":"Exited (0) 2 hours ago","Created":2,
			 "Labels":{"com.docker.compose.project":"app","com.docker.compose.service":"web","com.docker.compose.project.working_dir":"/p/app"},
			 "Ports":[]},
			{"Id":"aaa","Names":["/app-db-1"],"Image":"postgres","State":"running","Status":"Up 3 hours (healthy)","Created":1,
			 "Labels":{"com.docker.compose.project":"app","com.docker.compose.service":"db","com.docker.compose.project.working_dir":"/p/app"},
			 "Ports":[{"IP":"0.0.0.0","PrivatePort":5432,"PublicPort":5433,"Type":"tcp"},{"IP":"::","PrivatePort":5432,"PublicPort":5433,"Type":"tcp"},{"IP":"0.0.0.0","PrivatePort":443,"PublicPort":8443,"Type":"tcp"},{"PrivatePort":9999}]}
		]`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	list, err := client.Containers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d containers", len(list))
	}
	db := list[0]
	if db.Name != "app-db-1" || db.Service != "db" || db.WorkingDir != "/p/app" {
		t.Fatalf("mapping answered %+v", db)
	}
	if db.Health != "healthy" {
		t.Fatalf("health answered %q", db.Health)
	}
	if len(db.Ports) != 2 || db.Ports[0] != (Port{Public: 5433, Private: 5432}) || db.Ports[1] != (Port{Public: 8443, Private: 443}) {
		t.Fatalf("ports answered %v, want each published one once", db.Ports)
	}
	if db.PortsLabel() != "5433:5432, 8443:443" {
		t.Fatalf("ports label answered %q", db.PortsLabel())
	}
	// The labels arrive whole, they are what a link rule reads.
	if len(db.Labels) != 3 || db.Labels["com.docker.compose.service"] != "db" {
		t.Fatalf("labels answered %v, want the daemon's own map", db.Labels)
	}
	links := NewLinkMatcher(nil).Links(db)
	if len(links) != 2 || links[0] != (Link{Port: 5433, Scheme: "http"}) || links[1] != (Link{Port: 8443, Scheme: "https"}) {
		t.Fatalf("links answered %v, want http for plain ports and https for container port 443", links)
	}
	if list[1].Name != "app-web-1" || list[1].Health != "" {
		t.Fatalf("second entry answered %+v", list[1])
	}
}

func TestLifecycleTreatsNotModifiedAsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/"+apiVersion+"/containers/abc/start" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	if err := client.Start(context.Background(), "abc"); err != nil {
		t.Fatalf("304 answered error %v", err)
	}
}

func TestLifecycleSurfacesTheDaemonMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"No such container: abc"}`))
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	err := client.Stop(context.Background(), "abc")
	if err == nil || !strings.Contains(err.Error(), "No such container") {
		t.Fatalf("error answered %v", err)
	}
}

func TestNewClientRefusesUnknownScheme(t *testing.T) {
	if _, err := NewClient("fd://3"); err == nil {
		t.Fatal("fd scheme accepted")
	}
}

// The setting travels to the CLI as DOCKER_HOST, so only what the CLI reads
// may pass: http would leave every compose run and shell failing after the
// form said fine.
func TestValidateHostAcceptsOnlyWhatTheCLIReads(t *testing.T) {
	for _, host := range []string{"unix:///var/run/docker.sock", "tcp://box:2375"} {
		if err := ValidateHost(host); err != nil {
			t.Fatalf("%s was refused: %v", host, err)
		}
	}
	for _, host := range []string{"http://box:2375", "https://box:2376", "ssh://box", "fd://3", "box:2375"} {
		if err := ValidateHost(host); err == nil {
			t.Fatalf("%s was accepted", host)
		}
	}
}
