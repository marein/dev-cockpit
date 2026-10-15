package editorintelligence

import (
	"context"
	"testing"
	"time"
)

func phpRequest(t *testing.T) Request {
	t.Helper()
	return Request{
		Client:      "client-a",
		ProjectName: "proj",
		ProjectRoot: t.TempDir(),
		Path:        "src/a.php",
		Content:     "<?php\nnew Bar();\nhelper();\n",
		Line:        1,
		Character:   5,
	}
}

func TestServiceClassNameReadsTheHover(t *testing.T) {
	installFakeLSP(t, "normal", "intelephense")
	s := newTestService(t)

	req := phpRequest(t)
	res, err := s.ClassName(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	want := &lspRange{Start: lspPosition{Line: 1, Character: 5}, End: lspPosition{Line: 1, Character: 8}}
	if !res.Available || res.Name != `Foo\Bar` || res.Range == nil || *res.Range != *want {
		t.Fatalf("class: %+v", res)
	}
	req.Line = 2
	res, err = s.ClassName(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.Name != "" || res.Range == nil {
		t.Fatalf("a function names no class but keeps its range: %+v", res)
	}
}

// The lookup follows the pointer, so it must not take a jump down that
// still waits on the same document.
func TestServiceClassNameLeavesADefinitionInFlight(t *testing.T) {
	installFakeLSP(t, "hang", "intelephense")
	s := newTestService(t)
	req := phpRequest(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Result, 1)
	go func() {
		res, _ := s.Definition(ctx, req)
		done <- res
	}()
	waitFor(t, "the definition in flight", func() bool {
		mc := slotOf(s, req.ProjectName, req.Path)
		if mc == nil {
			return false
		}
		mc.inflightMu.Lock()
		defer mc.inflightMu.Unlock()
		return len(mc.inflight) == 1
	})
	res, err := s.ClassName(context.Background(), req)
	if err != nil || res.Name != `Foo\Bar` {
		t.Fatalf("class name beside a hanging definition: %+v %v", res, err)
	}
	select {
	case got := <-done:
		t.Fatalf("the class name lookup cancelled the definition: %+v", got)
	case <-time.After(300 * time.Millisecond):
	}
	cancel()
	if got := <-done; got.Status != StatusCanceled {
		t.Fatalf("the definition ends with its own context: %+v", got)
	}
}

func TestServiceClassNameWithoutTheRuleStartsNoServer(t *testing.T) {
	installFakeLSP(t, "normal", "gopls")
	s := newTestService(t)

	res, err := s.ClassName(context.Background(), goRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Available || res.Status != StatusNoLanguage {
		t.Fatalf("status %+v", res)
	}
	if n := s.ConnectionCount(); n != 0 {
		t.Fatalf("%d servers started for a language without class names", n)
	}
}

func TestServiceClassNameWaitsOutTheIndexing(t *testing.T) {
	installFakeLSP(t, "indexing", "intelephense")
	s := newTestService(t)

	start := time.Now()
	res, err := s.ClassName(context.Background(), phpRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	// The fake answers no hover while its announced indexing runs.
	if res.Name != `Foo\Bar` {
		t.Fatalf("must wait for the announced indexing: %+v", res)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Fatal("answered before the indexing ended")
	}
}

func TestServiceClassNameRejectsAPositionOutsideTheDocument(t *testing.T) {
	installFakeLSP(t, "normal", "intelephense")
	s := newTestService(t)

	req := phpRequest(t)
	req.Line = 99
	if _, err := s.ClassName(context.Background(), req); err == nil {
		t.Fatal("expected position error")
	}
}
