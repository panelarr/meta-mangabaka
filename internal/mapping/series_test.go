// SPDX-License-Identifier: GPL-3.0-or-later

package mapping_test

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	metadatav1 "github.com/panelarr/meta/gen/panelarr/metadata/v1"

	"github.com/panelarr/meta-mangabaka/internal/dump"
	"github.com/panelarr/meta-mangabaka/internal/fixture"
	"github.com/panelarr/meta-mangabaka/internal/mapping"
)

// mapAll maps every fixture row, keyed by ID. Rows that fail are in errs.
func mapAll(t *testing.T) (map[int]*mapping.Result, map[int]error) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "series.sqlite")
	if err := fixture.Build(ctx, path); err != nil {
		t.Fatal(err)
	}
	r, err := dump.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	got := map[int]*mapping.Result{}
	errs := map[int]error{}
	err = r.Each(ctx, func(row dump.Row) error {
		id, _ := strconv.Atoi(mapping.ID(mapping.Row(row)))
		res, err := mapping.Series(mapping.Row(row))
		if err != nil {
			errs[id] = err
			return nil
		}
		got[id] = res
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got, errs
}

func titleSet(e *metadatav1.ProviderEntry) map[string]*metadatav1.Title {
	m := map[string]*metadatav1.Title{}
	for _, t := range e.GetTitles() {
		m[t.GetText()+"|"+t.GetLanguage()] = t
	}
	return m
}

func TestFullSeries(t *testing.T) {
	got, _ := mapAll(t)
	e := got[fixture.IDFull].Entry

	if e.GetRef().GetProvider() != "mangabaka" || e.GetRef().GetId() != "101" || e.GetUrl() == "" {
		t.Fatalf("ref %v url %q", e.GetRef(), e.GetUrl())
	}
	if e.GetType() != metadatav1.EntryType_ENTRY_TYPE_MANGA || e.GetRawType() != "manga" ||
		e.GetStatus() != metadatav1.EntryStatus_ENTRY_STATUS_COMPLETED ||
		e.GetContentRating() != metadatav1.ContentRating_CONTENT_RATING_SUGGESTIVE {
		t.Fatalf("enums: %v %v %v", e.GetType(), e.GetStatus(), e.GetContentRating())
	}

	// Titles: main first and primary, every language, no duplicates.
	first := e.GetTitles()[0]
	if first.GetText() != "The Clockwork Garden" || !first.GetIsPrimary() || first.GetLanguage() != "en" {
		t.Fatalf("first title %v", first)
	}
	ts := titleSet(e)
	for key, kind := range map[string]metadatav1.TitleKind{
		"時計仕掛けの庭|ja":                   metadatav1.TitleKind_TITLE_KIND_NATIVE,
		"Tokei Jikake no Niwa|ja-Latn": metadatav1.TitleKind_TITLE_KIND_UNKNOWN,
		"Le Jardin Mécanique|fr":       metadatav1.TitleKind_TITLE_KIND_OFFICIAL,
		"Tokei-jikake no Niwa|ja-Latn": metadatav1.TitleKind_TITLE_KIND_ROMANIZED,
		"Clockwork Garden|":            metadatav1.TitleKind_TITLE_KIND_UNKNOWN,
		"TCG|en":                       metadatav1.TitleKind_TITLE_KIND_ALTERNATIVE,
	} {
		tt, ok := ts[key]
		if !ok {
			t.Errorf("missing title %q in %v", key, ts)
			continue
		}
		if tt.GetKind() != kind {
			t.Errorf("title %q kind %v, want %v", key, tt.GetKind(), kind)
		}
	}
	if len(ts) != len(e.GetTitles()) {
		t.Errorf("duplicate titles: %v", e.GetTitles())
	}
	if ts["Le Jardin Mécanique|fr"].GetNote() != "French edition" {
		t.Errorf("note lost")
	}

	if len(e.GetDescriptions()) != 1 || e.GetDescriptions()[0].GetFormat() != metadatav1.DescriptionFormat_DESCRIPTION_FORMAT_MARKDOWN {
		t.Errorf("descriptions %v", e.GetDescriptions())
	}

	// People: plain names, roles, no IDs.
	var credits []string
	for _, c := range e.GetCredits() {
		credits = append(credits, c.GetPerson().GetName()+":"+c.GetRawRole())
		if len(c.GetPerson().GetExternalIds()) != 0 {
			t.Errorf("person with ids: %v", c)
		}
	}
	if strings.Join(credits, ",") != "Aiko Fictional:author,Aiko Fictional:artist,Ben Madeup:artist" {
		t.Errorf("credits %v", credits)
	}

	// Publishers: original vs English, ID as an external ID, nameless one skipped.
	if len(e.GetCompanies()) != 2 {
		t.Fatalf("companies %v", e.GetCompanies())
	}
	orig := e.GetCompanies()[0]
	if orig.GetRole() != metadatav1.CompanyRole_COMPANY_ROLE_ORIGINAL_PUBLISHER || orig.GetRawRole() != "Original" ||
		orig.GetCompany().GetExternalIds()[0].GetId() != "publisher/7001" || orig.GetLanguage() != "ja" {
		t.Errorf("original publisher %v", orig)
	}
	if e.GetCompanies()[1].GetRole() != metadatav1.CompanyRole_COMPANY_ROLE_PUBLISHER {
		t.Errorf("english publisher %v", e.GetCompanies()[1])
	}

	// Tags: hierarchy, flags, weights, v1 genres merged without duplicates.
	tags := map[string]*metadatav1.TagAssignment{}
	for _, ta := range e.GetTags() {
		if tags[ta.GetTag().GetName()] != nil {
			t.Errorf("duplicate tag %q", ta.GetTag().GetName())
		}
		tags[ta.GetTag().GetName()] = ta
	}
	if g := tags["Fantasy"]; g == nil || !g.GetTag().GetIsGenre() || g.GetWeight() != metadatav1.TagWeight_TAG_WEIGHT_CORE {
		t.Errorf("Fantasy %v", g)
	}
	if g := tags["Sci-Fi"]; g == nil || !g.GetTag().GetIsGenre() {
		t.Errorf("v1 genre sci-fi not added: %v", tags)
	}
	if _, dup := tags["Slice Of Life"]; dup {
		t.Errorf("v1 genre duplicated a tags_v2 genre")
	}
	if g := tags["Gardens"]; g == nil || g.GetTag().GetParent() != "Setting" || g.GetTag().GetExternalIds()[0].GetId() != "tag/11" {
		t.Errorf("Gardens %v", g)
	}
	if g := tags["Clock Towers"]; g == nil || g.GetTag().GetParent() != "Buildings" || strings.Join(g.GetTag().GetImpliedBy(), ",") != "Gardens" {
		t.Errorf("Clock Towers %v", g)
	}
	if g := tags["Time Loop Twist"]; g == nil || !g.GetIsSpoiler() {
		t.Errorf("spoiler %v", g)
	}
	if g := tags["Partial Nudity"]; g == nil || !g.GetTag().GetIsExplicit() ||
		g.GetTag().GetContentRating() != metadatav1.ContentRating_CONTENT_RATING_SUGGESTIVE {
		t.Errorf("explicit %v", g)
	}

	// Links: piracy dropped, everywhere.
	for _, l := range e.GetLinks() {
		if l.GetUrl() == fixture.PiracyURL {
			t.Errorf("piracy link mapped")
		}
	}
	if len(e.GetLinks()) != 3 || e.GetLinks()[1].GetType() != metadatav1.LinkType_LINK_TYPE_STORE || e.GetLinks()[2].GetLanguage() != "" {
		t.Errorf("links %v", e.GetLinks())
	}
	raw, _ := e.GetRaw().MarshalJSON()
	if strings.Contains(string(raw), fixture.PiracyURL) {
		t.Errorf("piracy link kept in raw")
	}
	if strings.Contains(string(raw), "upstream payload") {
		t.Errorf("source response kept in raw")
	}
	if !strings.Contains(string(raw), `"tags_v2":[{`) {
		t.Errorf("raw JSON columns not stored as JSON: %.200s", raw)
	}

	// Relations: v2 wins over v1; unknown types become other with raw kept.
	if len(e.GetRelations()) != 2 {
		t.Fatalf("relations %v", e.GetRelations())
	}
	r0, r1 := e.GetRelations()[0], e.GetRelations()[1]
	if r0.GetType() != metadatav1.RelationType_RELATION_TYPE_SEQUEL || r0.GetTarget().GetId() != "102" ||
		r0.GetChronology() != metadatav1.RelationChronology_RELATION_CHRONOLOGY_UNKNOWN {
		t.Errorf("relation %v", r0)
	}
	if r1.GetType() != metadatav1.RelationType_RELATION_TYPE_OTHER || r1.GetRawType() != "brand_new_kind" || !r1.GetManual() || r1.GetNote() != "made up" {
		t.Errorf("relation %v", r1)
	}

	// Adaptation, progress, dates, licence.
	if a := e.GetAdaptations(); len(a) != 1 || a[0].GetStart().GetValue() != "Vol 1, Chap 1" || a[0].GetEnd().GetValue() == "" {
		t.Errorf("adaptations %v", a)
	}
	if p := e.GetProgress(); p.GetFinal().GetValue() != "12" || p.GetTotal().GetValue() != "108" || p.GetLatest() != nil {
		t.Errorf("progress %v", p)
	}
	d := e.GetDates()
	if d.GetStart().GetYear() != 2011 || d.GetStart().GetMonth() != 4 || d.GetStart().GetDay() != 5 || d.GetStart().GetEstimated() ||
		d.GetEnd().GetYear() != 2019 || !d.GetEnd().GetEstimated() {
		t.Errorf("dates %v", d)
	}
	if l := e.GetLicenses(); len(l) != 1 || !l[0].GetLicensed() {
		t.Errorf("licenses %v", l)
	}

	// Cover with placeholders and variants.
	img := e.GetImages()[0]
	if img.GetUrl() == "" || img.GetBlurhash() == "" || img.GetThumbhash() == "" || img.GetWidth() != 600 || img.GetFileSize() != 123456 || len(img.GetVariants()) != 4 {
		t.Errorf("cover %v", img)
	}
	if v := img.GetVariants()[1]; v.GetWidth() != 150 || v.GetDensity() != 2 {
		t.Errorf("variant %v", v)
	}

	// External IDs from the source columns, with provider keys and URLs.
	ids := map[string]string{}
	for _, x := range e.GetExternalIds() {
		ids[x.GetProvider()] = x.GetId()
		if x.GetUrl() == "" {
			t.Errorf("no url for %v", x)
		}
	}
	want := map[string]string{"anilist": "9001", "mal": "9002", "mangaupdates": "abc12xy", "animeplanet": "the-clockwork-garden", "shikimori": "9002"}
	for k, v := range want {
		if ids[k] != v {
			t.Errorf("external id %s = %q, want %q (all %v)", k, ids[k], v, ids)
		}
	}
	if _, ok := ids["kitsu"]; ok {
		t.Errorf("null kitsu id mapped")
	}

	if s := e.GetStats(); s.GetRating() != 81.5 || s.GetPopularityRank() != 42 {
		t.Errorf("stats %v", s)
	}
	if e.GetUpdatedAt().AsTime().Year() != 2026 {
		t.Errorf("updated_at %v", e.GetUpdatedAt())
	}
	if e.GetAttribution().GetLicense() != "CC BY-NC-SA 4.0" {
		t.Errorf("attribution %v", e.GetAttribution())
	}
}

func TestStatesAndMerges(t *testing.T) {
	got, errs := mapAll(t)
	m := got[fixture.IDMergedText]
	if m.MergedInto != "101" || m.Entry.GetState() != metadatav1.EntryState_ENTRY_STATE_MERGED || m.Entry.GetMergedInto().GetId() != "101" {
		t.Errorf("merged (text): %v %v", m.MergedInto, m.Entry.GetState())
	}
	if m := got[fixture.IDMergedInt]; m.MergedInto != "102" {
		t.Errorf("merged (int): %v", m.MergedInto)
	}
	if got[fixture.IDDeleted].Entry.GetState() != metadatav1.EntryState_ENTRY_STATE_REMOVED {
		t.Errorf("deleted state")
	}
	if got[fixture.IDFull].Entry.GetState() != metadatav1.EntryState_ENTRY_STATE_ACTIVE {
		t.Errorf("active state")
	}
	if _, ok := errs[fixture.IDNoTitle]; !ok || len(errs) != fixture.ExpectedSkipped {
		t.Errorf("errors %v", errs)
	}
}

func TestSparseAndUnknownValues(t *testing.T) {
	got, _ := mapAll(t)
	e := got[fixture.IDSparse].Entry
	if e.GetType() != metadatav1.EntryType_ENTRY_TYPE_OTHER || e.GetRawType() != "something_new" {
		t.Errorf("type %v %q", e.GetType(), e.GetRawType())
	}
	if e.GetStatus() != metadatav1.EntryStatus_ENTRY_STATUS_UNSPECIFIED || e.GetContentRating() != metadatav1.ContentRating_CONTENT_RATING_EROTIC {
		t.Errorf("status %v rating %v", e.GetStatus(), e.GetContentRating())
	}
	if len(e.GetCredits()) != 0 || len(e.GetTags()) != 0 || len(e.GetImages()) != 0 || e.GetDates() != nil || e.GetProgress() != nil || e.GetStats() != nil {
		t.Errorf("sparse entry has data: %v", e)
	}
}

func TestLanguagesAndV1Fallbacks(t *testing.T) {
	got, _ := mapAll(t)

	kr := got[fixture.IDManhwa].Entry
	ts := titleSet(kr)
	if ts["Ne-on Deungbul|ko-Latn"] == nil || ts["네온 등불|ko"].GetKind() != metadatav1.TitleKind_TITLE_KIND_NATIVE {
		t.Errorf("korean titles %v", kr.GetTitles())
	}
	if len(ts) != len(kr.GetTitles()) || len(kr.GetTitles()) != 3 {
		t.Errorf("korean titles not deduplicated: %v", kr.GetTitles())
	}
	if s := kr.GetDates().GetStart(); s.GetYear() != 2015 || s.GetMonth() != 3 || !s.GetEstimated() {
		t.Errorf("iso date %v", s)
	}
	if l := kr.GetLicenses(); len(l) != 1 || l[0].GetLicensed() {
		t.Errorf("unlicensed %v", l)
	}

	cn := got[fixture.IDManhua].Entry
	ts = titleSet(cn)
	if ts["Río de Tinta|es-419"] == nil || ts["Mo Zhi He|zh-Latn"] == nil || ts["墨之河|zh-Hans-CN"] == nil {
		t.Errorf("chinese titles %v", cn.GetTitles())
	}
	if ts["墨之河|zh-Hant"] == nil {
		t.Errorf("same text in another script variant should be kept: %v", cn.GetTitles())
	}

	seq := got[fixture.IDSequel].Entry
	rels := map[string]string{}
	for _, r := range seq.GetRelations() {
		rels[r.GetRawType()+":"+r.GetTarget().GetId()] = r.GetType().String()
	}
	if len(rels) != 3 || rels["prequel:101"] != "RELATION_TYPE_PREQUEL" || rels["side_story:6666"] != "RELATION_TYPE_SIDE_STORY" {
		t.Errorf("v1 relations %v", rels)
	}
	if a := seq.GetAdaptations(); len(a) != 1 || a[0].GetStart() != nil {
		t.Errorf("has_anime adaptation %v", a)
	}
	if p := seq.GetProgress(); p.GetLatest().GetValue() != "20" || p.GetTotal() != nil {
		t.Errorf("releasing progress %v", p)
	}
	if seq.GetDates().GetStart().GetYear() != 2020 {
		t.Errorf("year fallback %v", seq.GetDates())
	}
	if ids := seq.GetExternalIds(); len(ids) != 1 || ids[0].GetId() != "9003" {
		t.Errorf("text source id %v", ids)
	}

	nv := got[fixture.IDNovelV1Tags].Entry
	var names []string
	for _, ta := range nv.GetTags() {
		names = append(names, ta.GetTag().GetName())
	}
	if strings.Join(names, ",") != "Adventure,Flight,Paper" {
		t.Errorf("v1 tags %v", names)
	}
	if len(nv.GetLinks()) != 0 {
		t.Errorf("untyped v1 links mapped: %v", nv.GetLinks())
	}
}

func TestEveryGeneratedRowMaps(t *testing.T) {
	got, _ := mapAll(t)
	for i := range fixture.GeneratedCount {
		res, ok := got[fixture.GeneratedFirst+i]
		if !ok {
			t.Fatalf("row %d missing", fixture.GeneratedFirst+i)
		}
		e := res.Entry
		if e.GetType() == metadatav1.EntryType_ENTRY_TYPE_UNSPECIFIED || len(e.GetTitles()) != 1 || len(e.GetTags()) != 1 {
			t.Errorf("row %s: %v", e.GetRef().GetId(), e)
		}
	}
}
