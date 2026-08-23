// Package voice runs the speech engines behind the assistant's voice
// features: whisper for speech to text and piper for text to speech. It
// follows the editor intelligence Docker pattern: a fixed profile registry
// compiled into the binary, each image built locally from an embedded build
// file under a content hash tag, the containers running as the cockpit's own
// user with their model caches as host binds under the state directory, stale
// containers swept at boot. An engine container stays warm behind a small
// HTTP API inside and stops after an idle timeout, because a cold start per
// utterance would pay the model load every time.
package voice

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/filesystem"
)

//go:embed dockerfiles/whisper.Dockerfile
var whisperDockerfile string

//go:embed dockerfiles/piper.Dockerfile
var piperDockerfile string

// Profile is one fixed speech engine profile compiled into the binary. The
// container recipe is never configurable, so no setting can become a command
// execution surface; a setting only picks whether the fixed way runs, or not,
// and which of the profile's own fixed Options it runs with.
type Profile struct {
	// ID is the stable profile identifier, used by the settings key.
	ID string
	// Server is the short name this engine wears everywhere the cockpit
	// names something itself: the image, the container, the cache directory
	// and the stored settings value.
	Server string
	// Image is the local image repository the cockpit builds into.
	Image string
	// Dockerfile is the shipped build file the image is built from.
	Dockerfile string
	// Port is the fixed port the engine's HTTP API listens on inside the
	// container; the host side is an ephemeral publish on loopback.
	Port int
	// SettingKey is the settings key picking this engine's runtime option,
	// EnvVar the variable that option travels into the container in, Options
	// the fixed choices it may take and Default the one an absent or unknown
	// value reads as. The option is an id out of Options and nothing else:
	// the recipe stays compiled in, the value only selects between ways the
	// build file already knows, and the container falls back to the same
	// default for anything it does not know.
	SettingKey string
	EnvVar     string
	Options    []Option
	Default    string
}

// Option is one runtime choice of a profile: the stored id and what the
// settings page calls it.
type Option struct {
	ID    string
	Label string
}

// ModelSettingKey picks the speech to text model, VoiceSettingKey the speaking
// voice. Both live install wide beside the engines' own on/off keys.
const (
	ModelSettingKey = "voice-stt-model"
	VoiceSettingKey = "voice-tts-voice"
)

// profiles is the fixed registry: whisper turns speech into text, piper text
// into speech. The model choice trades accuracy for the wait after a push to
// talk press; the voice choice is one speaker for every language, so nothing
// switches gender mid conversation. Both are stored as generic ids, a size and
// a gender: which model or which voice files that is stays inside the build
// file, so swapping the concrete model for a better one of the same size never
// touches a stored setting.
var profiles = []*Profile{
	{
		ID:         "whisper",
		Server:     "whisper",
		Image:      "dev-cockpit-whisper",
		Dockerfile: whisperDockerfile,
		Port:       8035,
		SettingKey: ModelSettingKey,
		EnvVar:     "DC_WHISPER_MODEL",
		Options: []Option{
			{ID: "large", Label: "Large"},
			{ID: "medium", Label: "Medium"},
			{ID: "small", Label: "Small"},
			{ID: "base", Label: "Base"},
			{ID: "tiny", Label: "Tiny"},
		},
		// Small is the default: on a CPU only host it is the size that still
		// answers a push to talk press quickly, and it is good enough on
		// German while the larger ones make the wait the feature's problem.
		Default: "small",
	},
	{
		ID:         "piper",
		Server:     "piper",
		Image:      "dev-cockpit-piper",
		Dockerfile: piperDockerfile,
		Port:       8036,
		SettingKey: VoiceSettingKey,
		EnvVar:     "DC_PIPER_VOICE",
		Options: []Option{
			{ID: "male", Label: "Male"},
			{ID: "female", Label: "Female"},
		},
		Default: "male",
	},
}

// Normalize answers the option id to run with: the stored value when the
// profile offers it, its default for everything else. Absent and unknown read
// as the default, so a settings file from another version never picks
// something this binary does not ship.
func (p *Profile) Normalize(stored string) string {
	for _, o := range p.Options {
		if o.ID == stored {
			return stored
		}
	}
	return p.Default
}

// Whisper is the speech to text profile.
func Whisper() *Profile { return profiles[0] }

// Piper is the text to speech profile.
func Piper() *Profile { return profiles[1] }

