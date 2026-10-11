// SPDX-License-Identifier: GPL-3.0-or-later

package mangabaka_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"

	metadatav1 "github.com/panelarr/meta/gen/panelarr/metadata/v1"
	"github.com/panelarr/meta/sdk/client"
	"github.com/panelarr/meta/sdk/conformance"
	"github.com/panelarr/meta/sdk/plugin"

	"github.com/panelarr/meta-mangabaka/internal/api"
	"github.com/panelarr/meta-mangabaka/internal/fixture"
	"github.com/panelarr/meta-mangabaka/internal/mangabaka"
)

// lookupPlugin points the plugin at the fake API, with no rate-limit
// waits and instant back-off so tests run fast.
func lookupPlugin(t *testing.T, apiSrv *fixture.APIServer) *mangabaka.Plugin {
	t.Helper()
	p := mangabaka.New()
	p.APIHook = func(c *api.Client) {
		c.SetLimit(api.KindGet, rate.Inf, 1)
		c.SetLimit(api.KindSearch, rate.Inf, 1)
		c.Sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	}
	if err := p.Configure(map[string]any{"download_dir": t.TempDir(), "api_url": apiSrv.URL}); err != nil {
		t.Fatal(err)
	}
	return p
}

func get(t *testing.T, p *mangabaka.Plugin, provider, id string) (*metadatav1.ProviderEntry, error) {
	t.Helper()
	res, err := p.Get(context.Background(), &metadatav1.GetRequest{Ref: &metadatav1.ExternalId{Provider: provider, Id: id}})
	return res.GetEntry(), err
}

func TestGetByMangaBakaID(t *testing.T) {
	p := lookupPlugin(t, fixture.NewAPIServer(t))
	e, err := get(t, p, "mangabaka", "101")
	if err != nil {
		t.Fatal(err)
	}
	if e.GetRef().GetId() != "101" || e.GetTitles()[0].GetText() != "The Clockwork Garden" || len(e.GetExternalIds()) == 0 {
		t.Fatalf("entry %v", e)
	}
	// A merged series comes back as merged, pointing at its target.
	m, err := get(t, p, "mangabaka", "103")
	if err != nil || m.GetState() != metadatav1.EntryState_ENTRY_STATE_MERGED || m.GetMergedInto().GetId() != "101" {
		t.Fatalf("merged: %v %v", m.GetState(), err)
	}
	for _, id := range []string{"424242", "not-a-number", "104"} { // 104 is deleted
		if _, err := get(t, p, "mangabaka", id); plugin.ErrorCode(err) != plugin.CodeNotFound {
			t.Errorf("%s: want not_found, got %v", id, err)
		}
	}
}

func TestGetByExternalID(t *testing.T) {
	apiSrv := fixture.NewAPIServer(t)
	p := lookupPlugin(t, apiSrv)
	cases := map[[2]string]string{
		{"anilist", "9001"}:                     "101",
		{"mal", "9002"}:                         "101",
		{"mangaupdates", "abc12xy"}:             "101",
		{"animeplanet", "the-clockwork-garden"}: "101",
		{"anilist", "9003"}:                     "102",
		// Only a merged series has this ID: the lookup follows the merge.
		{"kitsu", "7777"}: "101",
	}
	for ref, want := range cases {
		e, err := get(t, p, ref[0], ref[1])
		if err != nil || e.GetRef().GetId() != want {
			t.Errorf("%s:%s: got %v %v, want %s", ref[0], ref[1], e.GetRef().GetId(), err, want)
		}
	}
	paths := map[string]bool{}
	for _, r := range apiSrv.Requests() {
		paths[r.Path] = true
	}
	for _, p := range []string{"/v1/source/my-anime-list/9002", "/v1/source/manga-updates/abc12xy", "/v1/source/anime-planet/the-clockwork-garden", "/v1/series/101"} {
		if !paths[p] {
			t.Errorf("no request to %s", p)
		}
	}
	if _, err := get(t, p, "anilist", "1"); plugin.ErrorCode(err) != plugin.CodeNotFound {
		t.Errorf("unknown anilist id: %v", err)
	}
	if _, err := get(t, p, "shikimori", "9002"); plugin.ErrorCode(err) != plugin.CodeNotFound {
		t.Errorf("unsupported provider: %v", err)
	}
}

