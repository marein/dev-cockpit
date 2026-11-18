package web

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/browserextension"
	"github.com/marein/dev-cockpit/internal/web/render"
)

type browserExtensionFixture struct {
	s         *Server
	send      func(*http.Request) *httptest.ResponseRecorder
	token     string
	assistant string
}

func newBrowserExtensionFixture(t *testing.T) *browserExtensionFixture {
	t.Helper()
	_, s := modelSettingsServer(t)
	s.browserExtensionTokens = browserextension.NewStore(s.cfg.StateDir)
	f := &browserExtensionFixture{s: s}
	f.send, f.token = ownerSession(t, s)
	dir, stateDir := t.TempDir(), t.TempDir()
	assistants, workspace, err := assistant.New(stateDir, sessionTurnCoder{dir: dir}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	f.s.assistants, f.s.workspace = assistants, workspace
	created, err := assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	f.assistant = created.ID
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(dir, "gate"), nil, 0o644)
		for _, one := range assistants.List() {
			for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && !assistants.RunningSince(one.ID).IsZero(); time.Sleep(20 * time.Millisecond) {
			}
		}
	})
	return f
}

func (f *browserExtensionFixture) page(t *testing.T) string {
	t.Helper()
	rec := f.send(httptest.NewRequest(http.MethodGet, settingsBrowserPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d", settingsBrowserPath, rec.Code)
	}
	return rec.Body.String()
}

func (f *browserExtensionFixture) owner(path string, form url.Values) *httptest.ResponseRecorder {
	form.Set(csrfFieldName, f.token)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return f.send(req)
}

func (f *browserExtensionFixture) newToken(name string) *httptest.ResponseRecorder {
	return f.owner(settingsBrowserPath, url.Values{"form": {"new-token"}, "name": {name}})
}

func (f *browserExtensionFixture) local(method, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return f.send(req.WithContext(context.WithValue(req.Context(), localCallKey, true)))
}

var shownBrowserExtensionToken = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type madeBrowserExtensionToken struct {
	Name  string `json:"name"`
	Token string `json:"token"`
}

func madeToken(t *testing.T, rec *httptest.ResponseRecorder) madeBrowserExtensionToken {
	t.Helper()
	var made madeBrowserExtensionToken
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &made) != nil || !shownBrowserExtensionToken.MatchString(made.Token) {
		t.Fatalf("making the token answered %d: %s", rec.Code, rec.Body.String())
	}
	return made
}

func (f *browserExtensionFixture) makeToken(t *testing.T, name string) string {
	t.Helper()
	return madeToken(t, f.newToken(name)).Token
}

