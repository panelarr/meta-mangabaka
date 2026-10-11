// SPDX-License-Identifier: GPL-3.0-or-later

// Package mapping turns MangaBaka series rows into records in meta's entry
// schema (ProviderEntry).
package mapping

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	metadatav1 "github.com/panelarr/meta/gen/panelarr/metadata/v1"
)

// Provider is the provider key for MangaBaka records.
const Provider = "mangabaka"

// Attribution is shown wherever MangaBaka data appears.
var Attribution = &metadatav1.Attribution{
	ProviderName: "MangaBaka",
	Url:          "https://mangabaka.org",
	License:      "CC BY-NC-SA 4.0",
	LicenseUrl:   "https://creativecommons.org/licenses/by-nc-sa/4.0/",
	Notice:       "Metadata from MangaBaka (mangabaka.org), CC BY-NC-SA 4.0. Data MangaBaka takes from other sites stays under those sites' terms.",
}

// Result is a mapped row.
type Result struct {
	Entry *metadatav1.ProviderEntry
	// MergedInto is the ID this series was merged into, if any.
	MergedInto string
}

// Ref returns the external ID of a MangaBaka series.
func Ref(id string) *metadatav1.ExternalId {
	return &metadatav1.ExternalId{Provider: Provider, Id: id}
}

// ID returns a row's series ID, or "" if it has none.
func ID(r Row) string {
	if id, ok := r.int("id"); ok {
		return strconv.FormatInt(id, 10)
	}
	return ""
}

// Series maps one row. It fails only when the row has no usable ID or no
// title at all.
func Series(r Row) (*Result, error) {
	id, ok := r.int("id")
	if !ok {
		return nil, fmt.Errorf("row without an id")
	}
	sid := strconv.FormatInt(id, 10)
	e := &metadatav1.ProviderEntry{
		Ref:              &metadatav1.ExternalId{Provider: Provider, Id: sid, Url: r.str("canonical_url")},
		Url:              r.str("canonical_url"),
		OriginalLanguage: language(r.str("original_language")),
		Attribution:      Attribution,
	}
	e.RawType = r.str("type")
	e.Type = entryType(e.RawType)
	e.RawStatus = r.str("status")
	e.Status = entryStatus(e.RawStatus)
	e.RawContentRating = r.str("content_rating")
	e.ContentRating = contentRating(e.RawContentRating)

	res := &Result{Entry: e}
	switch strings.ToLower(r.str("state")) {
	case "merged":
		e.State = metadatav1.EntryState_ENTRY_STATE_MERGED
	case "deleted":
		e.State = metadatav1.EntryState_ENTRY_STATE_REMOVED
	default:
		e.State = metadatav1.EntryState_ENTRY_STATE_ACTIVE
	}
	if into, ok := r.int("merged_with"); ok && into != id {
		e.State = metadatav1.EntryState_ENTRY_STATE_MERGED
		res.MergedInto = strconv.FormatInt(into, 10)
		e.MergedInto = Ref(res.MergedInto)
	} else if e.State == metadatav1.EntryState_ENTRY_STATE_MERGED {
		// Merged without a target: keep it as active rather than pointing
		// nowhere.
		e.State = metadatav1.EntryState_ENTRY_STATE_ACTIVE
	}

	e.Titles = titles(r, e.OriginalLanguage)
	if len(e.Titles) == 0 {
		return nil, fmt.Errorf("series %s has no title", sid)
	}
	if d := r.str("description"); d != "" {
		e.Descriptions = []*metadatav1.Description{{Language: "en", Text: d, Format: metadatav1.DescriptionFormat_DESCRIPTION_FORMAT_MARKDOWN}}
	}
	e.Credits = credits(r)
	e.Companies = publishers(r)
	if r.has("is_licensed") {
		e.Licenses = []*metadatav1.License{{Language: "en", Licensed: r.bool("is_licensed")}}
	}
	e.Tags = tags(r)
	e.Links = links(r)
	e.Relations = relations(r)
	e.Adaptations = adaptations(r)
	e.Progress = progress(r, e.Status)
	e.Dates = dates(r)
	e.Images = cover(r)
	e.ExternalIds = externalIDs(r)
	e.Stats = stats(r)
	if t, ok := timestamp(r.str("last_updated_at")); ok {
		e.UpdatedAt = timestamppb.New(t)
	}
	raw, err := rawStruct(r)
	if err != nil {
		return nil, fmt.Errorf("series %s: raw: %w", sid, err)
	}
	e.Raw = raw
	return res, nil
}