// Detected reports whether a docker client exists on this host; the daemon's
// availability is the caller's docker cache, the same split the editor
// intelligence settings read.
func Detected() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

// voiceRootLabel marks a container as belonging to one state directory. The
// state directory is the ownership boundary: a throwaway test instance beside
// the live one shares the daemon and must never lose the live one's engines,
// so the boot sweep only reads names carrying its own root's label.
const voiceRootLabel = "dev-cockpit.voice-state-root"

// imageBuildTimeout bounds one local image build; the build installs the
// engine from its registry, so the network rides in it.
const imageBuildTimeout = 15 * time.Minute

// warmTimeout bounds one container start up to the first health answer. The
// first ever start downloads the models into the cache bind, which is most of
// this budget; a warm cache loads in seconds.
const warmTimeout = 15 * time.Minute

// requestTimeout bounds one engine round, transcription or synthesis. A clip
// is seconds and an answer minutes of audio, so minutes here mean the engine
// is wedged, not slow.
const requestTimeout = 5 * time.Minute

// idleTimeout is how long an engine stays warm after its last use. The models
// hold real memory, so an idle engine gives it back; the next use pays one
// warm start from the cache bind.
const idleTimeout = 5 * time.Minute

// maxAnswerBytes caps what one engine answer may hold; a wav of a long spoken
// answer is tens of megabytes, so the cap sits far above that.
const maxAnswerBytes = 1 << 28

// buildMu serializes local image builds, like the editor intelligence one:
// two requests warming at the same moment must wait for one build, not race
// two.
var buildMu sync.Mutex

// Transcript is one transcription answer: the text and the language whisper
// detected for the utterance.
type Transcript struct {
	Text     string
	Language string
}

// Service owns the engine containers: one per profile, warmed on first use,
// stopped after the idle timeout and on Close. All methods are safe for
// concurrent use.
type Service struct {
	stateRoot string
	cacheRoot string
	host      func() string
	setting   func(key string) string
	announce  func(engineID string)
	client    *http.Client

	mu        sync.Mutex
	running   map[string]*instance
	closed    bool
	sweepDone chan struct{}
}

// instance is one running engine container, where its API answers, its idle
// clock, and the option id it was started with. An engine loads its model
// before it answers, so the option cannot change under a running container:
// the next use compares and starts a fresh one.
type instance struct {
	profile   *Profile
	name      string
	base      string
	option    string
	lastUsed  time.Time
	idleTimer *time.Timer
}

// New builds the service. stateDir is this serve process's state directory,
// dockerHost answers the configured daemon, nil or empty for the ambient one,
// setting reads one install wide setting, nil while nothing stores any, in
// which case every profile runs its own default, and announce, nil for
// nobody listening, is told the profile id when a start is about to pay its
// one time costs, the image build and the model download, so the web layer
// can tell whoever is waiting.
func New(stateDir string, dockerHost func() string, setting func(key string) string, announce func(engineID string)) *Service {
	return &Service{
		stateRoot: filesystem.AbsDir(stateDir),
		cacheRoot: CacheRoot(stateDir),
		host:      dockerHost,
		setting:   setting,
		announce:  announce,
		client:    &http.Client{},
		running:   map[string]*instance{},
	}
}

// Option answers the option id a profile runs with right now: the stored pick
// when the profile offers it, its default otherwise. The web layer renders the
// same answer, so what the page shows and what the container gets are one
// value.
func (s *Service) Option(p *Profile) string {
	if p.SettingKey == "" {
		return ""
	}
	stored := ""
	if s.setting != nil {
		stored = s.setting(p.SettingKey)
	}
	return p.Normalize(stored)
}

// CacheRoot is where the engines' cache directories live, one place under the
// state directory this serve process owns. Resolved, because the path travels
// into a container as a mount. Deliberately not in the backup: the models are
// downloads the engines repeat, no answer of the cockpit is lost with them.
func CacheRoot(stateDir string) string {
	return filepath.Join(filesystem.AbsDir(stateDir), "voice")
}

func hostOf(host func() string) string {
	if host == nil {
		return ""
	}
	return host()
}

// dockerHostEnv is the environment a configured daemon travels in, nil for
// the ambient one.
func dockerHostEnv(host string) []string {
	if host == "" {
		return nil
	}
	return []string{"DOCKER_HOST=" + host}
}

