// SPDX-License-Identifier: GPL-3.0-or-later

// Package fixture builds a synthetic MangaBaka-style dump for tests. Every
// series in it is made up; the layout (one wide "series" table, JSON text
// columns, flattened objects) follows the real dump so the reader and the
// mapper are tested against the same shapes.
package fixture

import (
	"context"
	"crypto/sha1" //nolint:gosec // the dump is published with SHA-1 checksums
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite" // pure-Go SQLite
)

// Series is one made-up row: column -> value. Slices and maps are stored
// as JSON text, as in the dump.
type Series map[string]any

func j(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// Well-known IDs in the fixture.
const (
	IDFull          = 101 // every field filled
	IDSequel        = 102 // v1 relationship columns only, has_anime without details
	IDMergedText    = 103 // merged into IDFull, merged_with stored as text
	IDDeleted       = 104 // state deleted
	IDNoTitle       = 105 // no title at all: skipped by the plugin
	IDSparse        = 106 // almost everything null, unknown type value
	IDManhwa        = 107 // Korean, ISO timestamps, estimated dates
	IDManhua        = 108 // Chinese, regional language codes
	IDMergedInt     = 109 // merged into IDSequel, merged_with stored as a number
	IDNovelV1Tags   = 110 // only v1 tags and genres
	GeneratedFirst  = 1000
	GeneratedCount  = 24
	PiracyURL       = "https://unlicensed-copies.example/read/101"
	KitsuOnMerged   = 7777
	ExpectedSkipped = 1
)

// All returns every series in the fixture.
func All() []Series {
	out := []Series{
		{
			"id": IDFull, "state": "active", "canonical_url": "https://mangabaka.example/101",
			"title": "The Clockwork Garden", "native_title": "時計仕掛けの庭", "romanized_title": "Tokei Jikake no Niwa",
			"original_language": "ja", "type": "manga", "status": "completed", "content_rating": "suggestive",
			"description": "A **made-up** story about a garden that keeps time.",
			"year":        2011, "published_start_date": "2011-04-05", "published_end_date": "2019-12-20",
			"published_start_date_is_estimated": 0, "published_end_date_is_estimated": 1,
			"is_licensed": 1, "has_anime": 1, "anime_start": "Vol 1, Chap 1", "anime_end": "Vol 4, Chap 30 (S1)",
			"rating": 81.5, "popularity_global_current": 42, "popularity_type_current": 7,
			"final_volume": "12", "total_chapters": "108", "last_updated_at": "2026-09-30T12:34:56.789Z",
			"authors": j([]string{"Aiko Fictional"}), "artists": j([]string{"Aiko Fictional", "Ben Madeup"}),
			"titles": j([]map[string]any{
				{"language": "en", "traits": []string{"official"}, "title": "The Clockwork Garden", "note": nil, "is_primary": true},
				{"language": "ja", "traits": []string{"native"}, "title": "時計仕掛けの庭", "note": nil, "is_primary": true},
				{"language": "ja-Latn", "traits": []string{}, "title": "Tokei Jikake no Niwa", "note": nil, "is_primary": false},
				{"language": "fr", "traits": []string{"official"}, "title": "Le Jardin Mécanique", "note": "French edition", "is_primary": true},
			}),
			"secondary_titles_unknown": j([]map[string]any{{"type": "unknown", "title": "Clockwork Garden", "note": nil}}),
			"secondary_titles_ja-ro":   j([]map[string]any{{"type": "romanized", "title": "Tokei-jikake no Niwa"}}),
			"secondary_titles_en":      j([]map[string]any{{"type": "alternative", "title": "TCG", "note": "abbreviation"}}),
			"genres":                   j([]string{"fantasy", "slice_of_life", "sci-fi"}),
			"tags":                     j([]string{"Gardens"}),
			"tags_v2": j([]map[string]any{
				{"id": 1, "name": "Fantasy", "is_genre": true, "is_spoiler": false, "is_explicit": false, "implied_by_tag_ids": []int{}, "parent_id": nil, "level": 0, "name_path": "Fantasy", "series_count": 900, "description": nil, "content_rating": "safe", "weight": "core"},
				{"id": 2, "name": "Slice of Life", "is_genre": true, "is_spoiler": false, "is_explicit": false, "implied_by_tag_ids": []int{}, "parent_id": nil, "level": 0, "name_path": "Slice of Life", "series_count": 800, "description": nil, "content_rating": "safe", "weight": "defining"},
				{"id": 10, "name": "Setting", "is_genre": false, "is_spoiler": false, "is_explicit": false, "implied_by_tag_ids": []int{}, "parent_id": nil, "level": 0, "name_path": "Setting", "series_count": 5000, "description": "Where it happens.", "content_rating": "safe", "weight": "unweighted"},
				{"id": 11, "name": "Gardens", "is_genre": false, "is_spoiler": false, "is_explicit": false, "implied_by_tag_ids": []int{}, "parent_id": 10, "level": 1, "name_path": "Setting > Gardens", "series_count": 40, "description": nil, "content_rating": "safe", "weight": "recurrent"},
				{"id": 12, "name": "Clock Towers", "is_genre": false, "is_spoiler": false, "is_explicit": false, "implied_by_tag_ids": []int{11}, "parent_id": 999, "level": 2, "name_path": "Setting > Buildings > Clock Towers", "series_count": 12, "description": nil, "content_rating": "safe", "weight": "incidental"},
				{"id": 20, "name": "Time Loop Twist", "is_genre": false, "is_spoiler": true, "is_explicit": false, "implied_by_tag_ids": []int{}, "parent_id": nil, "level": 0, "name_path": "Time Loop Twist", "series_count": 30, "description": nil, "content_rating": "safe", "weight": "core"},
				{"id": 30, "name": "Partial Nudity", "is_genre": false, "is_spoiler": false, "is_explicit": true, "implied_by_tag_ids": []int{}, "parent_id": nil, "level": 0, "name_path": "Partial Nudity", "series_count": 300, "description": nil, "content_rating": "suggestive", "weight": "incidental"},
			}),
			"links": j([]string{"https://publisher.example/clockwork", PiracyURL}),
			"links_v2": j([]map[string]any{
				{"id": "00000000-0000-7000-8000-000000000001", "url": "https://publisher.example/clockwork", "name": "publisher.example", "name_display": "Example Publisher", "type": "publisher", "language": "ja"},
				{"id": "00000000-0000-7000-8000-000000000002", "url": "https://store.example/clockwork-1", "name": "store.example", "name_display": "Example Store", "type": "retailer", "language": "en"},
				{"id": "00000000-0000-7000-8000-000000000003", "url": PiracyURL, "name": "unlicensed-copies.example", "name_display": "Unlicensed", "type": "piracy", "language": "en"},
				{"id": "00000000-0000-7000-8000-000000000004", "url": "https://social.example/@clockwork", "name": "social.example", "name_display": "Social", "type": "social", "language": "unknown"},
			}),
			"publishers": j([]map[string]any{
				{"id": 7001, "name": "Example Shoten", "canonical_url": "https://mangabaka.example/publisher/7001", "language": "ja", "note": nil, "type": "Original"},
				{"id": 7002, "name": "Example Press", "canonical_url": nil, "language": "en", "note": "digital only", "type": "English"},
				{"id": nil, "name": nil, "language": "en", "note": nil, "type": "Other"},
			}),
			"relationships_v2": j([]map[string]any{
				{"id": "00000000-0000-7000-8000-0000000000a1", "to_series_id": IDSequel, "relation_type": "sequel", "is_manual": false, "note": nil, "chronology": "unknown", "published_start_date": nil},
				{"id": "00000000-0000-7000-8000-0000000000a2", "to_series_id": 5555, "relation_type": "brand_new_kind", "is_manual": true, "note": "made up", "chronology": "narrative", "published_start_date": nil},
			}),
			"relationships_sequel": j([]int{IDSequel}),
			"cover_raw_url":        "https://images.example/101/raw.jpg", "cover_raw_size": 123456, "cover_raw_width": 600, "cover_raw_height": 900,
			"cover_raw_format": "jpeg", "cover_raw_blurhash": "LEHV6nWB2yk8pyo0adR*.7kCMdnj", "cover_raw_thumbhash": "1QcSHQRnh493V4dIh4eXh1h4kJUI",
			"cover_x150_x1": "https://images.example/101/150.jpg", "cover_x150_x2": "https://images.example/101/150@2.jpg",
			"cover_x250_x1": "https://images.example/101/250.jpg", "cover_x350_x3": "https://images.example/101/350@3.jpg",
			"source_anilist_id": 9001, "source_anilist_rating": 7.9, "source_anilist_rating_normalized": 79,
			"source_my_anime_list_id": 9002, "source_manga_updates_id": "abc12xy", "source_anime_planet_id": "the-clockwork-garden",
			"source_kitsu_id": nil, "source_shikimori_id": 9002,
			"source_manga_updates_response": j(map[string]any{"huge": "upstream payload"}),
		},
		{
			"id": IDSequel, "state": "active", "title": "The Clockwork Garden: Winter", "type": "manga", "status": "releasing",
			"content_rating": "safe", "original_language": "ja", "has_anime": 1, "total_chapters": "20", "year": 2020,
			"relationships_prequel": j([]int{IDFull}), "relationships_side_story": j([]int{IDFull, 6666}),
			"relationships_v2": "[]", "source_anilist_id": "9003",
		},
		{
			"id": IDMergedText, "state": "merged", "merged_with": "101", "title": "Clockwork Garden (duplicate)",
			"type": "manga", "status": "completed", "content_rating": "safe",
			// Only this merged record carries the Kitsu ID: a lookup by it
			// must follow the merge to IDFull.
			"source_kitsu_id": KitsuOnMerged,
		},
		{"id": IDDeleted, "state": "deleted", "title": "Removed Entry", "type": "manga", "status": "unknown"},
		{"id": IDNoTitle, "state": "active", "titles": "[]", "type": "manga"},
		{"id": IDSparse, "title": "Sparse Series", "type": "something_new", "status": "", "content_rating": "erotica",
			"authors": "null", "artists": nil, "tags_v2": "null", "genres_v2": nil, "relationships_v2": "[]"},
		{
			"id": IDManhwa, "state": "active", "title": "Neon Lanterns", "native_title": "네온 등불", "type": "manhwa",
			"status": "hiatus", "content_rating": "safe", "original_language": "ko",
			"published_start_date": "2015-03-01T00:00:00.000Z", "published_start_date_is_estimated": 1,
			"titles":                 j([]map[string]any{{"language": "ko", "traits": []string{"native", "alternative"}, "title": "네온 등불", "note": nil, "is_primary": true}}),
			"secondary_titles_ko-ro": j([]map[string]any{{"type": "romanized", "title": "Ne-on Deungbul", "note": nil}}),
			"secondary_titles_ko":    j([]map[string]any{{"type": "native", "title": "네온 등불"}}),
			"authors":                j([]string{"Min Example"}), "is_licensed": 0,
		},
		{
			"id": IDManhua, "state": "active", "title": "River of Ink", "native_title": "墨之河", "type": "manhua",
			"status": "releasing", "content_rating": "safe", "original_language": "zh-Hans-CN", "romanized_title": "Mo Zhi He",
			"secondary_titles_es-la":   j([]map[string]any{{"type": "official", "title": "Río de Tinta"}}),
			"secondary_titles_zh-Hant": j([]map[string]any{{"type": "native", "title": "墨之河"}}),
		},
		{"id": IDMergedInt, "state": "merged", "merged_with": int64(IDSequel), "title": "Winter Garden", "type": "manga"},
		{
			"id": IDNovelV1Tags, "state": "active", "title": "Paper Wings", "type": "novel", "status": "completed",
			"tags": j([]string{"Flight", "Paper"}), "genres": j([]string{"adventure"}), "links": j([]string{"https://unknown-site.example/x"}),
		},
	}
	types := []string{"manga", "manhwa", "manhua", "novel", "oel", "other"}
	statuses := []string{"releasing", "completed", "hiatus", "cancelled", "upcoming", "unknown"}
	ratings := []string{"safe", "suggestive", "erotica", "pornographic"}
	for i := range GeneratedCount {
		id := GeneratedFirst + i
		out = append(out, Series{
			"id": id, "state": "active", "title": fmt.Sprintf("Synthetic Series %d", id),
			"type": types[i%len(types)], "status": statuses[i%len(statuses)], "content_rating": ratings[i%len(ratings)],
			"original_language": []string{"ja", "ko", "zh", "en"}[i%4], "year": 1990 + i,
			"rating": float64(50 + i), "popularity_global_current": 100 + i,
			"genres":  j([]string{[]string{"action", "romance", "comedy"}[i%3]}),
			"authors": j([]string{fmt.Sprintf("Author %d", i%5)}),
		})
	}
	return out
}

// Build writes the fixture database to path.
func Build(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	defs := make([]string, len(columns))
	for i, c := range columns {
		if c.name == "id" {
			defs[i] = `"id" INTEGER PRIMARY KEY`
			continue
		}
		defs[i] = fmt.Sprintf("%q %s", c.name, c.typ)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE \"series\" (\n   "+strings.Join(defs, ",\n   ")+"\n)"); err != nil {
		return fmt.Errorf("fixture: create: %w", err)
	}
	known := map[string]bool{}
	for _, c := range columns {
		known[c.name] = true
	}
	for _, s := range All() {
		names := make([]string, 0, len(s))
		marks := make([]string, 0, len(s))
		vals := make([]any, 0, len(s))
		for k, v := range s {
			if !known[k] {
				return fmt.Errorf("fixture: unknown column %q", k)
			}
			names = append(names, fmt.Sprintf("%q", k))
			marks = append(marks, "?")
			vals = append(vals, v)
		}
		q := fmt.Sprintf("INSERT INTO series (%s) VALUES (%s)", strings.Join(names, ", "), strings.Join(marks, ", ")) //nolint:gosec // column names are from the fixed list above
		if _, err := db.ExecContext(ctx, q, vals...); err != nil {
			return fmt.Errorf("fixture: insert %v: %w", s["id"], err)
		}
	}
	return nil
}

// Compress zstd-compresses src into dst and returns dst's SHA-1.
func Compress(src, dst string) (string, error) {
	in, err := os.Open(src) //nolint:gosec // test paths
	if err != nil {
		return "", err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst) //nolint:gosec // test paths
	if err != nil {
		return "", err
	}
	defer func() { _ = out.Close() }()
	h := sha1.New() //nolint:gosec // see import
	enc, err := zstd.NewWriter(io.MultiWriter(out, h))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(enc, in); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), out.Close()
}
