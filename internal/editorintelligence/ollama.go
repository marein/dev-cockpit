package editorintelligence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// defaultOllamaURL is the fixed loopback endpoint of a local Ollama. The
// DEV_COCKPIT_OLLAMA_URL environment variable overrides it for tests and
// unusual local setups; the web UI never configures a URL.
const defaultOllamaURL = "http://127.0.0.1:11434"

const ollamaTimeout = 20 * time.Second

// fimTemplate carries the model family specific fill in the middle tokens.
// The response sanitizer strips the stop tokens again should a model echo
// them.
type fimTemplate struct {
	prefix string
	suffix string
	middle string
	// suffixFirst marks the Mistral shape, where the prompt carries the
	// suffix before the prefix and generation continues after the prefix.
	suffixFirst bool
	stops       []string
}

// prompt assembles the raw FIM prompt for the document context.
func (t fimTemplate) prompt(prefix, suffix string) string {
	if t.suffixFirst {
		return t.prefix + suffix + t.suffix + prefix
	}
	return t.prefix + prefix + t.suffix + suffix + t.middle
}

// fimTemplates maps a model name fragment to its template. A model whose
// name matches no entry is rejected: chat prompting is not a substitute for
// FIM completion.
var fimTemplates = []struct {
	match    string
	template fimTemplate
}{
	{"qwen", fimTemplate{
		prefix: "<|fim_prefix|>", suffix: "<|fim_suffix|>", middle: "<|fim_middle|>",
		stops: []string{"<|endoftext|>", "<|fim_prefix|>", "<|fim_suffix|>", "<|fim_middle|>", "<|fim_pad|>", "<|repo_name|>", "<|file_sep|>", "<|im_start|>", "<|im_end|>"},
	}},
	{"codellama", fimTemplate{
		prefix: "<PRE> ", suffix: " <SUF>", middle: " <MID>",
		stops: []string{"<EOT>", "<PRE>", "<SUF>", "<MID>"},
	}},
	{"deepseek-coder", fimTemplate{
		prefix: "<｜fim▁begin｜>", suffix: "<｜fim▁hole｜>", middle: "<｜fim▁end｜>",
		stops: []string{"<｜end▁of▁sentence｜>", "<｜fim▁begin｜>", "<｜fim▁hole｜>", "<｜fim▁end｜>"},
	}},
	{"starcoder", fimTemplate{
		prefix: "<fim_prefix>", suffix: "<fim_suffix>", middle: "<fim_middle>",
		stops: []string{"<|endoftext|>", "<fim_prefix>", "<fim_suffix>", "<fim_middle>"},
	}},
	{"codegemma", fimTemplate{
		prefix: "<|fim_prefix|>", suffix: "<|fim_suffix|>", middle: "<|fim_middle|>",
		stops: []string{"<|file_separator|>", "<|fim_prefix|>", "<|fim_suffix|>", "<|fim_middle|>"},
	}},
	{"codestral", fimTemplate{
		prefix: "[SUFFIX]", suffix: "[PREFIX]", suffixFirst: true,
		stops: []string{"[PREFIX]", "[SUFFIX]", "</s>"},
	}},
}

// templateFor picks the FIM template for a model name like
// "qwen2.5-coder:7b".
func templateFor(model string) (fimTemplate, bool) {
	lower := strings.ToLower(model)
	for _, entry := range fimTemplates {
		if strings.Contains(lower, entry.match) {
			return entry.template, true
		}
	}
	return fimTemplate{}, false
}

// OllamaProvider talks to a local Ollama over its generate API without
// streaming. The client never follows redirects and honours cancellation.
type OllamaProvider struct {
	baseURL string
	model   string
	client  *http.Client
}

// NewOllama returns a provider for the given model at the fixed loopback
// endpoint (or the environment override).
func NewOllama(model string) *OllamaProvider {
	base := strings.TrimRight(os.Getenv("DEV_COCKPIT_OLLAMA_URL"), "/")
	if base == "" {
		base = defaultOllamaURL
	}
	return &OllamaProvider{
		baseURL: base,
		model:   model,
		client: &http.Client{
			Timeout: ollamaTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return errors.New("redirects are not followed")
			},
		},
	}
}

// Name labels the provider in the UI.
func (o *OllamaProvider) Name() string { return "Ollama" }

// Available checks that Ollama answers, the model is installed and a FIM
// template exists for it. It sends no source code.
func (o *OllamaProvider) Available(ctx context.Context) (bool, string) {
	if strings.TrimSpace(o.model) == "" {
		return false, "No model configured."
	}
	if _, ok := templateFor(o.model); !ok {
		return false, fmt.Sprintf("No fill in the middle template is known for %q. Use a FIM capable model such as qwen2.5-coder, codellama, deepseek-coder, starcoder2, codegemma or codestral.", o.model)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL+"/api/tags", nil)
	if err != nil {
		return false, err.Error()
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return false, fmt.Sprintf("Ollama is not reachable at %s.", o.baseURL)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("Ollama answered with status %d.", resp.StatusCode)
	}
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tags); err != nil {
		return false, "Ollama answered with an unreadable model list."
	}
	want := strings.TrimSpace(o.model)
	for _, m := range tags.Models {
		if m.Name == want || strings.SplitN(m.Name, ":", 2)[0] == want {
			return true, ""
		}
	}
	return false, fmt.Sprintf("Model %q is not installed in Ollama.", o.model)
}

// Complete runs one non streaming FIM generation and returns the sanitized
// insertion text. An empty result is a valid "nothing useful" answer.
func (o *OllamaProvider) Complete(ctx context.Context, fim FIMRequest) (string, error) {
	template, ok := templateFor(o.model)
	if !ok {
		return "", fmt.Errorf("no FIM template for model %q", o.model)
	}
	body, err := json.Marshal(map[string]any{
		"model":  o.model,
		"prompt": template.prompt(fim.Prefix, fim.Suffix),
		"raw":    true,
		"stream": false,
		"options": map[string]any{
			"num_predict": fimPredictLimit,
			"temperature": 0.2,
			"stop":        template.stops,
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return "", fmt.Errorf("ollama status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var out struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return "", err
	}
	return sanitizeInsertion(out.Response, template.stops), nil
}