// dockerCmd builds one docker CLI call carrying DOCKER_HOST for a configured
// daemon, so every call of the feature reaches the same daemon the
// availability gate read.
func dockerCmd(ctx context.Context, dockerPath string, env []string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, dockerPath, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd
}

// imageRef is the profile's image with a tag that is a short stable hash of
// its build file's content: a release that changes the build file misses its
// tag on existing hosts and builds on next use, while an unchanged file never
// rebuilds. The boot sweep removes tags of this scheme no shipped build file
// produces anymore.
func imageRef(p *Profile) string {
	sum := sha256.Sum256([]byte(p.Dockerfile))
	return p.Image + ":" + hex.EncodeToString(sum[:6])
}

// ensureImage makes the profile's image exist locally under the tag its
// current build file hashes to, building it from that file when the tag is
// missing. The build reads the file from stdin with an empty context, so
// nothing of the host rides into the image, and the image is never pulled
// prebuilt with an engine inside: whoever builds holds the licenses.
func ensureImage(ctx context.Context, dockerPath string, env []string, p *Profile) error {
	buildMu.Lock()
	defer buildMu.Unlock()
	ref := imageRef(p)
	if dockerCmd(ctx, dockerPath, env, "image", "inspect", ref).Run() == nil {
		return nil
	}
	log.Printf("voice: building %s for %s", ref, p.ID)
	buildCtx, cancel := context.WithTimeout(ctx, imageBuildTimeout)
	defer cancel()
	build := dockerCmd(buildCtx, dockerPath, env, "build", "-t", ref, "-")
	build.Stdin = strings.NewReader(p.Dockerfile)
	out, err := build.CombinedOutput()
	if err != nil {
		tail := string(out)
		if len(tail) > 400 {
			tail = tail[len(tail)-400:]
		}
		return fmt.Errorf("build %s: %w: %s", ref, err, strings.TrimSpace(tail))
	}
	log.Printf("voice: built %s", ref)
	return nil
}

// processUID and processGID are the identity the engine containers run as,
// this process's own ids: what an engine writes into the cache bind then
// belongs to the cockpit and comes off the disk without help.
var processUID, processGID = os.Getuid(), os.Getgid()

// containerPrefix is the naming scheme's per-engine start; the name builder
// and the boot sweep both read it, so what one creates the other recognizes.
func containerPrefix(server string) string {
	return "dev-cockpit-" + server + "-"
}

// containerName is the engine container's name. An engine is one per
// instance, not per project, so the distinguishing part is a short stable
// hash of the state root: two serve processes on one daemon never fight over
// one name, and the label scopes the sweep to this instance's own.
func (s *Service) containerName(p *Profile) string {
	sum := sha256.Sum256([]byte(s.stateRoot))
	return containerPrefix(p.Server) + hex.EncodeToString(sum[:3])
}

// cacheDir is the profile's cache directory. It lives under this instance's
// own state directory, so it needs no instance hash the way the container
// name does.
func (s *Service) cacheDir(p *Profile) string {
	return filepath.Join(s.cacheRoot, "dev-cockpit-"+p.Server)
}

// ensureCacheDir makes the directory exist before the run binds it: docker
// would create a missing bind source itself, owned by root and with no say in
// the mode. The home subdirectory comes with it, HOME points there inside the
// container, so an engine writing dotfiles lands in the mount instead of a
// home directory its uid does not have in the image; the models subdirectory
// is where DC_MODEL_DIR points.
func ensureCacheDir(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "home"), 0o755); err != nil {
		return fmt.Errorf("cache directory %s: %w", dir, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "models"), 0o755); err != nil {
		return fmt.Errorf("cache directory %s: %w", dir, err)
	}
	return nil
}

// voiceSchemeName reports whether the name belongs to the voice naming
// scheme, whatever instance hash it carries.
func voiceSchemeName(name string) bool {
	for _, p := range profiles {
		if strings.HasPrefix(name, containerPrefix(p.Server)) {
			return true
		}
	}
	return false
}

// SweepStale starts the boot sweep in the background and gates the first
// engine start behind it. It removes every container of the voice naming
// scheme labeled with this service's own state root: at serve start none of
// them has a living owner. The root label is the ownership boundary, so
// another live instance's engines on the same daemon are never touched. Call
// once, right after New.
func (s *Service) SweepStale() {
	done := make(chan struct{})
	s.mu.Lock()
	s.sweepDone = done
	s.mu.Unlock()
	go func() {
		defer close(done)
		sweepStale(s.stateRoot, dockerHostEnv(hostOf(s.host)))
	}()
}