func entryType(s string) metadatav1.EntryType {
	switch strings.ToLower(s) {
	case "":
		return metadatav1.EntryType_ENTRY_TYPE_UNSPECIFIED
	case "manga":
		return metadatav1.EntryType_ENTRY_TYPE_MANGA
	case "manhwa":
		return metadatav1.EntryType_ENTRY_TYPE_MANHWA
	case "manhua":
		return metadatav1.EntryType_ENTRY_TYPE_MANHUA
	case "novel":
		return metadatav1.EntryType_ENTRY_TYPE_NOVEL
	case "light_novel", "light novel":
		return metadatav1.EntryType_ENTRY_TYPE_LIGHT_NOVEL
	case "oel":
		return metadatav1.EntryType_ENTRY_TYPE_OEL
	case "one_shot", "oneshot":
		return metadatav1.EntryType_ENTRY_TYPE_ONE_SHOT
	case "doujinshi":
		return metadatav1.EntryType_ENTRY_TYPE_DOUJINSHI
	}
	return metadatav1.EntryType_ENTRY_TYPE_OTHER
}

func entryStatus(s string) metadatav1.EntryStatus {
	switch strings.ToLower(s) {
	case "":
		return metadatav1.EntryStatus_ENTRY_STATUS_UNSPECIFIED
	case "releasing":
		return metadatav1.EntryStatus_ENTRY_STATUS_RELEASING
	case "completed":
		return metadatav1.EntryStatus_ENTRY_STATUS_COMPLETED
	case "hiatus":
		return metadatav1.EntryStatus_ENTRY_STATUS_HIATUS
	case "cancelled", "canceled":
		return metadatav1.EntryStatus_ENTRY_STATUS_CANCELLED
	case "upcoming":
		return metadatav1.EntryStatus_ENTRY_STATUS_UPCOMING
	}
	return metadatav1.EntryStatus_ENTRY_STATUS_UNKNOWN
}

func contentRating(s string) metadatav1.ContentRating {
	switch strings.ToLower(s) {
	case "":
		return metadatav1.ContentRating_CONTENT_RATING_UNSPECIFIED
	case "safe":
		return metadatav1.ContentRating_CONTENT_RATING_SAFE
	case "suggestive":
		return metadatav1.ContentRating_CONTENT_RATING_SUGGESTIVE
	case "erotica", "erotic":
		return metadatav1.ContentRating_CONTENT_RATING_EROTIC
	case "pornographic":
		return metadatav1.ContentRating_CONTENT_RATING_PORNOGRAPHIC
	}
	return metadatav1.ContentRating_CONTENT_RATING_UNKNOWN
}

// language turns MangaBaka's language codes into BCP 47: "unknown" becomes
// empty, legacy "<lang>-ro" (romanized) becomes "<lang>-Latn", and "es-la"
// (Latin American Spanish) becomes "es-419".
func language(s string) string {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "", "unknown":
		return ""
	case "es-la":
		return "es-419"
	}
	if base, ok := strings.CutSuffix(strings.ToLower(s), "-ro"); ok {
		return base + "-Latn"
	}
	return s
}

// romanizedOf is the romanized form of a language: "ja" -> "ja-Latn",
// "zh-Hans-CN" -> "zh-Latn".
func romanizedOf(lang string) string {
	if lang == "" {
		return ""
	}
	base, _, _ := strings.Cut(lang, "-")
	return strings.ToLower(base) + "-Latn"
}

type titleList struct {
	out  []*metadatav1.Title
	seen map[string]bool
}

func (l *titleList) add(t *metadatav1.Title) {
	t.Text = strings.TrimSpace(t.Text)
	if t.Text == "" {
		return
	}
	key := strings.ToLower(t.Text) + "|" + strings.ToLower(t.Language)
	if l.seen[key] {
		return
	}
	l.seen[key] = true
	l.out = append(l.out, t)
}

type mbTitle struct {
	Language  string   `json:"language"`
	Traits    []string `json:"traits"`
	Title     string   `json:"title"`
	Note      *string  `json:"note"`
	IsPrimary bool     `json:"is_primary"`
	Type      string   `json:"type"`
}

