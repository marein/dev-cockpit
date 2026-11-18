package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/marein/dev-cockpit/internal/browser"
	"github.com/marein/dev-cockpit/internal/browserextension"
)

func TestTheBrowserExtensionIsHandedOutForEachBrowser(t *testing.T) {
	f := newBrowserExtensionFixture(t)
	f.s.version = "dev"
	format, err := staticAssets.ReadFile("static/js/dc/browser-context.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"chrome", "firefox", "safari"} {
		rec := f.send(httptest.NewRequest(http.MethodGet, "/settings/browser/extension/"+name+".zip", nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/zip" {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Header().Get("Content-Type"))
		}
		if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="dev-cockpit-browser-extension-`+name+`-`+browserextension.Version+`.zip"` {
			t.Fatalf("%s: %s", name, got)
		}
		files := unzipped(t, rec.Body.Bytes())
		if !bytes.Equal(files["pick.js"], browser.PickScript()) || !bytes.HasPrefix(files["console.js"], browser.ConsoleScript()) || !bytes.Equal(files["draw.js"], browser.DrawScript()) || !bytes.Equal(files["browser-context.js"], format) {
			t.Fatalf("%s: the zip does not carry the cockpit's picker, console catch, drawing and format", name)
		}
		var m struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(files["manifest.json"], &m); err != nil || m.Version != browserextension.Version {
			t.Fatalf("%s: manifest %s", name, files["manifest.json"])
		}
	}
	for _, path := range []string{"/settings/browser/extension/edge.zip", "/settings/browser/extension/chrome"} {
		if rec := f.send(httptest.NewRequest(http.MethodGet, path, nil)); rec.Code != http.StatusNotFound {
			t.Fatalf("%s answered %d", path, rec.Code)
		}
	}
	page := f.page(t)
	for _, want := range []string{`<code data-browser-extension-version>` + browserextension.Version + `</code>`, `href="/settings/browser/extension/chrome.zip" download data-no-pe`, `href="/settings/browser/extension/firefox.zip" download data-no-pe`, `href="/settings/browser/extension/safari.zip" download data-no-pe`, `id="settings-browser-extension"`} {
		if !bytes.Contains([]byte(page), []byte(want)) {
			t.Fatalf("the page lacks %s", want)
		}
	}
}

func TestATokenGetsNoBrowserExtensionDownload(t *testing.T) {
	f := newBrowserExtensionFixture(t)
	token := f.makeToken(t, "Laptop")
	if rec := api(f, http.MethodGet, "/settings/browser/extension/chrome.zip", token, nil, ""); rec.Code == http.StatusOK {
		t.Fatalf("a token got the zip: %d", rec.Code)
	}
}
