package web

import (
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/share"
)

// A hand built form grants on its own what the dialog would let it, no more:
// New link and Edit read a switch only off a project picked or on that link,
// never off an assistant, a project left out or one of another link, and a
// save changes its own link alone.
func TestAHandBuiltFormGrantsOnlyWhatItsEntriesMay(t *testing.T) {
	sa := newSharedAssistant(t)
	lookup := func(token string) share.Share {
		t.Helper()
		sh, err := sa.s.shares.Lookup(token)
		if err != nil {
			t.Fatal(err)
		}
		return sh
	}
	grants := func(sh share.Share, kind share.Kind, target string) []share.Permission {
		r, _ := sh.Resource(kind, target)
		return r.Permissions
	}
	made := func(form url.Values) share.Share {
		t.Helper()
		form.Set("amount", "1")
		form.Set("unit", "hours")
		rec := sa.post(settingsSharingNewPath, form)
		token := shownToken(rec.Body.String())
		if rec.Code != http.StatusOK || token == "" {
			t.Fatalf("making %v answered %d", form, rec.Code)
		}
		return lookup(token)
	}
	onAssistant := made(url.Values{"name": {"Anna"}, "project": {"shop"}, "assistant": {sa.id}, "grant:comments": {sa.id}})
	if len(grants(onAssistant, share.KindProject, "shop")) != 0 || len(grants(onAssistant, share.KindAssistant, sa.id)) != 0 {
		t.Fatalf("a switch naming the assistant granted %+v", onAssistant.Resources)
	}
	leftOut := made(url.Values{"name": {"Bert"}, "project": {"shop"}, "grant:comments": {"blog"}})
	if len(leftOut.Resources) != 1 || len(grants(leftOut, share.KindProject, "shop")) != 0 {
		t.Fatalf("a switch naming a project left out made %+v", leftOut.Resources)
	}

	mine, mineToken, err := sa.s.shares.Create("Carla", []share.Resource{{Kind: share.KindProject, Target: "shop"}, {Kind: share.KindAssistant, Target: sa.id}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, otherToken, err := sa.s.shares.Create("Dora", []share.Resource{{Kind: share.KindProject, Target: "blog", Permissions: []share.Permission{share.Comments}}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	save := func(form url.Values) {
		t.Helper()
		form.Set("form", "save")
		if rec := sa.post(shareEditPath(mine.ID)+"?modal=1", form); rec.Code != http.StatusOK {
			t.Fatalf("saving %v answered %d %s", form, rec.Code, rec.Body.String())
		}
	}
	save(url.Values{"resource": {"assistant:" + sa.id, "project:blog"}, "grant:comments": {sa.id, "blog"}, "drop": {"project:blog"}})
	got, other := lookup(mineToken), lookup(otherToken)
	if !slices.Equal(got.Targets(share.KindProject), []string{"shop"}) || len(grants(got, share.KindAssistant, sa.id)) != 0 || len(grants(got, share.KindProject, "shop")) != 0 {
		t.Fatalf("a save naming the assistant and another link's project made %+v", got.Resources)
	}
	if !slices.Equal(other.Targets(share.KindProject), []string{"blog"}) || !slices.Equal(grants(other, share.KindProject, "blog"), []share.Permission{share.Comments}) {
		t.Fatalf("a save of one link changed another to %+v", other.Resources)
	}
	save(url.Values{"resource": {"project:shop", "assistant:" + sa.id}, "project": {"blog"}, "assistant": {sa.other}, "grant:comments": {"blog", sa.other}})
	got, other = lookup(mineToken), lookup(otherToken)
	if !slices.Equal(grants(got, share.KindProject, "blog"), []share.Permission{share.Comments}) || len(grants(got, share.KindProject, "shop")) != 0 || len(grants(got, share.KindAssistant, sa.other)) != 0 {
		t.Fatalf("the Add granted %+v", got.Resources)
	}
	if len(other.Resources) != 1 || !slices.Equal(grants(other, share.KindProject, "blog"), []share.Permission{share.Comments}) {
		t.Fatalf("the Add changed another link to %+v", other.Resources)
	}
}
