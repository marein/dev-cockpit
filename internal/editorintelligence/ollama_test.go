package editorintelligence

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func ollamaStub(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	t.Setenv("DEV_COCKPIT_OLLAMA_URL", server.URL)
	return server
}

func TestTemplateFor(t *testing.T) {
	cases := map[string]bool{
		"qwen2.5-coder:7b":    true,
		"codellama:13b-code":  true,
		"deepseek-coder:6.7b": true,
		"starcoder2:3b":       true,
		"codegemma:2b":        true,
		"codestral:22b":       true,
		"llama3:8b":           false,
		"mistral:7b":          false,
	}
	for model, want := range cases {
		if _, ok := templateFor(model); ok != want {
			t.Fatalf("templateFor(%q) = %v, want %v", model, ok, want)
		}
	}
}

func TestOllamaCompletePromptShape(t *testing.T) {
	var got struct {
		Model   string `json:"model"`
		Prompt  string `json:"prompt"`
		Raw     bool   `json:"raw"`
		Stream  bool   `json:"stream"`
		Options struct {
			NumPredict int      `json:"num_predict"`
			Stop       []string `json:"stop"`
		} `json:"options"`
	}
	ollamaStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Fatalf("path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"response": "middle()\n<|endoftext|>"})
	})
	provider := NewOllama("qwen2.5-coder:7b")
	insert, err := provider.Complete(context.Background(), FIMRequest{Prefix: "PRE", Suffix: "SUF"})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if insert != "middle()" {
		t.Fatalf("insert %q", insert)
	}
	if got.Prompt != "<|fim_prefix|>PRE<|fim_suffix|>SUF<|fim_middle|>" {
		t.Fatalf("prompt %q", got.Prompt)
	}
	if !got.Raw || got.Stream {
		t.Fatalf("raw/stream flags: %v %v", got.Raw, got.Stream)
	}
	if got.Options.NumPredict != fimPredictLimit || len(got.Options.Stop) == 0 {
		t.Fatalf("options: %+v", got.Options)
	}
}

func TestOllamaCompleteSuffixFirstPrompt(t *testing.T) {
	var prompt string
	ollamaStub(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Prompt string `json:"prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		prompt = body.Prompt
		_ = json.NewEncoder(w).Encode(map[string]string{"response": "x"})
	})
	provider := NewOllama("codestral:22b")
	if _, err := provider.Complete(context.Background(), FIMRequest{Prefix: "PRE", Suffix: "SUF"}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if prompt != "[SUFFIX]SUF[PREFIX]PRE" {
		t.Fatalf("prompt %q", prompt)
	}
}

func TestOllamaCompleteNoTemplate(t *testing.T) {
	t.Setenv("DEV_COCKPIT_OLLAMA_URL", "http://127.0.0.1:1")
	provider := NewOllama("llama3:8b")
	if _, err := provider.Complete(context.Background(), FIMRequest{}); err == nil {
		t.Fatal("expected template error")
	}
}

func TestOllamaCompleteCancellation(t *testing.T) {
	started := make(chan struct{})
	ollamaStub(t, func(w http.ResponseWriter, r *http.Request) {
		// The body must be consumed, otherwise the server never starts the
		// background read that turns the client disconnect into a context
		// cancellation. The timeout keeps the stub's Close from wedging
		// should detection lag.
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	})
	provider := NewOllama("qwen2.5-coder")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	start := time.Now()
	if _, err := provider.Complete(ctx, FIMRequest{}); err == nil {
		t.Fatal("expected cancellation error")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancellation did not interrupt the request")
	}
}

func TestOllamaCompleteErrorStatus(t *testing.T) {
	ollamaStub(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
	})
	provider := NewOllama("qwen2.5-coder")
	if _, err := provider.Complete(context.Background(), FIMRequest{}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected status error, got %v", err)
	}
}

func TestOllamaAvailable(t *testing.T) {
	ollamaStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{{"name": "qwen2.5-coder:7b"}},
		})
	})
	cases := []struct {
		model string
		want  bool
	}{
		{"qwen2.5-coder:7b", true},
		{"qwen2.5-coder", true},
		{"codellama", false},
		{"llama3", false},
		{"", false},
	}
	for _, c := range cases {
		got, reason := NewOllama(c.model).Available(context.Background())
		if got != c.want {
			t.Fatalf("Available(%q) = %v (%s), want %v", c.model, got, reason, c.want)
		}
		if !got && reason == "" {
			t.Fatalf("Available(%q) unavailable without reason", c.model)
		}
	}
}

func TestOllamaAvailableUnreachable(t *testing.T) {
	t.Setenv("DEV_COCKPIT_OLLAMA_URL", "http://127.0.0.1:1")
	ok, reason := NewOllama("qwen2.5-coder").Available(context.Background())
	if ok || !strings.Contains(reason, "not reachable") {
		t.Fatalf("got %v %q", ok, reason)
	}
}

func TestOllamaRefusesRedirects(t *testing.T) {
	ollamaStub(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/", http.StatusFound)
	})
	provider := NewOllama("qwen2.5-coder")
	if _, err := provider.Complete(context.Background(), FIMRequest{}); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("expected redirect refusal, got %v", err)
	}
}
