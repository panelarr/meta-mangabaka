// SPDX-License-Identifier: GPL-3.0-or-later

package fixture

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/panelarr/meta-mangabaka/internal/dump"
)

// Rows builds the fixture database and reads it back with the dump reader,
// so values have exactly the types a real dump gives.
func Rows(t testing.TB) []dump.Row {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "series.sqlite")
	if err := Build(ctx, path); err != nil {
		t.Fatal(err)
	}
	r, err := dump.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	var out []dump.Row
	if err := r.Each(ctx, func(row dump.Row) error {
		out = append(out, row)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

var sourceSites = []string{"anime_news_network", "anime_planet", "manga_updates", "my_anime_list", "anilist", "shikimori", "kitsu"}

var v1Buckets = []string{"main_story", "adaptation", "prequel", "sequel", "side_story", "spin_off", "alternative", "other"}

// nested maps flattened column prefixes back to object paths, longest first.
var nested = []struct {
	prefix string
	path   []string
}{
	{"popularity_global_history_", []string{"popularity", "global", "history"}},
	{"popularity_type_history_", []string{"popularity", "type", "history"}},
	{"popularity_global_", []string{"popularity", "global"}},
	{"popularity_type_", []string{"popularity", "type"}},
	{"cover_raw_", []string{"cover", "raw"}},
	{"cover_x150_", []string{"cover", "x150"}},
	{"cover_x250_", []string{"cover", "x250"}},
	{"cover_x350_", []string{"cover", "x350"}},
	{"published_", []string{"published"}},
	{"secondary_titles_", []string{"secondary_titles"}},
	{"anime_", []string{"anime"}},
}

var jsonColumns = []string{
	"titles", "tags_v2", "links_v2", "publishers", "relationships_v2", "genres", "genres_v2",
	"tags", "authors", "artists", "links",
}

var boolColumns = []string{"has_anime", "is_licensed", "start_date_is_estimated", "end_date_is_estimated"}

// APIObject turns a dump row back into the nested series object the API
// returns, the inverse of how the dump is built. Raw upstream responses
// (source_*_response) are not part of the API's default series object.
func APIObject(row map[string]any) map[string]any {
	obj := map[string]any{}
	set := func(path []string, key string, v any) {
		m := obj
		for _, p := range path {
			next, ok := m[p].(map[string]any)
			if !ok {
				next = map[string]any{}
				m[p] = next
			}
			m = next
		}
		m[key] = v
	}
	value := func(key string, v any) any {
		s, isStr := v.(string)
		if isStr && (slices.Contains(jsonColumns, key) || strings.HasPrefix(key, "secondary_titles_") || isV1Bucket(key)) {
			var parsed any
			dec := json.NewDecoder(strings.NewReader(s))
			dec.UseNumber()
			if dec.Decode(&parsed) == nil {
				return parsed
			}
		}
		for _, b := range boolColumns {
			if key == b || strings.HasSuffix(key, "_"+b) {
				if n, ok := v.(int64); ok {
					return n != 0
				}
			}
		}
		return v
	}
outer:
	for key, v := range row {
		if strings.HasPrefix(key, "source_") && strings.HasSuffix(key, "_response") {
			continue
		}
		for _, site := range sourceSites {
			if rest, ok := strings.CutPrefix(key, "source_"+site+"_"); ok {
				set([]string{"source", site}, rest, value(key, v))
				continue outer
			}
		}
		if isV1Bucket(key) {
			set([]string{"relationships"}, strings.TrimPrefix(key, "relationships_"), value(key, v))
			continue
		}
		for _, n := range nested {
			if rest, ok := strings.CutPrefix(key, n.prefix); ok {
				set(n.path, rest, value(key, v))
				continue outer
			}
		}
		obj[key] = value(key, v)
	}
	return obj
}

func isV1Bucket(key string) bool {
	b, ok := strings.CutPrefix(key, "relationships_")
	return ok && slices.Contains(v1Buckets, b)
}

// APIServer is a fake MangaBaka API over the fixture's series. It serves
// GET /v1/series/{id}, /v1/series/search and /v1/source/{site}/{id}, and
// can answer 429 or 500 on demand.
type APIServer struct {
	*httptest.Server

	mu      sync.Mutex
	objects map[string]map[string]any
	order   []string
	// failures queued for the next requests: "429:<Retry-After>" or "500".
	failures []string
	requests []APIRequest
}

// APIRequest is a request the fake API saw.
type APIRequest struct {
	Path, Query, UserAgent string
}

// NewAPIServer serves the fixture's series.
func NewAPIServer(t testing.TB) *APIServer {
	t.Helper()
	s := &APIServer{objects: map[string]map[string]any{}}
	for _, row := range Rows(t) {
		obj := APIObject(row)
		id := fmt.Sprint(obj["id"])
		s.objects[id] = obj
		s.order = append(s.order, id)
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Fail429 makes the next request answer 429, with a Retry-After header
// when retryAfter is not empty.
func (s *APIServer) Fail429(retryAfter string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, "429:"+retryAfter)
}

// Fail500 makes the next request answer 500.
func (s *APIServer) Fail500() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, "500")
}

// Requests returns the requests seen so far.
func (s *APIServer) Requests() []APIRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]any{"status": 404, "message": "NOT_FOUND"})
}

