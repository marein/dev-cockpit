package voice

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The container tests run the real path: image built from the shipped build
// file, engine warmed in its container, models in the cache bind. They skip
// where docker is missing, which is also what keeps them out of the
// containerized build. The state directory is a fixed per host cache
// location, so the model downloads happen once per host, not once per run;
// its own root label keeps the sweep away from a live instance's engines.
var testService *Service

func service(t *testing.T) *Service {
	t.Helper()
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker is not available on this host")
	}
	if err := exec.Command(dockerPath, "info").Run(); err != nil {
		t.Skip("the docker daemon does not answer")
	}
	if testService == nil {
		base, err := os.UserCacheDir()
		if err != nil {
			base = os.TempDir()
		}
		testService = New(filepath.Join(base, "dev-cockpit-voice-test"), nil, nil, nil)
		testService.SweepStale()
	}
	return testService
}

// TestMain stops the test engines: their idle timers die with this process,
// so without the close they would stand until the next run's sweep.
func TestMain(m *testing.M) {
	code := m.Run()
	if testService != nil {
		testService.Close()
	}
	os.Exit(code)
}

// The fixtures are short espeak-ng sentences as webm/opus, the format
// MediaRecorder produces. Whisper detects the language per utterance, so both
// go through the same call with no language setting anywhere.
func TestTranscribeGermanAndEnglish(t *testing.T) {
	svc := service(t)
	for _, tc := range []struct {
		file     string
		language string
		keyword  string
	}{
		{"testdata/de.webm", "de", "editor"},
		{"testdata/en.webm", "en", "editor"},
	} {
		clip, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatalf("read %s: %v", tc.file, err)
		}
		out, err := svc.Transcribe(context.Background(), clip)
		if err != nil {
			t.Fatalf("transcribe %s: %v", tc.file, err)
		}
		if out.Language != tc.language {
			t.Errorf("%s: detected language %q, want %q (text %q)", tc.file, out.Language, tc.language, out.Text)
		}
		if !strings.Contains(strings.ToLower(out.Text), tc.keyword) {
			t.Errorf("%s: transcript %q misses %q", tc.file, out.Text, tc.keyword)
		}
	}
}

func TestSynthesizeGermanAndEnglish(t *testing.T) {
	svc := service(t)
	for _, tc := range []struct {
		language string
		text     string
	}{
		{"de", "Guten Morgen. Die Sonne scheint und der Kaffee ist fertig."},
		{"en", "Good morning. The build is green and the tests are passing."},
	} {
		wav, err := svc.Synthesize(context.Background(), tc.text, tc.language)
		if err != nil {
			t.Fatalf("synthesize %s: %v", tc.language, err)
		}
		if len(wav) < 44 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
			t.Fatalf("%s: the answer is no wav (%d bytes)", tc.language, len(wav))
		}
		// A sentence of that length is seconds of audio; a header with next to
		// nothing behind it means the voice said nothing.
		if len(wav) < 20000 {
			t.Errorf("%s: suspiciously short audio, %d bytes", tc.language, len(wav))
		}
	}
}

// An engine's option is an id out of its own fixed list and nothing else:
// absent, unknown and a value from another version all read as the profile's
// default, so a settings file can never send a container after something this
// binary does not ship.
func TestProfileNormalize(t *testing.T) {
	for _, p := range profiles {
		if got := p.Normalize(""); got != p.Default {
			t.Errorf("%s: empty read as %q, want the default %q", p.ID, got, p.Default)
		}
		if got := p.Normalize("evil/repo"); got != p.Default {
			t.Errorf("%s: unknown value read as %q, want the default %q", p.ID, got, p.Default)
		}
		for _, o := range p.Options {
			if got := p.Normalize(o.ID); got != o.ID {
				t.Errorf("%s: offered %q read as %q", p.ID, o.ID, got)
			}
		}
		if p.Normalize(p.Default) != p.Default {
			t.Errorf("%s: its own default %q is not one of its options", p.ID, p.Default)
		}
	}
}

// A service without a settings reader runs every engine on its default, which
// is what the container tests and a server wired without settings get.
func TestOptionWithoutSettings(t *testing.T) {
	svc := New(t.TempDir(), nil, nil, nil)
	if got := svc.Option(Whisper()); got != Whisper().Default {
		t.Errorf("whisper option %q, want %q", got, Whisper().Default)
	}
	stored := map[string]string{VoiceSettingKey: "female", ModelSettingKey: "nonsense"}
	svc = New(t.TempDir(), nil, func(key string) string { return stored[key] }, nil)
	if got := svc.Option(Piper()); got != "female" {
		t.Errorf("piper option %q, want the stored female", got)
	}
	if got := svc.Option(Whisper()); got != Whisper().Default {
		t.Errorf("whisper option %q, want the default for a stored nonsense value", got)
	}
}

// The detector runs without docker: it decides which voice reads an answer,
// out of the two the engine has voices for.
func TestDetectLanguage(t *testing.T) {
	if got := DetectLanguage("Bitte starte den Server neu und prüfe danach die Logdatei."); got != "de" {
		t.Errorf("german text detected as %q", got)
	}
	if got := DetectLanguage("Please restart the server and check the log file afterwards."); got != "en" {
		t.Errorf("english text detected as %q", got)
	}
}
