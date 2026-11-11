package clirun

import "testing"

func TestShellWordQuotesOnlyWhatAShellReads(t *testing.T) {
	cases := map[string]string{
		"/root/files/report-2026_11.v2.png": "/root/files/report-2026_11.v2.png",
		"/root/files/Übersicht.pdf":         "/root/files/Übersicht.pdf",
		"/root/files/my shot.png":           `'/root/files/my shot.png'`,
		"/root/files/it's.txt":              `'/root/files/it'"'"'s.txt'`,
		"/root/files/$HOME;rm.txt":          `'/root/files/$HOME;rm.txt'`,
		"/root/files/~a*b?.txt":             `'/root/files/~a*b?.txt'`,
		"/root/files/tab\there":             "'/root/files/tab\there'",
		"":                                  "''",
	}
	for in, want := range cases {
		if got := ShellWord(in); got != want {
			t.Fatalf("ShellWord(%q) = %q, wanted %q", in, got, want)
		}
	}
}