func (s *APIServer) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, APIRequest{Path: r.URL.Path, Query: r.URL.RawQuery, UserAgent: r.UserAgent()})
	var fail string
	if len(s.failures) > 0 {
		fail, s.failures = s.failures[0], s.failures[1:]
	}
	s.mu.Unlock()

	switch {
	case fail == "500":
		writeJSON(w, http.StatusInternalServerError, map[string]any{"status": 500, "message": "boom"})
		return
	case strings.HasPrefix(fail, "429:"):
		if ra := strings.TrimPrefix(fail, "429:"); ra != "" {
			w.Header().Set("Retry-After", ra)
		}
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"status": 429, "message": "Too Many Requests"})
		return
	}

	path := r.URL.Path
	switch {
	case path == "/v1/series/search":
		s.search(w, r)
	case strings.HasPrefix(path, "/v1/series/"):
		id := strings.TrimPrefix(path, "/v1/series/")
		s.mu.Lock()
		obj, ok := s.objects[id]
		s.mu.Unlock()
		if !ok || obj["state"] == "deleted" {
			notFound(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": 200, "data": obj})
	case strings.HasPrefix(path, "/v1/source/"):
		parts := strings.Split(strings.TrimPrefix(path, "/v1/source/"), "/")
		if len(parts) != 2 {
			notFound(w)
			return
		}
		site, id := strings.ReplaceAll(parts[0], "-", "_"), parts[1]
		withMerged := r.URL.Query().Get("with_merged_series") == "true"
		var found []map[string]any
		s.mu.Lock()
		for _, sid := range s.order {
			obj := s.objects[sid]
			src, _ := obj["source"].(map[string]any)
			entry, _ := src[site].(map[string]any)
			if entry == nil || fmt.Sprint(entry["id"]) != id {
				continue
			}
			switch obj["state"] {
			case "deleted":
				continue
			case "merged":
				if !withMerged {
					continue
				}
			}
			found = append(found, obj)
		}
		s.mu.Unlock()
		if len(found) == 0 {
			notFound(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": 200, "data": map[string]any{"series": found}})
	default:
		notFound(w)
	}
}

func (s *APIServer) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	text := strings.ToLower(q.Get("q"))
	types, ratings := q["type"], q["content_rating"]
	if text == "" && len(types) == 0 && len(ratings) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": 400, "message": "at least one parameter is required"})
		return
	}
	page, _ := strconv.Atoi(q.Get("page"))
	if page <= 0 {
		page = 1
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 10
	}
	var hits []map[string]any
	s.mu.Lock()
	for _, id := range s.order {
		obj := s.objects[id]
		if obj["state"] != "active" {
			continue // the API never returns merged or deleted series in search
		}
		if len(types) > 0 && !slices.Contains(types, fmt.Sprint(obj["type"])) {
			continue
		}
		if len(ratings) > 0 && !slices.Contains(ratings, fmt.Sprint(obj["content_rating"])) {
			continue
		}
		if text != "" {
			hay := strings.ToLower(fmt.Sprint(obj["title"], " ", obj["native_title"], " ", obj["romanized_title"]))
			if !strings.Contains(hay, text) {
				continue
			}
		}
		hits = append(hits, obj)
	}
	s.mu.Unlock()
	start := (page - 1) * limit
	end := min(start+limit, len(hits))
	if start > len(hits) {
		start = end
	}
	var next any
	if end < len(hits) {
		next = fmt.Sprintf("%s/v1/series/search?page=%d", s.URL, page+1)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": 200,
		"data":   hits[start:end],
		"pagination": map[string]any{
			"count": len(hits), "page": page, "limit": limit, "next": next, "previous": nil,
		},
	})
}