func traitKind(traits []string) metadatav1.TitleKind {
	has := func(s string) bool {
		for _, t := range traits {
			if strings.EqualFold(t, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("native"):
		return metadatav1.TitleKind_TITLE_KIND_NATIVE
	case has("official"):
		return metadatav1.TitleKind_TITLE_KIND_OFFICIAL
	case has("alternative"):
		return metadatav1.TitleKind_TITLE_KIND_ALTERNATIVE
	}
	return metadatav1.TitleKind_TITLE_KIND_UNKNOWN
}

func secondaryKind(s string) metadatav1.TitleKind {
	switch strings.ToLower(s) {
	case "official":
		return metadatav1.TitleKind_TITLE_KIND_OFFICIAL
	case "native":
		return metadatav1.TitleKind_TITLE_KIND_NATIVE
	case "romanized":
		return metadatav1.TitleKind_TITLE_KIND_ROMANIZED
	case "alternative":
		return metadatav1.TitleKind_TITLE_KIND_ALTERNATIVE
	case "unofficial":
		return metadatav1.TitleKind_TITLE_KIND_UNOFFICIAL
	}
	return metadatav1.TitleKind_TITLE_KIND_UNKNOWN
}

// titles collects every title. The main title comes first and is primary.
func titles(r Row, origLang string) []*metadatav1.Title {
	l := &titleList{seen: map[string]bool{}}
	var list []mbTitle
	r.json("titles", &list)

	main := r.str("title")
	if main != "" {
		t := &metadatav1.Title{Text: main, Kind: metadatav1.TitleKind_TITLE_KIND_OFFICIAL, IsPrimary: true}
		// Take language and kind from the matching entry in titles[].
		for _, x := range list {
			if strings.EqualFold(strings.TrimSpace(x.Title), main) {
				t.Language = language(x.Language)
				t.Traits = x.Traits
				if k := traitKind(x.Traits); k != metadatav1.TitleKind_TITLE_KIND_UNKNOWN {
					t.Kind = k
				}
				break
			}
		}
		l.add(t)
	}
	for _, x := range list {
		l.add(&metadatav1.Title{
			Text:      x.Title,
			Language:  language(x.Language),
			Kind:      traitKind(x.Traits),
			IsPrimary: x.IsPrimary,
			Traits:    x.Traits,
			Note:      deref(x.Note),
		})
	}
	if s := r.str("native_title"); s != "" {
		l.add(&metadatav1.Title{Text: s, Language: origLang, Kind: metadatav1.TitleKind_TITLE_KIND_NATIVE})
	}
	if s := r.str("romanized_title"); s != "" {
		l.add(&metadatav1.Title{Text: s, Language: romanizedOf(origLang), Kind: metadatav1.TitleKind_TITLE_KIND_ROMANIZED})
	}

	// Secondary titles: flattened into one column per language, or (when
	// the dump did not flatten them) one JSON object.
	byLang := map[string][]mbTitle{}
	var obj map[string][]mbTitle
	if r.json("secondary_titles", &obj) {
		for k, v := range obj {
			byLang[k] = append(byLang[k], v...)
		}
	}
	for col := range r {
		lang, ok := strings.CutPrefix(col, "secondary_titles_")
		if !ok {
			continue
		}
		var items []mbTitle
		if r.json(col, &items) {
			byLang[lang] = append(byLang[lang], items...)
		}
	}
	langs := make([]string, 0, len(byLang))
	for k := range byLang {
		langs = append(langs, k)
	}
	sort.Strings(langs)
	for _, k := range langs {
		for _, x := range byLang[k] {
			l.add(&metadatav1.Title{Text: x.Title, Language: language(k), Kind: secondaryKind(x.Type), Note: deref(x.Note)})
		}
	}
	return l.out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

func credits(r Row) []*metadatav1.Credit {
	var out []*metadatav1.Credit
	for _, c := range []struct {
		col  string
		role metadatav1.PersonRole
		raw  string
	}{
		{"authors", metadatav1.PersonRole_PERSON_ROLE_AUTHOR, "author"},
		{"artists", metadatav1.PersonRole_PERSON_ROLE_ARTIST, "artist"},
	} {
		for i, name := range r.strings(c.col) {
			out = append(out, &metadatav1.Credit{
				Person:  &metadatav1.Person{Name: name},
				Role:    c.role,
				RawRole: c.raw,
				Order:   int32(i), //nolint:gosec // small list
			})
		}
	}
	return out
}

type mbPublisher struct {
	ID           flexInt `json:"id"`
	Name         *string `json:"name"`
	CanonicalURL *string `json:"canonical_url"`
	Language     *string `json:"language"`
	Note         *string `json:"note"`
	Type         *string `json:"type"`
}

// publishers maps publishers. MangaBaka's company IDs become external IDs
// "publisher/<id>" at the mangabaka provider.
func publishers(r Row) []*metadatav1.CompanyCredit {
	var list []mbPublisher
	if !r.json("publishers", &list) {
		return nil
	}
	var out []*metadatav1.CompanyCredit
	for i, p := range list {
		name := deref(p.Name)
		if name == "" {
			continue
		}
		c := &metadatav1.Company{Name: name, Type: metadatav1.CompanyType_COMPANY_TYPE_PUBLISHER, RawType: "publisher"}
		if p.ID.ok {
			c.ExternalIds = []*metadatav1.ExternalId{{
				Provider: Provider,
				Id:       "publisher/" + strconv.FormatInt(p.ID.v, 10),
				Url:      deref(p.CanonicalURL),
			}}
		}
		raw := deref(p.Type)
		role := metadatav1.CompanyRole_COMPANY_ROLE_PUBLISHER
		if strings.EqualFold(raw, "original") {
			role = metadatav1.CompanyRole_COMPANY_ROLE_ORIGINAL_PUBLISHER
		}
		out = append(out, &metadatav1.CompanyCredit{
			Company:  c,
			Role:     role,
			RawRole:  raw,
			Order:    int32(i), //nolint:gosec // small list
			Note:     deref(p.Note),
			Language: language(deref(p.Language)),
		})
	}
	return out
}

type mbTag struct {
	ID              flexInt   `json:"id"`
	Name            string    `json:"name"`
	IsSpoiler       *bool     `json:"is_spoiler"`
	IsGenre         bool      `json:"is_genre"`
	IsExplicit      bool      `json:"is_explicit"`
	ImpliedByTagIDs []flexInt `json:"implied_by_tag_ids"`
	ParentID        flexInt   `json:"parent_id"`
	Level           int32     `json:"level"`
	NamePath        string    `json:"name_path"`
	SeriesCount     int64     `json:"series_count"`
	Description     *string   `json:"description"`
	ContentRating   string    `json:"content_rating"`
	Weight          string    `json:"weight"`
}

func tagWeight(s string) metadatav1.TagWeight {
	switch strings.ToLower(s) {
	case "core":
		return metadatav1.TagWeight_TAG_WEIGHT_CORE
	case "defining":
		return metadatav1.TagWeight_TAG_WEIGHT_DEFINING
	case "recurrent":
		return metadatav1.TagWeight_TAG_WEIGHT_RECURRENT
	case "incidental":
		return metadatav1.TagWeight_TAG_WEIGHT_INCIDENTAL
	case "unweighted":
		return metadatav1.TagWeight_TAG_WEIGHT_UNWEIGHTED
	}
	return metadatav1.TagWeight_TAG_WEIGHT_UNSPECIFIED
}

func tagKey(name string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(strings.ToLower(name))), " ")
}

var smallWords = map[string]bool{"of": true, "and": true, "the": true, "in": true, "a": true}

// genreName turns a v1 genre key ("slice_of_life", "sci-fi") into a display
// name ("Slice of Life", "Sci-Fi").
func genreName(key string) string {
	words := strings.Split(strings.ReplaceAll(key, "_", " "), " ")
	for i, w := range words {
		if i > 0 && smallWords[w] {
			continue
		}
		parts := strings.Split(w, "-")
		for j, p := range parts {
			if p != "" {
				parts[j] = strings.ToUpper(p[:1]) + p[1:]
			}
		}
		words[i] = strings.Join(parts, "-")
	}
	return strings.Join(words, " ")
}

// tags maps the series' genres and tags. Genres come first, in the order
// of the `genres` list (the provider's order), merged with the matching
// tags_v2 genre nodes; then the remaining tags_v2 nodes, each with its path
// ("Settings > Fantasy" is a tag named Fantasy under Settings, not the genre
// Fantasy). The v1 `tags` list is used only when there are no tags_v2.
func tags(r Row) []*metadatav1.TagAssignment {
	var list []mbTag
	r.json("tags_v2", &list)
	names := map[int64]string{}
	for _, t := range list {
		if t.ID.ok {
			names[t.ID.v] = strings.TrimSpace(t.Name)
		}
	}
	// Genre nodes of tags_v2 by name, to merge with the genre list.
	genreNodes := map[string]mbTag{}
	for _, t := range list {
		if t.IsGenre {
			genreNodes[tagKey(t.Name)] = t
		}
	}
	seen := map[string]bool{}
	var out []*metadatav1.TagAssignment
	addGenre := func(name string, node *mbTag) {
		key := "genre/" + tagKey(name)
		if name == "" || seen[key] {
			return
		}
		seen[key] = true
		if node == nil {
			out = append(out, &metadatav1.TagAssignment{Tag: &metadatav1.Tag{Name: name, IsGenre: true}})
			return
		}
		a := assignment(*node, names)
		a.Tag.Name, a.Tag.IsGenre, a.Tag.Path, a.Tag.Parent = name, true, nil, ""
		out = append(out, a)
	}
	for _, g := range r.strings("genres") {
		if node, ok := genreNodes[tagKey(g)]; ok {
			addGenre(strings.TrimSpace(node.Name), &node)
			continue
		}
		addGenre(genreName(g), nil)
	}
	for _, t := range list {
		if t.IsGenre {
			addGenre(strings.TrimSpace(t.Name), &t)
		}
	}
	for _, t := range list {
		name := strings.TrimSpace(t.Name)
		if t.IsGenre || name == "" {
			continue
		}
		a := assignment(t, names)
		key := "tag/" + strings.Join(append(lowerAll(a.Tag.Path), tagKey(name)), "/")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, a)
	}
	if len(list) == 0 {
		for _, t := range r.strings("tags") {
			key := "tag/" + tagKey(t)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, &metadatav1.TagAssignment{Tag: &metadatav1.Tag{Name: t}})
		}
	}
	return out
}

func lowerAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = tagKey(s)
	}
	return out
}

// assignment maps one tags_v2 node.
func assignment(t mbTag, names map[int64]string) *metadatav1.TagAssignment {
	name := strings.TrimSpace(t.Name)
	tag := &metadatav1.Tag{
		Name:          name,
		Description:   deref(t.Description),
		Level:         t.Level,
		IsGenre:       t.IsGenre,
		IsExplicit:    t.IsExplicit,
		ContentRating: contentRating(t.ContentRating),
		EntryCount:    t.SeriesCount,
		Parent:        tagParent(t, names),
		Path:          tagPath(t),
	}
	for _, id := range t.ImpliedByTagIDs {
		if n, ok := names[id.v]; ok && id.ok {
			tag.ImpliedBy = append(tag.ImpliedBy, n)
		}
	}
	if t.ID.ok {
		tag.ExternalIds = []*metadatav1.ExternalId{{Provider: Provider, Id: "tag/" + strconv.FormatInt(t.ID.v, 10)}}
	}
	return &metadatav1.TagAssignment{
		Tag:       tag,
		Weight:    tagWeight(t.Weight),
		RawWeight: t.Weight,
		IsSpoiler: t.IsSpoiler != nil && *t.IsSpoiler,
	}
}

// tagPath returns a tag's ancestors from name_path ("A > B > C" gives
// [A, B] for tag C; paths that list only the ancestors are kept whole).
func tagPath(t mbTag) []string {
	var parts []string
	for _, p := range strings.Split(t.NamePath, ">") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) > 0 && strings.EqualFold(parts[len(parts)-1], strings.TrimSpace(t.Name)) {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// tagParent finds a tag's parent: by ID among the record's tags, or else
// from the path ("A > B > C" has parent "B" for tag "C").
func tagParent(t mbTag, names map[int64]string) string {
	if t.ParentID.ok {
		if n, ok := names[t.ParentID.v]; ok {
			return n
		}
	}
	parts := strings.Split(t.NamePath, ">")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	if len(parts) >= 2 && strings.EqualFold(parts[len(parts)-1], strings.TrimSpace(t.Name)) {
		return parts[len(parts)-2]
	}
	if len(parts) >= 1 && parts[len(parts)-1] != "" && !strings.EqualFold(parts[len(parts)-1], strings.TrimSpace(t.Name)) {
		// Some paths list only the ancestors.
		return parts[len(parts)-1]
	}
	return ""
}

type mbLink struct {
	URL         string  `json:"url"`
	Name        *string `json:"name"`
	NameDisplay *string `json:"name_display"`
	Type        string  `json:"type"`
	Language    *string `json:"language"`
}

// linkType maps links_v2 types. Links typed "piracy" are never mapped.
func linkType(s string) (metadatav1.LinkType, bool) {
	switch strings.ToLower(s) {
	case "piracy":
		return 0, false
	case "retailer":
		return metadatav1.LinkType_LINK_TYPE_STORE, true
	case "publisher":
		return metadatav1.LinkType_LINK_TYPE_PUBLISHER, true
	case "webplatform":
		return metadatav1.LinkType_LINK_TYPE_READ, true
	case "info":
		return metadatav1.LinkType_LINK_TYPE_DATABASE, true
	case "social":
		return metadatav1.LinkType_LINK_TYPE_SOCIAL, true
	case "news":
		return metadatav1.LinkType_LINK_TYPE_NEWS, true
	case "other":
		return metadatav1.LinkType_LINK_TYPE_OTHER, true
	}
	return metadatav1.LinkType_LINK_TYPE_UNKNOWN, true
}

// links maps links_v2. The untyped v1 "links" list is not used, because it
// can't tell legitimate links from links to unlicensed copies.
func links(r Row) []*metadatav1.Link {
	var list []mbLink
	if !r.json("links_v2", &list) {
		return nil
	}
	var out []*metadatav1.Link
	for _, l := range list {
		if strings.TrimSpace(l.URL) == "" {
			continue
		}
		t, ok := linkType(l.Type)
		if !ok {
			continue
		}
		title := deref(l.NameDisplay)
		if title == "" {
			title = deref(l.Name)
		}
		out = append(out, &metadatav1.Link{
			Type:     t,
			RawType:  l.Type,
			Url:      strings.TrimSpace(l.URL),
			Title:    title,
			Language: language(deref(l.Language)),
		})
	}
	return out
}

type mbRelation struct {
	ToSeriesID   flexInt `json:"to_series_id"`
	RelationType string  `json:"relation_type"`
	IsManual     bool    `json:"is_manual"`
	Note         *string `json:"note"`
	Chronology   *string `json:"chronology"`
}

var relationTypes = map[string]metadatav1.RelationType{
	"adaptation":      metadatav1.RelationType_RELATION_TYPE_ADAPTATION,
	"alternative":     metadatav1.RelationType_RELATION_TYPE_ALTERNATIVE,
	"cameo":           metadatav1.RelationType_RELATION_TYPE_CAMEO,
	"character_focus": metadatav1.RelationType_RELATION_TYPE_CHARACTER_FOCUS,
	"compilation":     metadatav1.RelationType_RELATION_TYPE_COMPILATION,
	"contains":        metadatav1.RelationType_RELATION_TYPE_CONTAINS,
	"crossover":       metadatav1.RelationType_RELATION_TYPE_CROSSOVER,
	"expansion":       metadatav1.RelationType_RELATION_TYPE_EXPANSION,
	"main":            metadatav1.RelationType_RELATION_TYPE_MAIN,
	"main_story":      metadatav1.RelationType_RELATION_TYPE_MAIN,
	"parent":          metadatav1.RelationType_RELATION_TYPE_PARENT,
	"parody":          metadatav1.RelationType_RELATION_TYPE_PARODY,
	"prequel":         metadatav1.RelationType_RELATION_TYPE_PREQUEL,
	"reboot":          metadatav1.RelationType_RELATION_TYPE_REBOOT,
	"remake":          metadatav1.RelationType_RELATION_TYPE_REMAKE,
	"sequel":          metadatav1.RelationType_RELATION_TYPE_SEQUEL,
	"series":          metadatav1.RelationType_RELATION_TYPE_SERIES,
	"side_story":      metadatav1.RelationType_RELATION_TYPE_SIDE_STORY,
	"source":          metadatav1.RelationType_RELATION_TYPE_SOURCE,
	"spin_off":        metadatav1.RelationType_RELATION_TYPE_SPIN_OFF,
	"summary":         metadatav1.RelationType_RELATION_TYPE_SUMMARY,
	"uncollected":     metadatav1.RelationType_RELATION_TYPE_UNCOLLECTED,
	"other":           metadatav1.RelationType_RELATION_TYPE_OTHER,
}

func relationType(s string) metadatav1.RelationType {
	if t, ok := relationTypes[strings.ToLower(s)]; ok {
		return t
	}
	return metadatav1.RelationType_RELATION_TYPE_OTHER
}

func chronology(s string) metadatav1.RelationChronology {
	switch strings.ToLower(s) {
	case "narrative":
		return metadatav1.RelationChronology_RELATION_CHRONOLOGY_NARRATIVE
	case "release":
		return metadatav1.RelationChronology_RELATION_CHRONOLOGY_RELEASE
	case "unknown":
		return metadatav1.RelationChronology_RELATION_CHRONOLOGY_UNKNOWN
	}
	return metadatav1.RelationChronology_RELATION_CHRONOLOGY_UNSPECIFIED
}

// v1Buckets are the older relationship columns, used when there are no
// relationships_v2.
var v1Buckets = []string{"main_story", "adaptation", "prequel", "sequel", "side_story", "spin_off", "alternative", "other"}

func relations(r Row) []*metadatav1.Relation {
	var list []mbRelation
	r.json("relationships_v2", &list)
	seen := map[string]bool{}
	var out []*metadatav1.Relation
	add := func(target int64, raw string, rel *metadatav1.Relation) {
		key := strconv.FormatInt(target, 10) + "|" + strings.ToLower(raw)
		if seen[key] {
			return
		}
		seen[key] = true
		rel.Target = Ref(strconv.FormatInt(target, 10))
		rel.Type = relationType(raw)
		rel.RawType = raw
		out = append(out, rel)
	}
	for _, x := range list {
		if !x.ToSeriesID.ok {
			continue
		}
		add(x.ToSeriesID.v, x.RelationType, &metadatav1.Relation{
			Chronology: chronology(deref(x.Chronology)),
			Manual:     x.IsManual,
			Note:       deref(x.Note),
		})
	}
	if len(list) > 0 {
		return out
	}
	// v1: relationships_<bucket> columns (or one JSON object) of ID lists.
	buckets := map[string][]flexInt{}
	var obj map[string][]flexInt
	if r.json("relationships", &obj) {
		buckets = obj
	}
	for _, b := range v1Buckets {
		var ids []flexInt
		if r.json("relationships_"+b, &ids) {
			buckets[b] = append(buckets[b], ids...)
		}
	}
	for _, b := range v1Buckets {
		for _, id := range buckets[b] {
			if id.ok {
				add(id.v, b, &metadatav1.Relation{})
			}
		}
	}
	return out
}

func adaptations(r Row) []*metadatav1.Adaptation {
	start, end := r.str("anime_start"), r.str("anime_end")
	var obj struct {
		Start *string `json:"start"`
		End   *string `json:"end"`
	}
	if r.json("anime", &obj) {
		if start == "" {
			start = deref(obj.Start)
		}
		if end == "" {
			end = deref(obj.End)
		}
	}
	if start == "" && end == "" && !r.bool("has_anime") {
		return nil
	}
	a := &metadatav1.Adaptation{Type: metadatav1.AdaptationType_ADAPTATION_TYPE_ANIME, RawType: "anime"}
	if start != "" {
		a.Start = freeRef(start)
	}
	if end != "" {
		a.End = freeRef(end)
	}
	return []*metadatav1.Adaptation{a}
}

// freeRef keeps a free-text position ("Vol 1, Chap 1") as is.
func freeRef(s string) *metadatav1.Ref {
	return &metadatav1.Ref{
		Unit:      metadatav1.RefUnit_REF_UNIT_UNKNOWN,
		Qualifier: metadatav1.RefQualifier_REF_QUALIFIER_UNKNOWN,
		Value:     s,
	}
}

func progress(r Row, status metadatav1.EntryStatus) *metadatav1.Progress {
	p := &metadatav1.Progress{}
	if v := r.str("final_volume"); v != "" {
		p.Final = &metadatav1.Ref{Unit: metadatav1.RefUnit_REF_UNIT_VOLUME, RawUnit: "volume", Qualifier: metadatav1.RefQualifier_REF_QUALIFIER_EXACT, Value: v}
	}
	if v := r.str("total_chapters"); v != "" {
		ref := &metadatav1.Ref{Unit: metadatav1.RefUnit_REF_UNIT_CHAPTER, RawUnit: "chapter", Qualifier: metadatav1.RefQualifier_REF_QUALIFIER_EXACT, Value: v}
		// For a finished work the chapter count is the total; otherwise it
		// is the latest chapter so far.
		if status == metadatav1.EntryStatus_ENTRY_STATUS_COMPLETED || status == metadatav1.EntryStatus_ENTRY_STATUS_CANCELLED {
			p.Total = ref
		} else {
			p.Latest = ref
		}
	}
	if p.Final == nil && p.Total == nil && p.Latest == nil {
		return nil
	}
	return p
}

var datePattern = regexp.MustCompile(`^(\d{4})(?:-(\d{2}))?(?:-(\d{2}))?`)

func partialDate(s string, estimated bool) *metadatav1.PartialDate {
	m := datePattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return nil
	}
	d := &metadatav1.PartialDate{Estimated: estimated}
	y, _ := strconv.Atoi(m[1])
	d.Year = int32(y) //nolint:gosec // four digits
	if m[2] != "" {
		mo, _ := strconv.Atoi(m[2])
		d.Month = int32(mo) //nolint:gosec // two digits
	}
	if m[3] != "" {
		day, _ := strconv.Atoi(m[3])
		d.Day = int32(day) //nolint:gosec // two digits
	}
	if d.Year == 0 {
		return nil
	}
	return d
}

