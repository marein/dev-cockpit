package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/markdown"
	"github.com/marein/dev-cockpit/internal/voice"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// spokenAnswer is one synthesis in flight: everyone asking for the same answer
// in the same voice waits on it and reads what it produced.
type spokenAnswer struct {
	done chan struct{}
	wav  []byte
	err  error
}

// The voice settings, one key per speech engine, stored install wide like the
// editor LSP keys and with the same value scheme: "auto", the engine's name
// with the "-docker" marker, or "off". Absent, "auto" and unknown values all
// mean the automatic default, so a later option never hides the feature.
// They live on the Assistant settings page's Voice tab, the same shape the
// editor page uses for its sections, so the page can grow more tabs; the
// bare /settings/assistant leads to the one there is.
const (
	assistantSettingsPath = "/settings/assistant"
	voiceSettingsPath     = "/settings/assistant/voice"
	voiceSTTKey           = "voice-stt"
	voiceTTSKey           = "voice-tts"
)

func (s *Server) handleSettingsAssistant(c *gin.Context) {
	c.Redirect(http.StatusSeeOther, voiceSettingsPath)
}

// voiceChoice normalizes one stored value onto the three the select offers,
// "auto" for everything unknown, the way lspChoice does for a language.
func voiceChoice(stored string, p *voice.Profile) string {
	switch stored {
	case "off", p.Server + "-docker":
		return stored
	}
	return "auto"
}

// voiceDockerOK is whether an engine could run right now: the daemon answers
// and a docker client exists, the same two halves the LSP gate reads.
func (s *Server) voiceDockerOK() bool {
	return s.docker.State().Available && voice.Detected()
}

// voiceSTTOff and voiceTTSOff report whether an engine is off right now,
// explicitly or as the end of the automatic chain; the resolution is the
// LSP one, the value schemes are identical on purpose. A server wired
// without the voice service or the settings store, which the handler tests
// build, has no engine to run and reads as off.
func (s *Server) voiceSTTOff() bool {
	if s.voice == nil || s.settings == nil {
		return true
	}
	return resolveLSPMode(s.settings.Get(voiceSTTKey), voice.Whisper().Server, s.voiceDockerOK())
}

func (s *Server) voiceTTSOff() bool {
	if s.voice == nil || s.settings == nil {
		return true
	}
	return resolveLSPMode(s.settings.Get(voiceTTSKey), voice.Piper().Server, s.voiceDockerOK())
}

// voiceOptions maps a profile's fixed choices onto the page's select. The
// list is the profile's own, so the page can only ever offer what the engine
// container knows how to run.
func voiceOptions(p *voice.Profile) []render.VoiceOption {
	out := make([]render.VoiceOption, 0, len(p.Options))
	for _, o := range p.Options {
		out = append(out, render.VoiceOption{ID: o.ID, Label: o.Label})
	}
	return out
}

// voiceOptionSelected is the option in force for a profile: the stored pick
// when it is one the profile offers, its default otherwise, the same answer
// the service gives the container.
func (s *Server) voiceOptionSelected(p *voice.Profile) string {
	if s.settings == nil {
		return p.Default
	}
	return p.Normalize(s.settings.Get(p.SettingKey))
}

// handleSettingsVoice renders the voice page: per engine the automatic,
// Docker and Off choice the editor LSP page offers, plus the engine's own
// pick, the model for speech to text and the speaking voice for text to
// speech. Both selects show the stored choice, never what automatic resolves
// to.
func (s *Server) handleSettingsVoice(c *gin.Context) {
	whisper, piper := voice.Whisper(), voice.Piper()
	c.HTML(http.StatusOK, "settings_assistant_voice.gohtml", render.SettingsVoiceData{
		Page:        s.page(c, "Settings", "settings"),
		SettingsNav: s.settingsNav("assistant"),
		Section:     "voice",
		DockerOK:    s.docker.State().Available,
		Engines: []render.VoiceEngine{
			{
				Key:            "stt",
				Label:          "Speech to text",
				Detail:         "Runs faster-whisper on the CPU",
				Server:         whisper.Server,
				Selected:       voiceChoice(s.settings.Get(voiceSTTKey), whisper),
				OptionKey:      "stt-model",
				OptionLabel:    "Model",
				OptionDetail:   "A bigger model transcribes more accurately and keeps you waiting longer after a push to talk press. Changing this restarts the engine and downloads the model once.",
				Options:        voiceOptions(whisper),
				OptionSelected: s.voiceOptionSelected(whisper),
			},
			{
				Key:            "tts",
				Label:          "Text to speech",
				Detail:         "Runs Piper on the CPU",
				Server:         piper.Server,
				Selected:       voiceChoice(s.settings.Get(voiceTTSKey), piper),
				OptionKey:      "tts-voice",
				OptionLabel:    "Voice",
				OptionDetail:   "One voice reads every answer. Changing this restarts the engine and downloads the voice once.",
				Options:        voiceOptions(piper),
				OptionSelected: s.voiceOptionSelected(piper),
			},
		},
	})
}