func sweepStale(stateRoot string, env []string) {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ownLabel := "label=" + voiceRootLabel + "=" + stateRoot
	out, err := dockerCmd(ctx, dockerPath, env, "ps", "-a", "--filter", ownLabel, "--format", "{{.Names}}").Output()
	if err != nil {
		return
	}
	removed := 0
	for _, name := range strings.Fields(string(out)) {
		if voiceSchemeName(name) {
			_ = dockerCmd(ctx, dockerPath, env, "rm", "-f", name).Run()
			removed++
		}
	}
	if removed > 0 {
		log.Printf("voice: swept %d stale engine container(s)", removed)
	}
	// Image tags of the scheme that no shipped build file produces anymore:
	// a release changed the file, its hash tag moved on. Images are shared
	// across instances, so a tag still referenced by any container stays.
	current := map[string]bool{}
	repos := map[string]bool{}
	for _, p := range profiles {
		current[imageRef(p)] = true
		repos[p.Image] = true
	}
	out, err = dockerCmd(ctx, dockerPath, env, "images", "--format", "{{.Repository}}:{{.Tag}}").Output()
	if err != nil {
		return
	}
	staleTags := 0
	for _, ref := range strings.Fields(string(out)) {
		repo, _, found := strings.Cut(ref, ":")
		if !found || !repos[repo] || current[ref] {
			continue
		}
		if used, err := dockerCmd(ctx, dockerPath, env, "ps", "-a", "--filter", "ancestor="+ref, "-q").Output(); err != nil || len(strings.TrimSpace(string(used))) > 0 {
			continue
		}
		_ = dockerCmd(ctx, dockerPath, env, "rmi", ref).Run()
		staleTags++
	}
	if staleTags > 0 {
		log.Printf("voice: swept %d outdated engine image tag(s)", staleTags)
	}
}

// waitSweep gates the first start behind the boot sweep, so a fresh start can
// never race the removal of its own predecessor's name.
func (s *Service) waitSweep() {
	s.mu.Lock()
	done := s.sweepDone
	s.mu.Unlock()
	if done != nil {
		<-done
	}
}

// removeStaleContainer clears the name right before a start: a container that
// outlived an unclean death would otherwise block the new engine with a taken
// name. Nothing speaks to such a container anymore, so removal is the reuse.
func removeStaleContainer(ctx context.Context, dockerPath string, env []string, name string) {
	rmCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = dockerCmd(rmCtx, dockerPath, env, "rm", "-f", name).Run()
}

