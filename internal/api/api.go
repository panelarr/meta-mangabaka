// SPDX-License-Identifier: GPL-3.0-or-later

// Package api is a small client for MangaBaka's REST API. It keeps to the
// published rate limits on its own (one bucket for searches, one for other
// requests), identifies itself with a User-Agent, and backs off when the API
// answers 429 Too Many Requests.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/panelarr/meta/sdk/httpx"
)

// DefaultBaseURL is MangaBaka's API.
const DefaultBaseURL = "https://api.mangabaka.org"

// Published limits, per IP: searches 30 a minute, other requests 180 a
// minute.
const (
	SearchPerMinute = 30
	GetPerMinute    = 180
)

// Kind selects the rate-limit bucket of a request.
type Kind int

const (
	KindGet Kind = iota
	KindSearch
)

// ErrNotFound means the API has no such record (HTTP 404).
var ErrNotFound = errors.New("mangabaka: not found")

// RateLimitedError means the API kept answering 429 after the client's
// retries. RetryAfter is a hint for when to try again.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("mangabaka: rate limited by the API (retry after %s)", e.RetryAfter)
}

// StatusError is any other unsuccessful answer.
type StatusError struct {
	Status  int
	Message string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("mangabaka: HTTP %d: %s", e.Status, e.Message)
}

// Client calls the API. The zero value is not usable; use New.
type Client struct {
	BaseURL   string
	UserAgent string
	HTTP      *http.Client
	// Timeout bounds each request. Zero means no extra timeout.
	Timeout time.Duration

	// MaxRetries is how often a 429 or 5xx answer is retried.
	MaxRetries int
	// BaseDelay and MaxDelay shape the exponential back-off used when the
	// API gives no Retry-After.
	BaseDelay, MaxDelay time.Duration
	// MaxRetryAfter caps how long the client waits on a Retry-After before
	// giving up and reporting the rate limit instead.
	MaxRetryAfter time.Duration

	// Now and Sleep are replaced in tests.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error

	mu      sync.Mutex
	buckets map[Kind]*rate.Limiter
}

// New returns a client with the published limits. searchPerMinute and
// getPerMinute may lower them; zero or more than published means published.
func New(baseURL, userAgent string, hc *http.Client, searchPerMinute, getPerMinute int) *Client {
	c := &Client{
		BaseURL:       strings.TrimRight(baseURL, "/"),
		UserAgent:     userAgent,
		HTTP:          hc,
		Timeout:       30 * time.Second,
		MaxRetries:    4,
		BaseDelay:     time.Second,
		MaxDelay:      30 * time.Second,
		MaxRetryAfter: 2 * time.Minute,
		Now:           time.Now,
		Sleep:         sleep,
	}
	c.SetLimit(KindSearch, perMinute(searchPerMinute, SearchPerMinute), 1)
	c.SetLimit(KindGet, perMinute(getPerMinute, GetPerMinute), 1)
	return c
}

func perMinute(n, max int) rate.Limit {
	if n <= 0 || n > max {
		n = max
	}
	return rate.Every(time.Minute / time.Duration(n))
}

// SetLimit replaces the bucket for a kind of request. The default burst of
// 1 spreads requests evenly, which keeps within a leaky-bucket limit.
func (c *Client) SetLimit(k Kind, r rate.Limit, burst int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.buckets == nil {
		c.buckets = map[Kind]*rate.Limiter{}
	}
	c.buckets[k] = rate.NewLimiter(r, burst)
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// wait takes a token from the kind's bucket, sleeping until one is free.
func (c *Client) wait(ctx context.Context, k Kind) error {
	c.mu.Lock()
	lim := c.buckets[k]
	c.mu.Unlock()
	if lim == nil {
		return nil
	}
	now := c.Now()
	res := lim.ReserveN(now, 1)
	if !res.OK() {
		return errors.New("mangabaka: rate limiter misconfigured")
	}
	if d := res.DelayFrom(now); d > 0 {
		if err := c.Sleep(ctx, d); err != nil {
			res.CancelAt(c.Now())
			return err
		}
	}
	return nil
}

// backoff returns the delay before retry number attempt (0-based): the base
// delay doubled per attempt, capped, with up to 25% jitter.
func (c *Client) backoff(attempt int) time.Duration {
	d := c.BaseDelay << attempt
	if d <= 0 || d > c.MaxDelay {
		d = c.MaxDelay
	}
	return d + time.Duration(rand.Float64()*0.25*float64(d)) //nolint:gosec // jitter, not security
}

// retryAfter parses a Retry-After header: seconds or an HTTP date.
func (c *Client) retryAfter(h string) (time.Duration, bool) {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0, false
	}
	if s, err := strconv.Atoi(h); err == nil && s >= 0 {
		return time.Duration(s) * time.Second, true
	}
	if t, err := http.ParseTime(h); err == nil {
		d := t.Sub(c.Now())
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}

// envelope is the API's answer: {status, data, pagination?} or
// {status, message}.
type envelope struct {
	Status     int             `json:"status"`
	Message    string          `json:"message"`
	Data       json.RawMessage `json:"data"`
	Pagination *Pagination     `json:"pagination"`
}

// Pagination is the API's paging block.
type Pagination struct {
	Count    int64   `json:"count"`
	Page     int     `json:"page"`
	Limit    int     `json:"limit"`
	Next     *string `json:"next"`
	Previous *string `json:"previous"`
}

// get performs one API call with rate limiting and retries, and returns
// the decoded envelope.
func (c *Client) get(ctx context.Context, k Kind, path string, q url.Values) (*envelope, error) {
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var lastDelay time.Duration
	for attempt := 0; ; attempt++ {
		if err := c.wait(ctx, k); err != nil {
			return nil, err
		}
		status, header, body, err := c.do(ctx, u)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// An anti-bot challenge won't go away by retrying.
			if httpx.IsChallenge(err) {
				return nil, err
			}
			// Network trouble: retry like a 5xx.
			if attempt >= c.MaxRetries {
				return nil, err
			}
			if err := c.Sleep(ctx, c.backoff(attempt)); err != nil {
				return nil, err
			}
			continue
		}
		switch {
		case status == http.StatusOK:
			var env envelope
			dec := json.NewDecoder(bytes.NewReader(body))
			dec.UseNumber()
			if err := dec.Decode(&env); err != nil {
				return nil, fmt.Errorf("mangabaka: bad response from %s: %w", path, err)
			}
			return &env, nil
		case status == http.StatusNotFound:
			return nil, ErrNotFound
		case status == http.StatusTooManyRequests:
			d, ok := c.retryAfter(header.Get("Retry-After"))
			if !ok {
				d = c.backoff(attempt)
			}
			lastDelay = d
			if attempt >= c.MaxRetries || d > c.MaxRetryAfter {
				return nil, &RateLimitedError{RetryAfter: lastDelay}
			}
			if err := c.Sleep(ctx, d); err != nil {
				return nil, err
			}
		case status >= 500:
			if attempt >= c.MaxRetries {
				return nil, &StatusError{Status: status, Message: message(body)}
			}
			if err := c.Sleep(ctx, c.backoff(attempt)); err != nil {
				return nil, err
			}
		default:
			return nil, &StatusError{Status: status, Message: message(body)}
		}
	}
}