func api(f *browserExtensionFixture, method, path, token string, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
	if body == nil {
		body = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.s.browserExtensionRouter().ServeHTTP(rec, req)
	return rec
}

func apiForm(f *browserExtensionFixture, path, token string, form url.Values) *httptest.ResponseRecorder {
	return api(f, http.MethodPost, path, token, bytes.NewBufferString(form.Encode()), "application/x-www-form-urlencoded")
}

func apiPicture(t *testing.T, f *browserExtensionFixture, token string, fields map[string]string, name string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	part, err := w.CreateFormFile("image", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(data)
	_ = w.Close()
	return api(f, http.MethodPost, browserExtensionAPIPath+"/pictures", token, &body, w.FormDataContentType())
}

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func errorOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var answer struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil || answer.Error == "" {
		t.Fatalf("the refusal %d is no JSON sentence: %s", rec.Code, rec.Body.String())
	}
	return answer.Error
}

func (s *Server) browserExtensionRouter() http.Handler {
	r := gin.New()
	r.SetHTMLTemplate(render.HTMLTemplate(s.assets.assetPath, "test", "test", s.plugins, nil))
	r.Use(ginsessions.Sessions("session", cookie.NewStore([]byte("test-key"))))
	s.registerRoutes(r)
	return r
}

func TestATokenIsShownOnceAndListedByName(t *testing.T) {
	f := newBrowserExtensionFixture(t)
	if page := f.page(t); !strings.Contains(page, "data-browser-extension-tokens-empty>No token yet.") || !strings.Contains(page, `name-max="64"`) || !strings.Contains(page, `<button type="button" class="btn btn-sm ms-auto" data-browser-extension-token-new>`) {
		t.Fatalf("the page offers no token: %s", page)
	}
	rec := f.newToken("Laptop <b>")
	made := madeToken(t, rec)
	if made.Name != "Laptop <b>" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("the dialog answered %q %q: %s", made.Name, rec.Header().Get("Cache-Control"), rec.Body.String())
	}
	page := f.page(t)
	if strings.Contains(page, made.Token) {
		t.Fatal("the page shows the token again")
	}
	if !strings.Contains(page, "data-browser-extension-token-name>Laptop &lt;b&gt;</div>") || !strings.Contains(page, "never used") {
		t.Fatalf("the page does not list the token: %s", page)
	}
	data, _ := os.ReadFile(filepath.Join(f.s.cfg.StateDir, "browser-extension", "tokens.json"))
	if len(data) == 0 || bytes.Contains(data, []byte(made.Token)) {
		t.Fatalf("the file holds %q", data)
	}
	if rec := api(f, http.MethodGet, browserExtensionAPIPath+"/targets", made.Token, nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("the token was refused: %d", rec.Code)
	}
	if page := f.page(t); !strings.Contains(page, "used <dc-time") {
		t.Fatal("the page does not say when the token was used")
	}
}

func TestARefusedTokenNameIsAnsweredForTheDialog(t *testing.T) {
	f := newBrowserExtensionFixture(t)
	f.makeToken(t, "Laptop")
	rec := f.newToken("laptop")
	if rec.Code != http.StatusBadRequest || !strings.Contains(errorOf(t, rec), "A token with that name exists.") {
		t.Fatalf("a taken name in the dialog answered %d: %s", rec.Code, rec.Body.String())
	}
	rec = f.newToken("two\nlines")
	if rec.Code != http.StatusBadRequest || !strings.Contains(errorOf(t, rec), "A name is one line") {
		t.Fatalf("a name of two lines answered %d: %s", rec.Code, rec.Body.String())
	}
	if len(f.s.browserExtensionTokens.List()) != 1 {
		t.Fatal("a refused name made a token")
	}
}

