package coder

import (
	"fmt"
	"strings"

	"github.com/marein/dev-cockpit/internal/filesystem"
)

// CockpitGitSkillID names the one skill the cockpit writes itself: the coder
// side of the git proxy. The id is the marker, the skills UI renders it
// locked and the handlers refuse to edit or delete it.
const CockpitGitSkillID = "dev-cockpit-git"

// IsManagedSkill reports whether a skill id belongs to the cockpit. Managed
// skills are written at start and kept current by the serve process; the
// settings pages show them with that note and let nobody edit, overwrite or
// delete them there.
func IsManagedSkill(id string) bool {
	return strings.TrimSpace(id) == CockpitGitSkillID
}

// managedSkillMark says a skill on the disk is the cockpit's own, and the line
// behind it names the instance it belongs to. Both do work.
//
// The mark is the only proof of authorship there is, and the id is none:
// somebody may have a skill of their own under that name, and a start that
// took the name for granted would rename that skill's directory, replace its
// text, and the stop would delete the whole folder with everything else in it.
//
// The owner line tells two instances sharing one coder home apart, which is
// the normal case on a machine running a throwaway beside the real one. The
// skill directory is a single slot, and the instance serving from it keeps it:
// the other one leaves it alone instead of pointing every coder of the running
// instance at a socket that is about to disappear, and its stop leaves it
// alone too instead of deleting the running instance's skill.
//
// It is a whole line of its own so the directory can be read back out of it,
// which is what CockpitInstance.Running is asked about.
const (
	managedSkillMark      = "This skill is written and kept current by dev-cockpit."
	managedSkillOwnerLead = "Owning instance state directory: "
)

func managedSkillOwner(stateDir string) string {
	return managedSkillOwnerLead + filesystem.AbsDir(stateDir)
}

// managedSkillOwnerDir reads the owning instance's state directory back out of
// a skill's text. Not ok means the skill is not the cockpit's: either the mark
// is missing or the line that names the owner is.
func managedSkillOwnerDir(instructions string) (string, bool) {
	if !strings.Contains(instructions, managedSkillMark) {
		return "", false
	}
	_, rest, ok := strings.Cut(instructions, managedSkillOwnerLead)
	if !ok {
		return "", false
	}
	line, _, _ := strings.Cut(rest, "\n")
	line = strings.TrimSpace(line)
	return line, line != ""
}

// CockpitInstance is the instance a managed skill is rendered for and belongs
// to: what to run, which cockpit it reaches, and the one question that cannot
// be answered from a file.
//
// Running answers whether another instance is still serving from a state
// directory, and it is what tells the two cases apart that look identical on
// the disk: a second cockpit running right now beside this one, and this same
// cockpit restarted with a different --state-dir. The first one owns the skill
// and keeps it, the second left nothing behind but the mark of a state
// directory nobody serves from any more, and that one is taken over. Nil reads
// as nobody serving, so a caller that does not ask takes the skill over.
type CockpitInstance struct {
	Executable string
	StateDir   string
	Running    func(stateDir string) bool
}

func (i CockpitInstance) running(stateDir string) bool {
	return i.Running != nil && i.Running(stateDir)
}

// gitProxyCommand spells the proxy the way a coder has to call it, the
// assistant instructions' rule (Workspace.CockpitCommand): the absolute path
// of the running binary and the state directory of this instance, because a
// bare `dev-cockpit` depends on a PATH a session does not control, and on a
// machine with several instances it would reach whichever one owns the
// default state directory. The flag stands before the git arguments, which is
// where the command carries it.
func gitProxyCommand(inst CockpitInstance) string {
	exe := strings.TrimSpace(inst.Executable)
	if exe == "" {
		exe = "dev-cockpit"
	}
	var b strings.Builder
	b.WriteString(shellArg(exe) + " git")
	// The state directory is the whole address: it names which cockpit
	// answers. The projects root is deliberately not here, the server holds
	// it and resolves the working directory itself.
	if dir := strings.TrimSpace(inst.StateDir); dir != "" {
		fmt.Fprintf(&b, " --state-dir %s", shellArg(dir))
	}
	return b.String()
}

// shellArg spells one value the way it has to be typed. Neither of the two
// here is this package's choice: the binary is wherever it was installed or
// moved to, the state directory is a start flag, and an installation under a
// path with a space renders a line that falls apart into other arguments the
// moment a coder runs it, which is the same reason askpass.helperScript quotes
// the path it bakes into the helper. An ordinary value stays as it is, this is
// a line somebody reads and copies.
func shellArg(value string) string {
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("-_./:=@+,", r):
		default:
			return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
		}
	}
	return value
}

