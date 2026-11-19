package render

import (
	"strings"
	"testing"
)

// The label is display text, so a brand casing the plain capitalization
// cannot produce is special cased, while the ids stay lowercase everywhere
// they are ids.
func TestCoderLabelUsesTheBrandCasing(t *testing.T) {
	cases := map[string]string{
		"claude":   "Claude",
		"copilot":  "Copilot",
		"opencode": "OpenCode",
		"":         "",
	}
	for id, want := range cases {
		if got := CoderLabel(id); got != want {
			t.Errorf("CoderLabel(%q) = %q, want %q", id, got, want)
		}
	}
}

// The notice is where a flash names a session, a shell or a project, and the
// name stands in double quotes the way the handlers write it. The template
// prints the message as text, so the quotes survive and markup in a name
// cannot become markup.
func TestNoticeKeepsQuotedNamesAndEscapes(t *testing.T) {
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	var out strings.Builder
	err := tmpl.ExecuteTemplate(&out, "notice.gohtml", map[string]any{
		"Level":   "success",
		"Message": `Shell "a <b>" deleted.`,
		"Dismiss": true,
	})
	if err != nil {
		t.Fatalf("render notice: %v", err)
	}
	if got := out.String(); !strings.Contains(got, `Shell &#34;a &lt;b&gt;&#34; deleted.`) {
		t.Fatalf("notice output misses the quoted, escaped name: %q", got)
	}
}

// Where the cockpit runs no agent, the key list says why: a refused socket
// directory by its path, missing OpenSSH tools with the text they always had.
func TestTheSSHKeysSayWhyTheAgentIsOff(t *testing.T) {
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	keys := func(v SSHAgentView) string {
		t.Helper()
		var out strings.Builder
		if err := tmpl.ExecuteTemplate(&out, "settings_ssh_agent_keys.gohtml", v); err != nil {
			t.Fatalf("render the keys: %v", err)
		}
		return out.String()
	}
	refused := keys(SSHAgentView{Refused: "/tmp/dcs/ssh-agent"})
	if !strings.Contains(refused, "data-ssh-agent-refused") || !strings.Contains(refused, `<code class="text-break">/tmp/dcs/ssh-agent</code>`) ||
		strings.Contains(refused, "data-ssh-agent-missing") || strings.Contains(refused, "were not all found") {
		t.Fatalf("a refused directory reads %q", refused)
	}
	const missing = `<div class="alert alert-info mb-0" data-ssh-agent-missing>OpenSSH's <code>ssh-agent</code>, <code>ssh-add</code> and <code>ssh-keygen</code> were not all found when the cockpit started, so it runs no agent and ssh reads its keys itself.</div>`
	if got := keys(SSHAgentView{}); !strings.Contains(got, missing) {
		t.Fatalf("missing tools read %q", got)
	}
}