func TestSearch(t *testing.T) {
	apiSrv := fixture.NewAPIServer(t)
	p := lookupPlugin(t, apiSrv)
	ctx := context.Background()
	res, err := p.Search(ctx, &metadatav1.SearchRequest{Query: "Synthetic", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.GetEntries()) != 5 || res.GetNextPageToken() != "2" {
		t.Fatalf("page 1: %d entries, next %q", len(res.GetEntries()), res.GetNextPageToken())
	}
	seen := map[string]bool{}
	for _, e := range res.GetEntries() {
		seen[e.GetRef().GetId()] = true
	}
	res2, err := p.Search(ctx, &metadatav1.SearchRequest{Query: "Synthetic", Limit: 5, PageToken: "2"})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res2.GetEntries() {
		if seen[e.GetRef().GetId()] {
			t.Errorf("page 2 repeats %s", e.GetRef().GetId())
		}
	}

	res, err = p.Search(ctx, &metadatav1.SearchRequest{
		Query:          "synthetic",
		Types:          []metadatav1.EntryType{metadatav1.EntryType_ENTRY_TYPE_MANHWA, metadatav1.EntryType_ENTRY_TYPE_LIGHT_NOVEL, metadatav1.EntryType_ENTRY_TYPE_NOVEL},
		ContentRatings: []metadatav1.ContentRating{metadatav1.ContentRating_CONTENT_RATING_EROTIC},
		Limit:          100,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.GetEntries() {
		if e.GetContentRating() != metadatav1.ContentRating_CONTENT_RATING_EROTIC {
			t.Errorf("filter not applied: %v", e.GetContentRating())
		}
	}
	reqs := apiSrv.Requests()
	q, _ := url.ParseQuery(reqs[len(reqs)-1].Query)
	if strings.Join(q["type"], ",") != "manhwa,novel" || strings.Join(q["content_rating"], ",") != "erotica" || q.Get("limit") != "100" {
		t.Errorf("query %v", q)
	}

	if _, err := p.Search(ctx, &metadatav1.SearchRequest{}); plugin.ErrorCode(err) != plugin.CodeInvalidArgument {
		t.Errorf("empty search: %v", err)
	}
	if _, err := p.Search(ctx, &metadatav1.SearchRequest{Query: "x", PageToken: "zero"}); plugin.ErrorCode(err) != plugin.CodeInvalidArgument {
		t.Errorf("bad token: %v", err)
	}
	// Beyond MangaBaka's 12,000-result window: an empty page, no request.
	before := len(apiSrv.Requests())
	res, err = p.Search(ctx, &metadatav1.SearchRequest{Query: "x", Limit: 100, PageToken: "121"})
	if err != nil || len(res.GetEntries()) != 0 || len(apiSrv.Requests()) != before {
		t.Errorf("past the window: %v %v", res, err)
	}
}

func TestRateLimitedLookupReportsRetryHint(t *testing.T) {
	apiSrv := fixture.NewAPIServer(t)
	p := lookupPlugin(t, apiSrv)
	for range 5 {
		apiSrv.Fail429("30")
	}
	_, err := get(t, p, "mangabaka", "101")
	var pe *plugin.Error
	if !errors.As(err, &pe) || pe.Code != plugin.CodeResourceExhausted || pe.RetryAfter != 30*time.Second {
		t.Fatalf("got %v", err)
	}
	// After a 429 and a 500, the next answers succeed.
	apiSrv.Fail429("")
	apiSrv.Fail500()
	if _, err := get(t, p, "mangabaka", "101"); err != nil {
		t.Fatal(err)
	}
}

func TestLookupInfoAndSettings(t *testing.T) {
	p := mangabaka.New()
	info, _ := p.GetInfo(context.Background(), &metadatav1.GetInfoRequest{})
	var lookup *metadatav1.Ingestion
	for _, in := range info.GetInfo().GetIngestions() {
		if in.GetKind() == plugin.KindLookup {
			lookup = in
		}
	}
	if lookup == nil {
		t.Fatal("no lookup ingestion")
	}
	limits := map[string]int32{}
	for _, rl := range lookup.GetLimits().GetRateLimits() {
		if rl.GetPer().AsDuration() != time.Minute {
			t.Errorf("limit %s per %s", rl.GetOperation(), rl.GetPer().AsDuration())
		}
		limits[rl.GetOperation()] = rl.GetRequests()
	}
	if limits["search"] != 30 || limits["get"] != 180 {
		t.Errorf("limits %v", limits)
	}
	if err := p.Configure(map[string]any{"api_search_per_minute": float64(31)}); err == nil {
		t.Error("a search limit above MangaBaka's was accepted")
	}
	if err := p.Configure(map[string]any{"api_get_per_minute": float64(60), "api_timeout_seconds": float64(5)}); err != nil {
		t.Fatal(err)
	}
	if s := p.Settings(); s.APIGetPerMinute != 60 || s.APISearchPerMinute != 30 || s.APITimeoutSeconds != 5 {
		t.Errorf("settings %+v", s)
	}
}

func TestConformanceWithLookups(t *testing.T) {
	apiSrv := fixture.NewAPIServer(t)
	dumpSrv := fixture.NewServer(t)
	p := lookupPlugin(t, apiSrv)
	rep := conformance.Run(t, client.InProcess(p), conformance.Options{
		Settings:    map[string]any{"download_dir": t.TempDir(), "api_url": apiSrv.URL, "dump_url": dumpSrv.BaseURL()},
		SearchQuery: "garden",
		GetRef:      &metadatav1.ExternalId{Provider: "mangabaka", Id: "101"},
	})
	if rep.Events["upsert"] == 0 {
		t.Fatalf("events %v", rep.Events)
	}
	var searched, fetched bool
	for _, r := range apiSrv.Requests() {
		searched = searched || r.Path == "/v1/series/search"
		fetched = fetched || r.Path == "/v1/series/101"
	}
	if !searched || !fetched {
		t.Errorf("conformance didn't reach the API: search %v, get %v", searched, fetched)
	}
}