// cockpitGitSkill renders the skill from the running configuration, so the
// text always carries the concrete command of this instance and a changed
// flag or a moved binary reaches every coder with the next start. The
// description is what the coder matches a task against, so it names the
// operations and the condition: remote git needs the cockpit when the key
// asks for a passphrase, because only the cockpit can ask the person.
func cockpitGitSkill(inst CockpitInstance) (description, instructions string) {
	command := gitProxyCommand(inst)
	description = "Run git commands that reach a remote (push, pull, fetch, clone, ls-remote, " +
		"anything touching the network) through the running dev-cockpit instead of plain " +
		"git. Required when the ssh key has a passphrase: only the cockpit can ask for it, " +
		"in the user's browser, and plain git would hang on a prompt nobody here can answer."
	instructions = "When the ssh key has a passphrase, this terminal cannot answer the\n" +
		"prompt and the passphrase must not be typed into a coder session. Run\n" +
		"anything that reaches a remote through the cockpit instead, in the\n" +
		"current directory, like plain git:\n" +
		"\n" +
		"    " + command + " <git arguments>\n" +
		"\n" +
		"So `push`, `fetch`, `clone <url> .` (the project dir is the project),\n" +
		"`ls-remote`, and whatever else talks to a remote. Output and exit code\n" +
		"are git's own; a cancelled or unanswered question fails the command in\n" +
		"git's words, report that instead of retrying.\n" +
		"\n" +
		"The subcommand comes first, its options behind it. Git's own options\n" +
		"(`-c`, `-C`, `--git-dir`) are refused: they could point git at another\n" +
		"program or repository than the dialog names. Local work (status, add,\n" +
		"commit, log, diff) asks nothing and stays plain git.\n" +
		"\n" +
		managedSkillMark + " Do not edit.\n" +
		managedSkillOwner(inst.StateDir)
	return description, instructions
}

// EnsureManagedSkills writes the cockpit's own skills into one coder's global
// skill directory and brings an outdated copy up to date. It runs at start
// and renders from the running configuration, so a new wording, a moved
// binary or changed start flags reach every coder without anybody doing
// anything; an unchanged skill writes nothing.
//
// What it writes over is only ever the cockpit's own (managedSkillMark).
// Somebody's own skill under that name stays untouched, and the refusal says
// so: this may take a skill of theirs neither over nor away. A copy somebody
// edited still carries the mark and is rewritten, which is what "kept current"
// means; one whose mark is gone is not recognisable as the cockpit's any more
// and is left alone.
//
// A skill of another instance is taken over only when that instance stopped
// serving. Both cases look the same on the disk, and only one of them may be
// walked over: a cockpit running right now beside this one keeps the slot,
// while a state directory nobody answers from is the leftover of a start with
// other flags, and refusing that one would leave every coder without the skill
// until somebody cleaned up by hand.
func EnsureManagedSkills(repo SkillRepository, inst CockpitInstance) error {
	description, instructions := cockpitGitSkill(inst)
	original := ""
	if existing, err := repo.Find(CockpitGitSkillID); err == nil {
		owner, ours := managedSkillOwnerDir(existing.Instructions)
		switch {
		case !ours:
			return fmt.Errorf("a skill %q that is not the cockpit's is installed; it stays as it is", CockpitGitSkillID)
		case owner != filesystem.AbsDir(inst.StateDir) && inst.running(owner):
			return fmt.Errorf("the skill %q belongs to the cockpit serving from %s; it stays as it is", CockpitGitSkillID, owner)
		case existing.Description == description && existing.Instructions == strings.TrimSpace(instructions):
			return nil
		}
		original = existing.ID
	}
	_, err := repo.Save(original, CockpitGitSkillID, description, instructions)
	return err
}

// RemoveManagedSkills takes the cockpit's own skills off the disk again. It
// runs when the cockpit stops, because the skill tells a coder to reach a
// cockpit that is about to stop answering: the command needs the local API
// socket of a running instance, so a skill that outlived the process would
// send every coder down a path that cannot work. The skill is rendered state
// and no configuration of anybody's, so removing it loses nothing, and the
// next start writes it again.
//
// A skill that is not there is not an error: a start that could not write it,
// a stop after a previous one already cleaned up, and a coder installed while
// the cockpit ran all end here with nothing to do. Neither is a skill that is
// not this instance's, and that one is the case worth naming: somebody's own
// skill under this name, or the skill of the instance still running next to
// this one, which a throwaway's stop would otherwise delete out from under
// every coder of the real instance.
func RemoveManagedSkills(repo SkillRepository, inst CockpitInstance) error {
	existing, err := repo.Find(CockpitGitSkillID)
	if err != nil {
		return nil
	}
	if owner, ours := managedSkillOwnerDir(existing.Instructions); !ours || owner != filesystem.AbsDir(inst.StateDir) {
		return nil
	}
	_, err = repo.Delete(CockpitGitSkillID)
	return err
}
