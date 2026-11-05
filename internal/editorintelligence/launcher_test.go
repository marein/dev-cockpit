package editorintelligence

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareGoesOnAfterAPanickingSeed(t *testing.T) {
	work := t.TempDir()
	marker := filepath.Join(work, "image-built")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	installFakeDocker(t, filepath.Join(work, "args"), marker, "", "")
	php := *profileByID("php")
	seeded := false
	php.container.SeedIndex = func(context.Context, string, *Profile, string, string) {
		seeded = true
		panic("the seed broke")
	}
	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prev) })

	err := DockerLauncher(t.TempDir(), nil).Prepare(context.Background(), work, "app", filepath.Join(work, "app"), &php)
	if err != nil || !seeded {
		t.Fatalf("Prepare = %v, seed ran %v, want no error after the seed panicked", err, seeded)
	}
	if got := logs.String(); !strings.Contains(got, "seeding the php index of app panicked") || !strings.Contains(got, "the seed broke") || !strings.Contains(got, "goroutine ") {
		t.Errorf("log = %q, want the panic with its stack", got)
	}
}