// Transcribe runs one clip through the speech to text engine, warming its
// container when it is cold. The clip is whatever the browser recorded,
// webm/opus mostly and mp4/aac on Safari; the engine decodes the container
// format itself, so no caller has to say which it is.
func (s *Service) Transcribe(ctx context.Context, clip []byte) (Transcript, error) {
	if len(clip) == 0 {
		return Transcript{}, errors.New("the recording is empty")
	}
	body, err := s.request(ctx, Whisper(), func(base string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/transcribe", bytes.NewReader(clip))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		return req, nil
	})
	if err != nil {
		return Transcript{}, err
	}
	var answer struct {
		Text     string `json:"text"`
		Language string `json:"language"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return Transcript{}, fmt.Errorf("the engine answered unreadably: %w", err)
	}
	return Transcript{Text: answer.Text, Language: answer.Language}, nil
}

// Synthesize runs one text through the text to speech engine and answers the
// spoken wav. language is the lowercase two letter code the cockpit detected;
// the engine maps it onto its voices and falls back to English for anything
// it has no voice for.
func (s *Service) Synthesize(ctx context.Context, text, language string) ([]byte, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("there is nothing to speak")
	}
	payload, err := json.Marshal(map[string]string{"text": text, "language": language})
	if err != nil {
		return nil, err
	}
	return s.request(ctx, Piper(), func(base string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/synthesize", bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
}

// request runs one engine round: ensure the profile's container is warm, send
// the request the builder shapes, and answer the raw body of a 200. A
// transport error drops the instance and retries once, because a container
// that died between uses looks exactly like that; an answer the engine gave,
// an error included, is never retried.
func (s *Service) request(ctx context.Context, p *Profile, build func(base string) (*http.Request, error)) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		inst, err := s.ensureRunning(ctx, p)
		if err != nil {
			return nil, err
		}
		body, err := s.roundTrip(ctx, inst, build)
		if err == nil {
			s.markUsed(inst)
			return body, nil
		}
		var lost *transportError
		if attempt == 0 && errors.As(err, &lost) {
			s.drop(inst)
			continue
		}
		return nil, err
	}
}

// transportError marks a failure to reach the engine at all, the one case a
// retry through a fresh container can repair.
type transportError struct{ err error }

func (e *transportError) Error() string { return e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

func (s *Service) roundTrip(ctx context.Context, inst *instance, build func(base string) (*http.Request, error)) ([]byte, error) {
	postCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := build(inst.base)
	if err != nil {
		return nil, err
	}
	res, err := s.client.Do(req.WithContext(postCtx))
	if err != nil {
		return nil, &transportError{err: err}
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxAnswerBytes))
	if err != nil {
		return nil, &transportError{err: err}
	}
	if res.StatusCode != http.StatusOK {
		var refusal struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &refusal) == nil && refusal.Error != "" {
			return nil, fmt.Errorf("the engine refused: %s", refusal.Error)
		}
		return nil, fmt.Errorf("the engine answered status %d", res.StatusCode)
	}
	return body, nil
}

// ensureRunning answers the profile's warm engine, starting it when it is
// cold. The start runs detached from the request's own context: a person
// navigating away must not kill a container mid warm, so only the warm budget
// ends it, the same reasoning the git writes follow.
func (s *Service) ensureRunning(ctx context.Context, p *Profile) (*instance, error) {
	s.waitSweep()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("the voice service is shutting down")
	}
	option := s.Option(p)
	if inst := s.running[p.ID]; inst != nil {
		if inst.option == option {
			return inst, nil
		}
		// The setting moved while this engine stood warm. Its model was
		// loaded at start, so the change lands by starting over.
		log.Printf("voice: %s switches to %q, restarting", inst.name, option)
		s.removeLocked(inst)
	}
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return nil, errors.New("no docker client exists on this host")
	}
	env := dockerHostEnv(hostOf(s.host))
	warmCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), warmTimeout)
	defer cancel()
	// The one time costs are minutes rather than seconds, and whoever pressed
	// the button deserves to know that this wait is the first use one. The
	// check runs before the work, so the word is out while the wait happens.
	if s.announce != nil && s.firstUse(warmCtx, dockerPath, env, p) {
		s.announce(p.ID)
	}
	if err := ensureImage(warmCtx, dockerPath, env, p); err != nil {
		return nil, err
	}
	dir := s.cacheDir(p)
	if err := ensureCacheDir(dir); err != nil {
		return nil, err
	}
	name := s.containerName(p)
	removeStaleContainer(warmCtx, dockerPath, env, name)
	if out, err := dockerCmd(warmCtx, dockerPath, env, s.runArgs(dir, name, p, option)...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("start %s: %w: %s", name, err, firstLine(string(out)))
	}
	base, err := apiBase(warmCtx, dockerPath, env, name, p)
	if err == nil {
		err = waitReady(warmCtx, s.client, base, dockerPath, env, name)
	}
	if err != nil {
		removeStaleContainer(context.WithoutCancel(warmCtx), dockerPath, env, name)
		return nil, err
	}
	inst := &instance{profile: p, name: name, base: base, option: option}
	s.running[p.ID] = inst
	s.touchLocked(inst)
	log.Printf("voice: %s is warm on %s running %q", name, base, option)
	return inst, nil
}

// firstUse reports whether warming this profile still pays its one time
// costs: the image is not built yet, or nothing has been downloaded into the
// model cache. Both mean minutes instead of seconds.
func (s *Service) firstUse(ctx context.Context, dockerPath string, env []string, p *Profile) bool {
	if dockerCmd(ctx, dockerPath, env, "image", "inspect", imageRef(p)).Run() != nil {
		return true
	}
	entries, err := os.ReadDir(filepath.Join(s.cacheDir(p), "models"))
	return err != nil || len(entries) == 0
}

// runArgs is the container run of an engine: the cache directory mounts at
// its own path, the engine runs as the cockpit's own user with HOME inside
// the mount (the uid has no passwd entry in the image), the model directory
// points into the mount so the downloads survive the container, the picked
// option travels in the profile's own variable, and the API port publishes on
// loopback under an ephemeral host port the start reads back.
func (s *Service) runArgs(cacheDir, name string, p *Profile, option string) []string {
	args := []string{
		"run", "-d", "--rm", "--init",
		"--name", name,
		"--label", voiceRootLabel + "=" + s.stateRoot,
		"--user", fmt.Sprintf("%d:%d", processUID, processGID),
		"-v", cacheDir + ":" + cacheDir,
		"-e", "HOME=" + cacheDir + "/home",
		"-e", "DC_MODEL_DIR=" + cacheDir + "/models",
	}
	if p.EnvVar != "" && option != "" {
		args = append(args, "-e", p.EnvVar+"="+option)
	}
	return append(args,
		"-p", fmt.Sprintf("127.0.0.1:0:%d", p.Port),
		imageRef(p),
	)
}

// apiBase reads the host side of the published API port back out of the
// daemon. The answer may carry an address family per line; the loopback line
// is the one the publish asked for.
func apiBase(ctx context.Context, dockerPath string, env []string, name string, p *Profile) (string, error) {
	out, err := dockerCmd(ctx, dockerPath, env, "port", name, fmt.Sprintf("%d/tcp", p.Port)).Output()
	if err != nil {
		return "", fmt.Errorf("read the engine's port: %w", err)
	}
	for _, line := range strings.Fields(string(out)) {
		if strings.HasPrefix(line, "127.0.0.1:") {
			return "http://" + line, nil
		}
	}
	return "", fmt.Errorf("the engine published no loopback port: %q", strings.TrimSpace(string(out)))
}

// waitReady polls the engine's health route until it answers. The models load
// before the port binds, so an answering port means loaded models. A
// container that died while loading is reported with the tail of its own log
// instead of a timeout that says nothing.
func waitReady(ctx context.Context, client *http.Client, base, dockerPath string, env []string, name string) error {
	for tick := 0; ; tick++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
		if err != nil {
			return err
		}
		if res, err := client.Do(req); err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return nil
			}
		}
		if tick%20 == 19 {
			state, err := dockerCmd(ctx, dockerPath, env, "inspect", "-f", "{{.State.Running}}", name).Output()
			if err != nil || strings.TrimSpace(string(state)) != "true" {
				logs, _ := dockerCmd(ctx, dockerPath, env, "logs", "--tail", "5", name).CombinedOutput()
				return fmt.Errorf("the engine container died while starting: %s", firstLine(string(logs)))
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the engine did not become ready: %w", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// markUsed moves the instance's idle clock after a served request.
func (s *Service) markUsed(inst *instance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[inst.profile.ID] == inst {
		s.touchLocked(inst)
	}
}

// touchLocked resets the instance's idle timer; call with mu held.
func (s *Service) touchLocked(inst *instance) {
	inst.lastUsed = time.Now()
	if inst.idleTimer != nil {
		inst.idleTimer.Stop()
	}
	inst.idleTimer = time.AfterFunc(idleTimeout, func() { s.stopIdle(inst) })
}

// stopIdle takes one engine down once nothing used it for the whole idle
// window. A use that slipped in after the timer fired re-arms instead.
func (s *Service) stopIdle(inst *instance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.running[inst.profile.ID] != inst {
		return
	}
	if since := time.Since(inst.lastUsed); since < idleTimeout {
		inst.idleTimer = time.AfterFunc(idleTimeout-since, func() { s.stopIdle(inst) })
		return
	}
	s.removeLocked(inst)
	log.Printf("voice: %s idled out and was stopped", inst.name)
}

// drop discards one instance after a transport error, so the retry starts
// fresh. Another caller may have replaced it already; only the very instance
// that failed is taken down.
func (s *Service) drop(inst *instance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[inst.profile.ID] != inst {
		return
	}
	s.removeLocked(inst)
}

// removeLocked stops one running container; call with mu held.
func (s *Service) removeLocked(inst *instance) {
	delete(s.running, inst.profile.ID)
	if inst.idleTimer != nil {
		inst.idleTimer.Stop()
		inst.idleTimer = nil
	}
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return
	}
	removeStaleContainer(context.Background(), dockerPath, dockerHostEnv(hostOf(s.host)), inst.name)
}

// Close stops the engine containers. Call on shutdown, after which the
// service refuses new work.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, inst := range s.running {
		s.removeLocked(inst)
	}
}

// firstLine keeps an error message to docker's own reason, which stands first
// in its output; the trailing help hint line must never reach a log.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}
