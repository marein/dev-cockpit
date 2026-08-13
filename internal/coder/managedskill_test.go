package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testInstance is one cockpit with nothing else serving beside it, which is
// the ordinary machine.
func testInstance(stateDir string) CockpitInstance {
	return CockpitInstance{
		Executable: "/opt/dc/dev-cockpit",
		StateDir:   stateDir,
	}
}

// besideRunning is that instance with another cockpit still answering from
// serving, which is a throwaway started next to the real one.
func besideRunning(stateDir, serving string) CockpitInstance {
	instance := testInstance(stateDir)
	instance.Running = func(dir string) bool { return dir == serving }
	return instance
}

// flatten folds a wrapped text into one line, so a check for a phrase does
// not depend on where the wrapping happened to break it.
func flatten(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func TestEnsureManagedSkillsWritesTheGitSkillFromTheConfiguration(t *testing.T) {
	dir := t.TempDir()
	repo := NewStandardSkillRepository(dir)

	if err := EnsureManagedSkills(repo, testInstance("/var/dc-state")); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	skill, err := repo.Find(CockpitGitSkillID)
	if err != nil {
		t.Fatalf("the skill was not written: %v", err)
	}
	want := "/opt/dc/dev-cockpit git --state-dir /var/dc-state"
	if !strings.Contains(skill.Instructions, want) {
		t.Fatalf("the instructions do not carry the instance's own command %q:\n%s", want, skill.Instructions)
	}
	if !strings.Contains(skill.Description, "passphrase") {
		t.Fatalf("the description does not name the condition: %s", skill.Description)
	}
	// The skill is injected into every coder and read on every start, so it is
	// kept short on purpose. What may never fall out of it while it is being
	// shortened is checked here, one line per thing that has to survive.
	both := flatten(skill.Description) + " " + flatten(skill.Instructions)
	for what, phrase := range map[string]string{
		"the condition it is needed under": "passphrase",
		"the rule, which is the transport": "reach a remote",
		"that it is not a list of verbs":   "whatever else talks to a remote",
		"local work staying plain git":     "stays plain git",
		"the refused options":              "`-c`, `-C`, `--git-dir`",
		"that they are refused":            "are refused",
		"the managed note":                 managedSkillMark,
	} {
		if !strings.Contains(both, phrase) {
			t.Fatalf("the skill lost %s (%q):\n%s", what, phrase, both)
		}
	}
	// Examples stay examples, and the ones past the obvious two are what show
	// that the rule is the transport and not a set of subcommands.
	for _, example := range []string{"push", "pull", "fetch", "clone", "ls-remote"} {
		if !strings.Contains(both, example) {
			t.Fatalf("the skill gives no example of %s", example)
		}
	}
}

// A second start with the same configuration writes nothing, a changed flag
// rewrites, and a tampered copy is brought back: the skill is rendered state,
// not a file anybody maintains.
func TestEnsureManagedSkillsKeepsTheSkillCurrent(t *testing.T) {
	dir := t.TempDir()
	repo := NewStandardSkillRepository(dir)
	if err := EnsureManagedSkills(repo, testInstance("/var/dc-state")); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	path := filepath.Join(dir, CockpitGitSkillID, "SKILL.md")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := EnsureManagedSkills(repo, testInstance("/var/dc-state")); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Fatal("an unchanged configuration must write nothing")
	}

	if err := EnsureManagedSkills(repo, testInstance("/var/other-state")); err != nil {
		t.Fatalf("ensure with moved flags: %v", err)
	}
	skill, err := repo.Find(CockpitGitSkillID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(skill.Instructions, "--state-dir /var/other-state") {
		t.Fatal("changed start flags must reach the skill on the next start")
	}

	// A copy somebody edited still says whose it is, so it is brought back:
	// the skill is rendered state and not a file anybody maintains.
	tampered := "---\nname: dev-cockpit-git\ndescription: edited\n---\n\nedited\n\n" +
		managedSkillMark + "\n" + managedSkillOwner("/var/other-state") + "\n"
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureManagedSkills(repo, testInstance("/var/other-state")); err != nil {
		t.Fatalf("ensure over a tampered copy: %v", err)
	}
	skill, err = repo.Find(CockpitGitSkillID)
	if err != nil {
		t.Fatal(err)
	}
	if skill.Description == "edited" || !strings.Contains(skill.Instructions, "--state-dir /var/other-state") {
		t.Fatal("a tampered copy must be rewritten")
	}
}

// The id is no proof of authorship: somebody may have a skill of their own
// under that name. Taking it over would rename its directory, replace its
// text, and the stop would delete the folder with everything else in it.
func TestEnsureManagedSkillsLeavesSomebodyElsesSkillAlone(t *testing.T) {
	dir := t.TempDir()
	repo := NewStandardSkillRepository(dir)
	if _, err := repo.Save("", CockpitGitSkillID, "my own git helper", "do it my way"); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := EnsureManagedSkills(repo, testInstance("/var/dc-state")); err == nil {
		t.Fatal("a skill that is not the cockpit's has to be refused, not taken over")
	}
	skill, err := repo.Find(CockpitGitSkillID)
	if err != nil {
		t.Fatal(err)
	}
	if skill.Description != "my own git helper" || skill.Instructions != "do it my way" {
		t.Fatalf("somebody else's skill was overwritten: %+v", skill)
	}

	// And the stop leaves it where it is instead of deleting the directory.
	if err := RemoveManagedSkills(repo, testInstance("/var/dc-state")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := repo.Find(CockpitGitSkillID); err != nil {
		t.Fatalf("somebody else's skill was removed: %v", err)
	}
}

// A coder home is shared by every cockpit on the machine, so the skill
// directory is a single slot for all of them. The instance still serving keeps
// it: a throwaway next to the real instance may neither point every coder at
// its own socket nor take the skill away when it stops.
func TestManagedSkillsBelongToTheInstanceThatWroteThem(t *testing.T) {
	dir := t.TempDir()
	repo := NewStandardSkillRepository(dir)
	if err := EnsureManagedSkills(repo, testInstance("/var/dc-state")); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	throwaway := besideRunning("/tmp/throwaway-state", "/var/dc-state")
	if err := EnsureManagedSkills(repo, throwaway); err == nil {
		t.Fatal("a second instance must not rewrite the running one's skill")
	}
	skill, err := repo.Find(CockpitGitSkillID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(skill.Instructions, "--state-dir /var/dc-state") {
		t.Fatalf("the throwaway pointed the coders at itself:\n%s", skill.Instructions)
	}

	if err := RemoveManagedSkills(repo, throwaway); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := repo.Find(CockpitGitSkillID); err != nil {
		t.Fatalf("the throwaway's stop took the running instance's skill: %v", err)
	}
	if err := RemoveManagedSkills(repo, testInstance("/var/dc-state")); err != nil {
		t.Fatalf("remove by the owner: %v", err)
	}
	if _, err := repo.Find(CockpitGitSkillID); err == nil {
		t.Fatal("the owning instance's stop has to take its own skill")
	}
}

// The command is a line somebody copies into a shell, so a directory with a
// space in it may not fall apart into two arguments there.
func TestGitProxyCommandQuotesWhatAShellWouldSplit(t *testing.T) {
	plain := gitProxyCommand(testInstance("/var/dc-state"))
	if plain != "/opt/dc/dev-cockpit git --state-dir /var/dc-state" {
		t.Fatalf("an ordinary path may not be quoted: %s", plain)
	}
	spaced := gitProxyCommand(CockpitInstance{
		Executable: "/opt/My Cockpit/dev-cockpit",
		StateDir:   "/home/me/My State",
	})
	want := `'/opt/My Cockpit/dev-cockpit' git --state-dir '/home/me/My State'`
	if spaced != want {
		t.Fatalf("gitProxyCommand = %s, want %s", spaced, want)
	}
}

// The skill points a coder at the local API socket of a running instance, so
// one left behind after the stop would send every coder down a path that
// cannot answer. Removing it must leave everybody else's skills alone.
func TestRemoveManagedSkillsTakesOnlyTheCockpitsOwn(t *testing.T) {
	dir := t.TempDir()
	repo := NewStandardSkillRepository(dir)
	if _, err := repo.Save("", "mine", "a skill of my own", "do the thing"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := EnsureManagedSkills(repo, testInstance("/var/dc-state")); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	if err := RemoveManagedSkills(repo, testInstance("/var/dc-state")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := repo.Find(CockpitGitSkillID); err == nil {
		t.Fatal("the managed skill stayed on the disk")
	}
	if _, err := os.Stat(filepath.Join(dir, CockpitGitSkillID)); !os.IsNotExist(err) {
		t.Fatal("the managed skill's directory stayed behind")
	}
	if _, err := repo.Find("mine"); err != nil {
		t.Fatalf("removing took somebody else's skill with it: %v", err)
	}

	// A stop after a start that never wrote it, and a second stop, both have
	// nothing to do rather than something to fail on.
	if err := RemoveManagedSkills(repo, testInstance("/var/dc-state")); err != nil {
		t.Fatalf("removing what is not there must be no error: %v", err)
	}

	// And the next start puts it back, which is what makes removing it safe.
	if err := EnsureManagedSkills(repo, testInstance("/var/dc-state")); err != nil {
		t.Fatalf("ensure after remove: %v", err)
	}
	if _, err := repo.Find(CockpitGitSkillID); err != nil {
		t.Fatalf("the next start did not write it again: %v", err)
	}
}

func TestIsManagedSkill(t *testing.T) {
	if !IsManagedSkill(CockpitGitSkillID) || !IsManagedSkill(" dev-cockpit-git ") {
		t.Fatal("the cockpit's own id must read as managed")
	}
	if IsManagedSkill("my-skill") || IsManagedSkill("") {
		t.Fatal("nobody else's id may read as managed")
	}
}
