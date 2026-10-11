// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/panelarr/meta-mangabaka/internal/api"
	"github.com/panelarr/meta-mangabaka/internal/fixture"
)

// clock is a fake clock: Sleep advances it instead of waiting.
type clock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	return nil
}

func (c *clock) Sleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

// stamps records the fake time of every request.
type stamps struct {
	clock *clock
	mu    sync.Mutex
	at    []time.Time
}

func (s *stamps) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.at = append(s.at, s.clock.Now())
	s.mu.Unlock()
	return http.DefaultTransport.RoundTrip(r)
}

func newClient(t *testing.T, srv *fixture.APIServer, searchPM, getPM int) (*api.Client, *clock, *stamps) {
	t.Helper()
	clk := &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	st := &stamps{clock: clk}
	c := api.New(srv.URL, "meta-mangabaka/test (+https://github.com/panelarr/meta-mangabaka)", &http.Client{Transport: st}, searchPM, getPM)
	c.Now, c.Sleep = clk.Now, clk.Sleep
	return c, clk, st
}

func TestSeriesAndUserAgent(t *testing.T) {
	srv := fixture.NewAPIServer(t)
	c, _, _ := newClient(t, srv, 0, 0)
	obj, err := c.Series(context.Background(), "101")
	if err != nil {
		t.Fatal(err)
	}
	if obj["title"] != "The Clockwork Garden" {
		t.Fatalf("title %v", obj["title"])
	}
	if _, err := c.Series(context.Background(), "424242"); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("missing series: %v", err)
	}
	for _, r := range srv.Requests() {
		if !strings.HasPrefix(r.UserAgent, "meta-mangabaka/") {
			t.Errorf("user agent %q", r.UserAgent)
		}
	}
}

func TestSearchEncodesFiltersAndPages(t *testing.T) {
	srv := fixture.NewAPIServer(t)
	c, _, _ := newClient(t, srv, 0, 0)
	res, err := c.Search(context.Background(), api.SearchQuery{Q: "synthetic", Page: 1, Limit: 3, Types: []string{"manga", "manhwa"}, ContentRatings: []string{"safe"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Series) == 0 || res.Pagination.Limit != 3 {
		t.Fatalf("results %d, pagination %+v", len(res.Series), res.Pagination)
	}
	reqs := srv.Requests()
	q, err := url.ParseQuery(reqs[len(reqs)-1].Query)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(q["type"], ",") != "manga,manhwa" || q.Get("content_rating") != "safe" || q.Get("limit") != "3" || q.Get("q") != "synthetic" {
		t.Fatalf("query %v", q)
	}
}

func TestBySourceIncludesMerged(t *testing.T) {
	srv := fixture.NewAPIServer(t)
	c, _, _ := newClient(t, srv, 0, 0)
	list, err := c.BySource(context.Background(), "anilist", "9001")
	if err != nil || len(list) != 1 {
		t.Fatalf("%v %v", list, err)
	}
	reqs := srv.Requests()
	if last := reqs[len(reqs)-1]; last.Path != "/v1/source/anilist/9001" || !strings.Contains(last.Query, "with_merged_series=true") {
		t.Fatalf("request %+v", last)
	}
}

func TestRetryAfterIsHonoured(t *testing.T) {
	srv := fixture.NewAPIServer(t)
	c, clk, _ := newClient(t, srv, 0, 0)
	srv.Fail429("7")
	srv.Fail429(time.Date(2026, 1, 1, 0, 0, 20, 0, time.UTC).Format(http.TimeFormat))
	if _, err := c.Series(context.Background(), "101"); err != nil {
		t.Fatal(err)
	}
	var waited []time.Duration
	for _, d := range clk.Sleeps() {
		if d >= time.Second {
			waited = append(waited, d)
		}
	}
	// 7 s from the first answer; the second names a moment 20 s after the
	// start, which is 13 s later (plus the rate-limit spacing).
	if len(waited) != 2 || waited[0] != 7*time.Second || waited[1] < 12*time.Second || waited[1] > 13*time.Second {
		t.Fatalf("waits %v", clk.Sleeps())
	}
}

func TestBackoffWithoutRetryAfter(t *testing.T) {
	srv := fixture.NewAPIServer(t)
	c, clk, _ := newClient(t, srv, 0, 0)
	srv.Fail429("")
	srv.Fail429("")
	srv.Fail500()
	if _, err := c.Series(context.Background(), "101"); err != nil {
		t.Fatal(err)
	}
	var backoffs []time.Duration
	for _, d := range clk.Sleeps() {
		if d >= time.Second {
			backoffs = append(backoffs, d)
		}
	}
	if len(backoffs) != 3 {
		t.Fatalf("backoffs %v", clk.Sleeps())
	}
	for i, d := range backoffs {
		base := time.Second << i
		if d < base || d > base+base/4 {
			t.Errorf("backoff %d = %s, want %s..%s", i, d, base, base+base/4)
		}
	}
}

func TestGivesUpAfterRetries(t *testing.T) {
	srv := fixture.NewAPIServer(t)
	c, _, _ := newClient(t, srv, 0, 0)
	c.MaxRetries = 2
	for range 3 {
		srv.Fail429("9")
	}
	_, err := c.Series(context.Background(), "101")
	var rl *api.RateLimitedError
	if !errors.As(err, &rl) || rl.RetryAfter != 9*time.Second {
		t.Fatalf("got %v", err)
	}
	// A Retry-After beyond the cap is reported at once.
	srv.Fail429("3600")
	_, err = c.Series(context.Background(), "101")
	if !errors.As(err, &rl) || rl.RetryAfter != time.Hour {
		t.Fatalf("got %v", err)
	}
	if n := len(srv.Requests()); n != 4 {
		t.Fatalf("%d requests", n)
	}
}

func TestLimiterSpacesRequests(t *testing.T) {
	srv := fixture.NewAPIServer(t)
	// 2 lookups and 1 search a minute.
	c, _, st := newClient(t, srv, 1, 2)
	ctx := context.Background()
	for range 5 {
		if _, err := c.Series(ctx, "101"); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := c.Search(ctx, api.SearchQuery{Q: "garden"}); err != nil {
			t.Fatal(err)
		}
	}
	at := st.at
	gets, searches := at[:5], at[5:]
	for i := 1; i < len(gets); i++ {
		if d := gets[i].Sub(gets[i-1]); d < 30*time.Second {
			t.Errorf("lookups %d and %d only %s apart", i-1, i, d)
		}
	}
	if d := searches[1].Sub(searches[0]); d < time.Minute {
		t.Errorf("searches only %s apart", d)
	}
	// The two buckets are separate: the first search didn't wait for the
	// lookup bucket.
	if !searches[0].Equal(gets[4]) {
		t.Errorf("first search waited: %s after the last lookup", searches[0].Sub(gets[4]))
	}
}

func TestCancelledWhileWaiting(t *testing.T) {
	srv := fixture.NewAPIServer(t)
	c, _, _ := newClient(t, srv, 0, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv.Fail429("5")
	if _, err := c.Series(ctx, "101"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestBadRequest(t *testing.T) {
	srv := fixture.NewAPIServer(t)
	c, _, _ := newClient(t, srv, 0, 0)
	_, err := c.Search(context.Background(), api.SearchQuery{})
	var se *api.StatusError
	if !errors.As(err, &se) || se.Status != 400 || !strings.Contains(se.Message, "required") {
		t.Fatalf("got %v", err)
	}
}