func dates(r Row) *metadatav1.Dates {
	d := &metadatav1.Dates{
		Start: partialDate(r.str("published_start_date"), r.bool("published_start_date_is_estimated")),
		End:   partialDate(r.str("published_end_date"), r.bool("published_end_date_is_estimated")),
	}
	var obj struct {
		StartDate *string `json:"start_date"`
		EndDate   *string `json:"end_date"`
		StartEst  *bool   `json:"start_date_is_estimated"`
		EndEst    *bool   `json:"end_date_is_estimated"`
	}
	if r.json("published", &obj) {
		if d.Start == nil {
			d.Start = partialDate(deref(obj.StartDate), obj.StartEst != nil && *obj.StartEst)
		}
		if d.End == nil {
			d.End = partialDate(deref(obj.EndDate), obj.EndEst != nil && *obj.EndEst)
		}
	}
	if d.Start == nil {
		if y, ok := r.int("year"); ok && y > 0 {
			d.Start = &metadatav1.PartialDate{Year: int32(y)} //nolint:gosec // a year
		}
	}
	if d.Start == nil && d.End == nil {
		return nil
	}
	return d
}

func cover(r Row) []*metadatav1.Image {
	img := &metadatav1.Image{
		Kind:      metadatav1.ImageKind_IMAGE_KIND_COVER,
		Url:       r.str("cover_raw_url"),
		Format:    r.str("cover_raw_format"),
		Blurhash:  r.str("cover_raw_blurhash"),
		Thumbhash: r.str("cover_raw_thumbhash"),
	}
	if v, ok := r.int("cover_raw_width"); ok {
		img.Width = int32(v) //nolint:gosec // image size
	}
	if v, ok := r.int("cover_raw_height"); ok {
		img.Height = int32(v) //nolint:gosec // image size
	}
	if v, ok := r.int("cover_raw_size"); ok {
		img.FileSize = v
	}
	for _, w := range []int32{150, 250, 350} {
		for d := int32(1); d <= 3; d++ {
			u := r.str(fmt.Sprintf("cover_x%d_x%d", w, d))
			if u != "" {
				img.Variants = append(img.Variants, &metadatav1.ImageVariant{Width: w, Density: d, Url: u})
			}
		}
	}
	if img.Url == "" && len(img.Variants) == 0 {
		return nil
	}
	return []*metadatav1.Image{img}
}