func message(body []byte) string {
	var env envelope
	if json.Unmarshal(body, &env) == nil && env.Message != "" {
		return env.Message
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func (c *Client) do(ctx context.Context, u string) (int, http.Header, []byte, error) {
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return 0, nil, nil, err
	}
	return res.StatusCode, res.Header, body, nil
}

func decodeObject(raw json.RawMessage) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf("mangabaka: bad series object: %w", err)
	}
	return obj, nil
}

func decodeList(raw json.RawMessage) ([]map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var list []map[string]any
	if err := dec.Decode(&list); err != nil {
		return nil, fmt.Errorf("mangabaka: bad series list: %w", err)
	}
	return list, nil
}

// Series fetches one series by MangaBaka ID. Merged series come back as
// they are (state "merged", merged_with set).
func (c *Client) Series(ctx context.Context, id string) (map[string]any, error) {
	env, err := c.get(ctx, KindGet, "/v1/series/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	return decodeObject(env.Data)
}

// SearchQuery is a series search.
type SearchQuery struct {
	Q              string
	Page, Limit    int
	Types          []string
	ContentRatings []string
}

// MaxWindow is the API's paging window: page*limit may not exceed it.
const MaxWindow = 12000

// SearchPage is one page of search results.
type SearchPage struct {
	Series     []map[string]any
	Pagination Pagination
}

// Search runs a series search.
func (c *Client) Search(ctx context.Context, sq SearchQuery) (*SearchPage, error) {
	q := url.Values{}
	if sq.Q != "" {
		q.Set("q", sq.Q)
	}
	if sq.Page > 0 {
		q.Set("page", strconv.Itoa(sq.Page))
	}
	if sq.Limit > 0 {
		q.Set("limit", strconv.Itoa(sq.Limit))
	}
	for _, t := range sq.Types {
		q.Add("type", t)
	}
	for _, r := range sq.ContentRatings {
		q.Add("content_rating", r)
	}
	env, err := c.get(ctx, KindSearch, "/v1/series/search", q)
	if err != nil {
		return nil, err
	}
	list, err := decodeList(env.Data)
	if err != nil {
		return nil, err
	}
	p := &SearchPage{Series: list}
	if env.Pagination != nil {
		p.Pagination = *env.Pagination
	}
	return p, nil
}

// Sites are the external ID lookups the API offers, by meta provider key.
var Sites = map[string]string{
	"anilist":      "anilist",
	"kitsu":        "kitsu",
	"mal":          "my-anime-list",
	"animeplanet":  "anime-planet",
	"mangaupdates": "manga-updates",
}

// BySource returns the series that carry an external ID, merged ones
// included. site is an API site name (see Sites).
func (c *Client) BySource(ctx context.Context, site, id string) ([]map[string]any, error) {
	q := url.Values{"with_merged_series": {"true"}}
	env, err := c.get(ctx, KindGet, "/v1/source/"+url.PathEscape(site)+"/"+url.PathEscape(id), q)
	if err != nil {
		return nil, err
	}
	var data struct {
		Series json.RawMessage `json:"series"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil, fmt.Errorf("mangabaka: bad source lookup: %w", err)
	}
	if len(data.Series) == 0 || string(data.Series) == "null" {
		return nil, nil
	}
	return decodeList(data.Series)
}