func TestTokensRefuseTheLocalSocketAndGuests(t *testing.T) {
	f := newBrowserExtensionFixture(t)
	token := f.makeToken(t, "Laptop")
	id := f.s.browserExtensionTokens.List()[0].ID
	for _, rec := range []*httptest.ResponseRecorder{
		f.local(http.MethodGet, settingsBrowserPath, nil),
		f.local(http.MethodPost, settingsBrowserPath, url.Values{"form": {"new-token"}, "name": {"Assistant's"}}),
		f.local(http.MethodPost, settingsBrowserPath, url.Values{"form": {"revoke-token"}, "id": {id}}),
	} {
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "Ask the user.") || strings.Contains(rec.Body.String(), "Laptop") {
			t.Fatalf("the local socket answered %d: %s", rec.Code, rec.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodPost, settingsBrowserPath, strings.NewReader("form=new-token&name=Forged"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if rec := f.send(req); rec.Code != http.StatusForbidden {
		t.Fatalf("a create without the CSRF token answered %d", rec.Code)
	}
	if list := f.s.browserExtensionTokens.List(); len(list) != 1 || list[0].Name != "Laptop" {
		t.Fatalf("the tokens are %+v", list)
	}
	if _, err := f.s.browserExtensionTokens.Lookup(token); err != nil {
		t.Fatal("a refused revoke ended the token")
	}
	for _, path := range []string{settingsBrowserPath, browserExtensionAPIPath + "/send"} {
		if !neverForGuest(path) {
			t.Fatalf("a link could reach %s", path)
		}
	}
}

func TestARevokedTokenIsRefusedAtOnce(t *testing.T) {
	f := newBrowserExtensionFixture(t)
	token := f.makeToken(t, "Laptop")
	if rec := api(f, http.MethodGet, browserExtensionAPIPath+"/targets", token, nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("the token was refused before the revoke: %d", rec.Code)
	}
	rec := f.owner(settingsBrowserPath, url.Values{"form": {"revoke-token"}, "id": {f.s.browserExtensionTokens.List()[0].ID}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("the revoke answered %d: %s", rec.Code, rec.Body.String())
	}
	rec = api(f, http.MethodGet, browserExtensionAPIPath+"/targets", token, nil, "")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(errorOf(t, rec), "revoked") {
		t.Fatalf("the revoked token answered %d: %s", rec.Code, rec.Body.String())
	}
	if rec := f.owner(settingsBrowserPath, url.Values{"form": {"revoke-token"}, "id": {"gone"}}); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "data-browser-extension-tokens-error>That token is gone already.") {
		t.Fatalf("a revoke of no token answered %d", rec.Code)
	}
}

func TestATokenAndASessionNeverCross(t *testing.T) {
	f := newBrowserExtensionFixture(t)
	token := f.makeToken(t, "Laptop")
	for _, path := range []string{"/assistants/instances", settingsBrowserPath} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		rec := httptest.NewRecorder()
		f.s.browserExtensionRouter().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("the token opened %s: %d", path, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, browserExtensionAPIPath+"/targets", nil)
	if rec := f.send(req); rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("the session opened the extension's routes: %d", rec.Code)
	}
	for _, bad := range []string{"", "Basic " + token, "Bearer", "Bearer " + token + "x", "Bearer " + strings.Repeat("A", 43)} {
		req := httptest.NewRequest(http.MethodGet, browserExtensionAPIPath+"/targets", nil)
		if bad != "" {
			req.Header.Set("Authorization", bad)
		}
		rec := httptest.NewRecorder()
		f.s.browserExtensionRouter().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), token) {
			t.Fatalf("%q answered %d", bad, rec.Code)
		}
	}
	if rec := api(f, http.MethodGet, browserExtensionAPIPath+"/targets", token, nil, ""); rec.Code != http.StatusOK || rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("the targets answered %d with %v", rec.Code, rec.Header())
	}
	if rec := api(f, http.MethodOptions, browserExtensionAPIPath+"/send", "", nil, ""); rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("a preflight was answered")
	}
}

var routeParam = regexp.MustCompile(`[:*][A-Za-z_]+`)