// sites maps MangaBaka's source names to provider keys and record URLs.
var sites = map[string]struct {
	provider string
	url      string
}{
	"anilist":            {"anilist", "https://anilist.co/manga/%s"},
	"my_anime_list":      {"mal", "https://myanimelist.net/manga/%s"},
	"manga_updates":      {"mangaupdates", "https://www.mangaupdates.com/series/%s"},
	"kitsu":              {"kitsu", "https://kitsu.app/manga/%s"},
	"anime_planet":       {"animeplanet", "https://www.anime-planet.com/manga/%s"},
	"anime_news_network": {"ann", "https://www.animenewsnetwork.com/encyclopedia/manga.php?id=%s"},
	"shikimori":          {"shikimori", "https://shikimori.one/mangas/%s"},
}

func externalIDs(r Row) []*metadatav1.ExternalId {
	var out []*metadatav1.ExternalId
	for col := range r {
		site, ok := strings.CutPrefix(col, "source_")
		if !ok {
			continue
		}
		site, ok = strings.CutSuffix(site, "_id")
		if !ok {
			continue
		}
		id := r.str(col)
		if id == "" || id == "0" {
			continue
		}
		x := &metadatav1.ExternalId{Provider: strings.ReplaceAll(site, "_", ""), Id: id}
		if s, ok := sites[site]; ok {
			x.Provider = s.provider
			x.Url = fmt.Sprintf(s.url, id)
		}
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out
}

func stats(r Row) *metadatav1.Stats {
	s := &metadatav1.Stats{}
	found := false
	if v, ok := r.float("rating"); ok {
		s.Rating = &v
		found = true
	}
	if v, ok := r.int("popularity_global_current"); ok && v > 0 {
		s.PopularityRank = &v
		found = true
	}
	if !found {
		return nil
	}
	return s
}

func timestamp(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// jsonColumns hold JSON text. In the raw record they are kept as JSON, not
// as strings.
func isJSONColumn(col string) bool {
	switch col {
	case "titles", "tags_v2", "links_v2", "publishers", "relationships_v2", "genres", "genres_v2",
		"tags", "authors", "artists", "anime", "secondary_titles", "relationships", "published", "popularity":
		return true
	}
	return strings.HasPrefix(col, "secondary_titles_") || strings.HasPrefix(col, "relationships_")
}

// rawStruct keeps the original row, with three exceptions:
//   - source_*_response columns (raw upstream responses in the full dump)
//     are dropped: they are large, and they are other sites' data;
//   - links typed "piracy" are dropped from links_v2, and the untyped v1
//     "links" list is dropped, so links to unlicensed copies never travel
//     on;
//   - JSON text columns are stored as JSON.
func rawStruct(r Row) (*structpb.Struct, error) {
	m := make(map[string]any, len(r))
	for col, v := range r {
		if strings.HasPrefix(col, "source_") && strings.HasSuffix(col, "_response") {
			continue
		}
		if col == "links" {
			continue
		}
		if s, ok := v.(string); ok && isJSONColumn(col) {
			var parsed any
			if json.Unmarshal([]byte(s), &parsed) == nil {
				if col == "links_v2" {
					parsed = withoutPiracy(parsed)
				}
				m[col] = parsed
				continue
			}
		}
		m[col] = v
	}
	return structpb.NewStruct(m)
}

func withoutPiracy(v any) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}
	out := make([]any, 0, len(list))
	for _, x := range list {
		if l, ok := x.(map[string]any); ok {
			if t, _ := l["type"].(string); strings.EqualFold(t, "piracy") {
				continue
			}
		}
		out = append(out, x)
	}
	return out
}