// handleSettingsVoiceSave stores each engine's picks, the on off choice and
// the engine's own option. A value the select never offered keeps the current
// setting instead of writing something no option stands for, which is also
// what keeps an engine's option an id out of its fixed list.
func (s *Server) handleSettingsVoiceSave(c *gin.Context) {
	stores := []struct {
		key       string
		field     string
		optionKey string
		option    string
		profile   *voice.Profile
	}{
		{voiceSTTKey, "stt", voice.Whisper().SettingKey, "stt-model", voice.Whisper()},
		{voiceTTSKey, "tts", voice.Piper().SettingKey, "tts-voice", voice.Piper()},
	}
	for _, entry := range stores {
		if value := c.PostForm(entry.field); value != "" && voiceChoice(value, entry.profile) == value {
			s.settings.Set(entry.key, value)
		}
		if value := c.PostForm(entry.option); value != "" && entry.profile.Normalize(value) == value {
			s.settings.Set(entry.optionKey, value)
		}
	}
	s.redirectWithFlash(c, voiceSettingsPath, "Settings saved.", "")
}

// handleAssistantSTT takes one recorded clip and answers its transcript as
// JSON. The clip is whatever MediaRecorder produced, webm/opus mostly and
// mp4/aac on Safari; the engine decodes the container format itself, so both
// pass through here unnamed. The off check is the backstop for a page from
// before a settings change, the page attribute already takes the button away.
func (s *Server) handleAssistantSTT(c *gin.Context) {
	if _, err := s.conversations.Get(c.Param("id")); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if s.voiceSTTOff() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Speech to text is off."})
		return
	}
	header, err := c.FormFile("audio")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The recording could not be read."})
		return
	}
	if header.Size > s.maxUploadBytes() {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "That recording is too large."})
		return
	}
	src, err := header.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The recording could not be read."})
		return
	}
	clip, err := io.ReadAll(src)
	_ = src.Close()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The recording could not be read."})
		return
	}
	transcript, err := s.voice.Transcribe(c.Request.Context(), clip)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"text": strings.TrimSpace(transcript.Text), "language": transcript.Language})
}

// handleAssistantMessageAudio serves one answer spoken. The wav is rendered
// on the first ask and cached next to the conversation, so the speaker button
// and voice mode replay for free; a regenerated answer is a new message id
// and therefore a new cache entry. Only the cockpit process writes here, a
// container never touches the conversation directories: the text goes over
// the engine's API and the bytes come back the same way.
func (s *Server) handleAssistantMessageAudio(c *gin.Context) {
	id := c.Param("id")
	current, err := s.conversations.Get(id)
	if err != nil {
		c.String(http.StatusNotFound, err.Error())
		return
	}
	if s.voiceTTSOff() {
		c.String(http.StatusBadRequest, "Text to speech is off.")
		return
	}
	wanted := c.Param("messageId")
	var message assistant.Message
	found := false
	for _, m := range current.Messages {
		if m.ID == wanted && m.Role == assistant.RoleAssistant {
			message, found = m, true
			break
		}
	}
	if !found {
		c.String(http.StatusNotFound, "Message not found.")
		return
	}
	if message.State != assistant.StateComplete {
		c.String(http.StatusBadRequest, "The answer is not finished.")
		return
	}
	wav, err := s.speakAnswer(c.Request.Context(), id, message)
	if err != nil {
		c.String(http.StatusBadGateway, userFacingError(c, err))
		return
	}
	// Served straight out of the answer this request synthesized. The type is
	// set before ServeContent so nothing sniffs it, and reading from a byte
	// reader still answers a range request, which is what an audio element
	// asks with.
	c.Header("Content-Type", "audio/wav")
	c.Header("X-Content-Type-Options", "nosniff")
	http.ServeContent(c.Writer, c.Request, "", time.Time{}, bytes.NewReader(wav))
}

// speakAnswer synthesizes one answer and hands back the wav. Nothing is kept:
// a spoken answer is two seconds of engine time and megabytes of uncompressed
// audio, so it is cheaper to say it again than to store it, and with nothing
// stored there is no stale copy to invalidate when the voice changes and
// nothing of a conversation lying around outside its transcript.
//
// One synthesis still runs per answer however many ask at once: voice mode and
// a speaker click landing together share the one round instead of paying for
// two.
func (s *Server) speakAnswer(ctx context.Context, conversationID string, m assistant.Message) ([]byte, error) {
	text := markdown.Speech(m.Content)
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("There is nothing to speak in this answer.")
	}
	key := conversationID + "/" + m.ID + "/" + s.voiceOptionSelected(voice.Piper())
	s.audioMu.Lock()
	if s.audioBusy == nil {
		s.audioBusy = map[string]*spokenAnswer{}
	}
	if run, busy := s.audioBusy[key]; busy {
		s.audioMu.Unlock()
		select {
		case <-run.done:
			return run.wav, run.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	run := &spokenAnswer{done: make(chan struct{})}
	s.audioBusy[key] = run
	s.audioMu.Unlock()

	run.wav, run.err = s.voice.Synthesize(ctx, text, voice.DetectLanguage(text))
	close(run.done)
	s.audioMu.Lock()
	delete(s.audioBusy, key)
	s.audioMu.Unlock()
	return run.wav, run.err
}
