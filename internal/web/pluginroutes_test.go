package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/pluginhost"
	"github.com/marein/dev-cockpit/internal/web/render"
	"github.com/marein/dev-cockpit/plugin"
)

// subtreePlugin is the smallest plugin whose subtree the router mounts, one
// page handler and one asset.
type subtreePlugin struct{}

func (subtreePlugin) ConfigureServe(s plugin.Serve) error {
	s.AddAssets(fstest.MapFS{
		"widget.js": &fstest.MapFile{Data: []byte("export default class {}")},
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plugin page"))
	})
	s.AddRoutes(mux)
	return nil
}

// pluginRouter registers the full route table over one fake plugin, with the
// session middleware the auth check reads. The extra sign-in route stands in
// for the login form, it puts the user into the session the way a successful
// login does, so the tests below can hold a real browser session.
func pluginRouter(t *testing.T) (*gin.Engine, staticAssetManifest) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	serves, err := pluginhost.ConfigureServe([]plugin.Named[plugin.ServePlugin]{{ID: "fake", Plugin: subtreePlugin{}}}, "", "", nil, nil)
	if err != nil {
		t.Fatalf("ConfigureServe() = %v", err)
	}
	assets, err := newStaticAssetManifest(serves)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	s := &Server{
		cfg:     config.Config{AuthUsername: "admin"},
		assets:  assets,
		plugins: serves,
	}
	r := gin.New()
	r.SetHTMLTemplate(render.HTMLTemplate(assets.assetPath, "test", "test", serves))
	r.Use(ginsessions.Sessions("session", cookie.NewStore([]byte("test-key"))))
	r.GET("/test/sign-in", func(c *gin.Context) {
		sess := ginsessions.Default(c)
		sess.Set(sessionUserKey, "admin")
		if err := sess.Save(); err != nil {
			t.Fatalf("save session: %v", err)
		}
		c.Status(http.StatusNoContent)
	})
	s.registerRoutes(r)
	return r, assets
}

// signIn answers the session cookie of a signed in browser.
func signIn(t *testing.T, r *gin.Engine) string {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/test/sign-in", nil))
	session := rec.Header().Get("Set-Cookie")
	if session == "" {
		t.Fatal("the sign-in answered no session cookie")
	}
	return session
}

// A plugin's subtree sits behind the session like every app route: without
// one, the page and the hashed asset URL both answer the login redirect, and
// with one both serve. The asset also stays out of the public static routes,
// where it would bypass the session.
func TestPluginSubtreeSitsBehindTheSession(t *testing.T) {
	r, assets := pluginRouter(t)
	hashedURL := assets.assetURL["/plugins/fake/assets/widget.js"]
	if hashedURL == "" || hashedURL == "/plugins/fake/assets/widget.js" {
		t.Fatalf("the plugin asset got no hashed URL: %q", hashedURL)
	}

	for _, path := range []string{"/plugins/fake/page", hashedURL} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
			t.Fatalf("unauthenticated GET %s answered %d %q, want the login redirect",
				path, rec.Code, rec.Header().Get("Location"))
		}
	}

	// The static routes are the public ones, a plugin asset must not be
	// reachable there under its exact URL, hashed or raw.
	for _, route := range r.Routes() {
		if route.Path == hashedURL || route.Path == "/plugins/fake/assets/widget.js" {
			t.Fatalf("the plugin asset is registered as its own route: %s", route.Path)
		}
	}

	session := signIn(t, r)
	page := httptest.NewRequest(http.MethodGet, "/plugins/fake/page", nil)
	page.Header.Set("Cookie", session)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, page)
	if rec.Code != http.StatusOK || rec.Body.String() != "plugin page" {
		t.Fatalf("signed in GET /plugins/fake/page answered %d %q", rec.Code, rec.Body.String())
	}
	asset := httptest.NewRequest(http.MethodGet, hashedURL, nil)
	asset.Header.Set("Cookie", session)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, asset)
	if rec.Code != http.StatusOK || rec.Body.String() != "export default class {}" {
		t.Fatalf("signed in GET %s answered %d %q", hashedURL, rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Fatalf("the hashed asset answered Cache-Control %q, want immutable", got)
	}
}

// An unsafe method into the subtree carries the CSRF token or is refused,
// even signed in: the plugin routes ride the same middleware chain as the
// app's own forms.
func TestPluginSubtreeRequiresTheCSRFToken(t *testing.T) {
	r, _ := pluginRouter(t)
	session := signIn(t, r)

	post := httptest.NewRequest(http.MethodPost, "/plugins/fake/page", nil)
	post.Header.Set("Cookie", session)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, post)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a POST without the CSRF token answered %d, want %d", rec.Code, http.StatusForbidden)
	}
}
