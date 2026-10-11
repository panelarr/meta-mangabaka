// SPDX-License-Identifier: GPL-3.0-or-later

package fixture

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Server serves the fixture dump the way MangaBaka publishes dumps: the
// zstd file (with Range support) and its .sha1 next to it.
type Server struct {
	*httptest.Server

	// SHA1 is the checksum of the served file.
	SHA1 string
	// Size of the compressed file.
	Size int64

	mu sync.Mutex
	// Data is the compressed file.
	data []byte
	// PublishedSHA1, if set, is published instead of the real checksum.
	publishedSHA1 string
	// cutAfter, if > 0, makes full downloads stop after this many bytes.
	cutAfter int
	// requests records "METHOD path range" for each request.
	requests []string
	// userAgents seen.
	userAgents []string
}

// BaseURL is the dump directory URL to configure the plugin with.
func (s *Server) BaseURL() string { return s.URL + "/v1/database" }

// NewServer builds the fixture, compresses it and serves it.
func NewServer(t testing.TB) *Server {
	t.Helper()
	dir := t.TempDir()
	db := filepath.Join(dir, "series.sqlite")
	if err := Build(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	zst := filepath.Join(dir, "series.sqlite.zst")
	sum, err := Compress(db, zst)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(zst) //nolint:gosec // test path
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{SHA1: sum, Size: int64(len(data)), data: data}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// PublishWrongChecksum makes the server publish a checksum that doesn't
// match the file.
func (s *Server) PublishWrongChecksum() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishedSHA1 = strings.Repeat("0", 40)
}

// CutDownloadsAfter makes the next whole-file downloads end early after n
// bytes, like a dropped connection. 0 turns it off.
func (s *Server) CutDownloadsAfter(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cutAfter = n
}

// Requests returns what was requested so far.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// UserAgents returns the User-Agent headers seen.
func (s *Server) UserAgents() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.userAgents...)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, r.Method+" "+r.URL.Path+" "+r.Header.Get("Range"))
	s.userAgents = append(s.userAgents, r.UserAgent())
	published := s.SHA1
	if s.publishedSHA1 != "" {
		published = s.publishedSHA1
	}
	cut := s.cutAfter
	data := s.data
	s.mu.Unlock()

	switch r.URL.Path {
	case "/v1/database/series.sqlite.zst.sha1":
		_, _ = w.Write([]byte(published + "  series.sqlite.zst\n"))
	case "/v1/database/series.sqlite.zst":
		if cut > 0 && r.Header.Get("Range") == "" {
			// Promise the whole file, send part of it, then drop the
			// connection.
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data[:cut])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
				}
			}
			return
		}
		http.ServeContent(w, r, "series.sqlite.zst", time.Unix(0, 0), bytes.NewReader(data))
	default:
		http.NotFound(w, r)
	}
}