func TestOnlyATokenOpensTheAPI(t *testing.T) {
	f := newBrowserExtensionFixture(t)
	token := f.makeToken(t, "Laptop")
	r := gin.New()
	r.SetHTMLTemplate(render.HTMLTemplate(f.s.assets.assetPath, "test", "test", f.s.plugins, nil))
	r.Use(gin.CustomRecoveryWithWriter(io.Discard, f.s.recoveryHandler))
	r.Use(ginsessions.Sessions("session", cookie.NewStore([]byte("test-key"))))
	f.s.registerRoutes(r)
	serve := func(method, path, auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	inAPI := 0
	for _, route := range r.Routes() {
		path := routeParam.ReplaceAllString(route.Path, "x")
		if strings.HasPrefix(route.Path, apiPathPrefix+"/") {
			inAPI++
			if !strings.HasPrefix(route.Path, browserExtensionAPIPath+"/") {
				t.Errorf("%s stands under %s outside the browser extension's routes", route.Path, apiPathPrefix)
			}
			if rec := f.send(httptest.NewRequest(route.Method, path, nil)); rec.Code != http.StatusUnauthorized {
				t.Errorf("the session opened %s %s: %d", route.Method, route.Path, rec.Code)
			}
			continue
		}
		with, without := serve(route.Method, path, "Bearer "+token), serve(route.Method, path, "")
		if with.Code != without.Code || with.Header().Get("Location") != without.Header().Get("Location") {
			t.Errorf("the token changed %s %s: %d %q, without it %d %q", route.Method, route.Path, with.Code, with.Header().Get("Location"), without.Code, without.Header().Get("Location"))
		}
	}
	if inAPI != 5 {
		t.Fatalf("%d routes stand under %s", inAPI, apiPathPrefix)
	}
	for _, path := range []string{`/extension/targets`, `/extension/send`, "/api/targets"} {
		for _, rec := range []*httptest.ResponseRecorder{serve(http.MethodGet, path, "Bearer "+token), f.send(httptest.NewRequest(http.MethodGet, path, nil))} {
			if rec.Code != http.StatusNotFound || rec.Header().Get("Location") != "" {
				t.Fatalf("%s answered %d %q", path, rec.Code, rec.Header().Get("Location"))
			}
		}
	}
	if !neverForGuest(apiPathPrefix) || !neverForGuest(browserExtensionAPIPath+"/targets") {
		t.Fatal("a link could reach the API")
	}
}

func TestTheTargetsListTheAssistantsByIDAndName(t *testing.T) {
	f := newBrowserExtensionFixture(t)
	token := f.makeToken(t, "Laptop")
	if err := f.s.assistants.Rename(f.assistant, "Layout"); err != nil {
		t.Fatal(err)
	}
	rec := api(f, http.MethodGet, browserExtensionAPIPath+"/targets", token, nil, "")
	var got struct {
		Assistants      []map[string]string `json:"assistants"`
		AssistantCoders []map[string]string `json:"assistantCoders"`
		Coders          []map[string]string `json:"coders"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Assistants) != 1 || len(got.Assistants[0]) != 2 || got.Assistants[0]["id"] != f.assistant || got.Assistants[0]["name"] != "Layout" {
		t.Fatalf("the targets are %s", rec.Body.String())
	}
	if len(got.AssistantCoders) != 1 || len(got.AssistantCoders[0]) != 2 || got.AssistantCoders[0]["id"] != "claude" || got.AssistantCoders[0]["name"] != "Claude" {
		t.Fatalf("a new assistant may run on %s", rec.Body.String())
	}
	if got.Coders == nil || len(got.Coders) != 0 {
		t.Fatalf("the coders are %s", rec.Body.String())
	}
}

func browserExtensionCard(note string, picture string) string {
	card := assistant.Picked{
		Note: note, Title: "Shop", Address: "localhost:8095/cart", Viewport: "1440×900",
		Chips:    []assistant.PickedChip{{Kind: "element", Text: `button#buy "Buy"`}},
		Sections: []assistant.PickedSection{{Kind: "element", Title: `1. button#buy "Buy"`, Rows: []assistant.PickedRow{{Term: "Selector", Value: "#buy", Code: true}}}},
	}
	if picture != "" {
		card.Sections = append(card.Sections, assistant.PickedSection{Kind: "image", Title: "2. Drawing", Picture: picture})
	}
	raw, _ := json.Marshal(card)
	return string(raw)
}

func TestASendReachesTheAssistantWithTheTokensName(t *testing.T) {
	f := newBrowserExtensionFixture(t)
	token := f.makeToken(t, "Work laptop")
	rec := apiPicture(t, f, token, map[string]string{"to": "assistant", "id": f.assistant}, "drawing-localhost-8095 <x>.png", pngOf(t, 40, 20))
	var picture struct {
		File string `json:"file"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &picture) != nil || picture.File != "drawing-localhost-8095--x-.png" {
		t.Fatalf("the picture answered %d: %s", rec.Code, rec.Body.String())
	}
	message := "The button needs room.\n\n## Picked in the Browser\n\n- Picture: `" + picture.File + "`"
	rec = apiForm(f, browserExtensionAPIPath+"/send", token, url.Values{"to": {"assistant"}, "id": {f.assistant}, "message": {message}, "card": {browserExtensionCard("The button needs room.", picture.File)}, "picture": {picture.File}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"url":"/assistants/`+f.assistant+`"`) || !strings.Contains(rec.Body.String(), `"name":"The button needs room."`) {
		t.Fatalf("the send answered %d: %s", rec.Code, rec.Body.String())
	}
	thread, _ := f.s.assistants.Get(f.assistant)
	sent := thread.Messages[0]
	if sent.Content != message || sent.Via != "Work laptop" || sent.Picked == nil || len(sent.Attachments) != 1 || sent.Attachments[0].Name != picture.File || sent.Attachments[0].Media != "image" {
		t.Fatalf("the assistant got %+v", sent)
	}
	view := f.s.assistantMessageView(f.assistant, sent, false, true, "claude")
	if view.Picked == nil || view.Picked.Via != "Work laptop" || len(view.Attachments) != 0 {
		t.Fatalf("the thread shows %+v", view.Picked)
	}
}

func TestASendOutsideItsShapeIsRefused(t *testing.T) {
	f := newBrowserExtensionFixture(t)
	token := f.makeToken(t, "Laptop")
	note := "Look here"
	message := note + "\n\n## Picked in the Browser"
	card := browserExtensionCard(note, "")
	for name, c := range map[string]struct {
		form url.Values
		code int
	}{
		"no target":                    {url.Values{"id": {f.assistant}, "message": {message}, "card": {card}}, http.StatusBadRequest},
		"an unknown target kind":       {url.Values{"to": {"shell"}, "id": {f.assistant}, "message": {message}, "card": {card}}, http.StatusBadRequest},
		"an unknown assistant":         {url.Values{"to": {"assistant"}, "id": {"nobody"}, "message": {message}, "card": {card}}, http.StatusNotFound},
		"no message":                   {url.Values{"to": {"assistant"}, "id": {f.assistant}, "message": {"  "}, "card": {card}}, http.StatusBadRequest},
		"no card":                      {url.Values{"to": {"assistant"}, "id": {f.assistant}, "message": {message}}, http.StatusBadRequest},
		"a card of another message":    {url.Values{"to": {"assistant"}, "id": {f.assistant}, "message": {message}, "card": {browserExtensionCard("Else", "")}}, http.StatusBadRequest},
		"a card past its bound":        {url.Values{"to": {"assistant"}, "id": {f.assistant}, "message": {message}, "card": {strings.Repeat(" ", assistant.MaxPickedBytes+1)}}, http.StatusBadRequest},
		"a picture the message lacks":  {url.Values{"to": {"assistant"}, "id": {f.assistant}, "message": {message}, "card": {browserExtensionCard(note, "x.png")}}, http.StatusBadRequest},
		"a file not uploaded":          {url.Values{"to": {"assistant"}, "id": {f.assistant}, "message": {message}, "card": {card}, "picture": {"../tokens.json"}}, http.StatusBadRequest},
		"a message past its bound":     {url.Values{"to": {"assistant"}, "id": {f.assistant}, "message": {note + strings.Repeat("x", assistant.MaxPromptBytes)}, "card": {card}}, http.StatusBadRequest},
		"a coder that does not run":    {url.Values{"to": {"coder"}, "id": {"nobody"}, "message": {message}}, http.StatusGone},
		"a coder id out of its shape":  {url.Values{"to": {"coder"}, "id": {"no body"}, "message": {message}}, http.StatusNotFound},
		"a message too long for coder": {url.Values{"to": {"coder"}, "id": {"nobody"}, "message": {strings.Repeat("x", maxCoderPromptBytes+1)}}, http.StatusBadRequest},
	} {
		rec := apiForm(f, browserExtensionAPIPath+"/send", token, c.form)
		if rec.Code != c.code || len(errorOf(t, rec)) > maxBrowserExtensionRefusal {
			t.Fatalf("%s answered %d: %s", name, rec.Code, rec.Body.String())
		}
	}
	for name, c := range map[string]struct {
		fields map[string]string
		data   []byte
		code   int
	}{
		"text named .png":      {map[string]string{"to": "assistant", "id": f.assistant}, []byte("<svg onload=alert(1)>"), http.StatusBadRequest},
		"a GIF":                {map[string]string{"to": "assistant", "id": f.assistant}, []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"), http.StatusBadRequest},
		"a side past 8192":     {map[string]string{"to": "assistant", "id": f.assistant}, pngOf(t, 8193, 1), http.StatusRequestEntityTooLarge},
		"an unknown assistant": {map[string]string{"to": "assistant", "id": "nobody"}, pngOf(t, 2, 2), http.StatusNotFound},
		"a coder not running":  {map[string]string{"to": "coder", "id": "nobody"}, pngOf(t, 2, 2), http.StatusGone},
		"no target":            {map[string]string{"id": f.assistant}, pngOf(t, 2, 2), http.StatusBadRequest},
	} {
		rec := apiPicture(t, f, token, c.fields, "drawing.png", c.data)
		if rec.Code != c.code {
			t.Fatalf("%s answered %d: %s", name, rec.Code, rec.Body.String())
		}
		errorOf(t, rec)
	}
	if thread, _ := f.s.assistants.Get(f.assistant); len(thread.Messages) != 0 {
		t.Fatalf("a refused send left %+v", thread.Messages)
	}
	if dir, _ := f.s.assistants.UploadDir(f.assistant); dirHasFiles(dir) {
		t.Fatal("a refused picture was saved")
	}
}

func dirHasFiles(dir string) bool {
	entries, _ := os.ReadDir(dir)
	return len(entries) > 0
}

func TestANewAssistantRunsOnTheCoderTheExtensionNames(t *testing.T) {
	f := newBrowserExtensionFixture(t)
	token := f.makeToken(t, "Laptop")
	rec := apiForm(f, browserExtensionAPIPath+"/assistants", token, url.Values{"coder": {"claude"}})
	var made struct {
		ID, URL string
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &made) != nil || made.URL != "/assistants/"+made.ID {
		t.Fatalf("the new assistant answered %d: %s", rec.Code, rec.Body.String())
	}
	if got, err := f.s.assistants.Get(made.ID); err != nil || got.CoderID != "claude" {
		t.Fatalf("no assistant %s on claude: %+v, %v", made.ID, got.Summary, err)
	}
	for name, form := range map[string]url.Values{
		"no coder":             {},
		"a coder not here":     {"coder": {"copilot"}},
		"a coder out of shape": {"coder": {"claude\n"}},
	} {
		rec := apiForm(f, browserExtensionAPIPath+"/assistants", token, form)
		if rec.Code != http.StatusBadRequest || len(errorOf(t, rec)) > maxBrowserExtensionRefusal {
			t.Fatalf("%s answered %d: %s", name, rec.Code, rec.Body.String())
		}
	}
	if n := len(f.s.assistants.List()); n != 2 {
		t.Fatalf("%d assistants stand", n)
	}
}

func TestPictureFileNames(t *testing.T) {
	for sent, want := range map[string]string{
		"drawing-localhost-8095-20261112-101500.png": "drawing-localhost-8095-20261112-101500.png",
		"../../etc/passwd":                           "passwd.png",
		"..png":                                      "drawing.png",
		"a\x1b[2Jb.PNG":                              "a--2Jb.png",
		"bild ü.png":                                 "bild--.png",
		".hidden.png":                                "hidden.png",
		strings.Repeat("x", 300) + ".png":            strings.Repeat("x", 100) + ".png",
	} {
		if got := pictureFileName(sent); got != want {
			t.Errorf("%q is stored as %q, want %q", sent, got, want)
		}
	}
}
