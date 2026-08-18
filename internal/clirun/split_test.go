package clirun

import (
	"strings"
	"testing"
)

func TestSplitCommandGroupsWithoutInterpreting(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"docker compose up -d", []string{"docker", "compose", "up", "-d"}},
		{"  docker   compose\tdown  ", []string{"docker", "compose", "down"}},
		{`docker compose -f "my stack.yml" up`, []string{"docker", "compose", "-f", "my stack.yml", "up"}},
		{`sh -c 'echo hi'`, []string{"sh", "-c", "echo hi"}},
		{`echo a\ b`, []string{"echo", "a b"}},
		// Nothing is expanded, so a variable and a pattern travel as text.
		{"echo $HOME *.yml", []string{"echo", "$HOME", "*.yml"}},
		// An empty argument is a real one.
		{`echo "" x`, []string{"echo", "", "x"}},
		{"", nil},
	}
	for _, c := range cases {
		got, err := SplitCommand(c.line)
		if err != nil {
			t.Fatalf("%q answered %v", c.line, err)
		}
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Fatalf("%q split into %q, wanted %q", c.line, got, c.want)
		}
	}
	for _, line := range []string{`docker "up`, `docker 'up`, `docker up\`} {
		if _, err := SplitCommand(line); err == nil {
			t.Fatalf("%q was accepted", line)
		}
	}
}
