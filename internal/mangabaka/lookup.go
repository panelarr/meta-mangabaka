// SPDX-License-Identifier: GPL-3.0-or-later

package mangabaka

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	metadatav1 "github.com/panelarr/meta/gen/panelarr/metadata/v1"
	"github.com/panelarr/meta/sdk/plugin"

	"github.com/panelarr/meta-mangabaka/internal/api"
	"github.com/panelarr/meta-mangabaka/internal/mapping"
)

const (
	defaultSearchLimit = 10
	maxSearchLimit     = 100
	// maxMergeHops bounds following merged_with when a lookup lands on a
	// merged series.
	maxMergeHops = 3
)

// apiTypes maps entry types to MangaBaka's type filter values.
var apiTypes = map[metadatav1.EntryType]string{
	metadatav1.EntryType_ENTRY_TYPE_MANGA:       "manga",
	metadatav1.EntryType_ENTRY_TYPE_MANHWA:      "manhwa",
	metadatav1.EntryType_ENTRY_TYPE_MANHUA:      "manhua",
	metadatav1.EntryType_ENTRY_TYPE_NOVEL:       "novel",
	metadatav1.EntryType_ENTRY_TYPE_LIGHT_NOVEL: "novel",
	metadatav1.EntryType_ENTRY_TYPE_WEB_NOVEL:   "novel",
	metadatav1.EntryType_ENTRY_TYPE_OEL:         "oel",
	metadatav1.EntryType_ENTRY_TYPE_OTHER:       "other",
}

var apiRatings = map[metadatav1.ContentRating]string{
	metadatav1.ContentRating_CONTENT_RATING_SAFE:         "safe",
	metadatav1.ContentRating_CONTENT_RATING_SUGGESTIVE:   "suggestive",
	metadatav1.ContentRating_CONTENT_RATING_EROTIC:       "erotica",
	metadatav1.ContentRating_CONTENT_RATING_PORNOGRAPHIC: "pornographic",
}

func filterValues[K comparable](in []K, m map[K]string) []string {
	var out []string
	for _, k := range in {
		if v, ok := m[k]; ok && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// lookupError turns API errors into plugin errors.
func lookupError(err error) error {
	var rl *api.RateLimitedError
	var se *api.StatusError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, api.ErrNotFound):
		return plugin.ErrNotFound("MangaBaka has no such series")
	case errors.As(err, &rl):
		return plugin.ErrRateLimited(rl.RetryAfter, "MangaBaka is rate-limiting requests")
	case errors.As(err, &se) && se.Status == 400:
		return plugin.ErrInvalidArgument("MangaBaka rejected the request: %s", se.Message)
	default:
		return plugin.Errorf(plugin.CodeUnavailable, "%v", err)
	}
}

// Search finds series through the API.
func (p *Plugin) Search(ctx context.Context, req *metadatav1.SearchRequest) (*metadatav1.SearchResponse, error) {
	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	limit = min(limit, maxSearchLimit)
	page := 1
	if tok := req.GetPageToken(); tok != "" {
		n, err := strconv.Atoi(tok)
		if err != nil || n < 1 {
			return nil, plugin.ErrInvalidArgument("bad page token %q", tok)
		}
		page = n
	}
	q := api.SearchQuery{
		Q:              strings.TrimSpace(req.GetQuery()),
		Page:           page,
		Limit:          limit,
		Types:          filterValues(req.GetTypes(), apiTypes),
		ContentRatings: filterValues(req.GetContentRatings(), apiRatings),
	}
	if q.Q == "" && len(q.Types) == 0 && len(q.ContentRatings) == 0 {
		return nil, plugin.ErrInvalidArgument("a query or a filter is required")
	}
	if len([]rune(q.Q)) > 500 {
		return nil, plugin.ErrInvalidArgument("the query is longer than 500 characters")
	}
	if page*limit > api.MaxWindow {
		// MangaBaka only pages through the first 12,000 results.
		return &metadatav1.SearchResponse{}, nil
	}
	res, err := p.apiClient().Search(ctx, q)
	if err != nil {
		return nil, lookupError(err)
	}
	out := &metadatav1.SearchResponse{}
	for _, obj := range res.Series {
		r, err := mapping.Series(mapping.FromAPI(obj))
		if err != nil {
			continue // no ID or no title: nothing to show
		}
		out.Entries = append(out.Entries, r.Entry)
	}
	if res.Pagination.Next != nil && (page+1)*limit <= api.MaxWindow {
		out.NextPageToken = strconv.Itoa(page + 1)
	}
	return out, nil
}

// Get fetches one series: by MangaBaka ID, or by an ID at a site MangaBaka
// cross-references.
func (p *Plugin) Get(ctx context.Context, req *metadatav1.GetRequest) (*metadatav1.GetResponse, error) {
	ref := req.GetRef()
	c := p.apiClient()
	var obj map[string]any
	var err error
	switch provider := ref.GetProvider(); {
	case provider == mapping.Provider:
		if _, perr := strconv.ParseUint(ref.GetId(), 10, 63); perr != nil {
			return nil, plugin.ErrNotFound("%q is not a MangaBaka series ID", ref.GetId())
		}
		obj, err = c.Series(ctx, ref.GetId())
	case api.Sites[provider] != "":
		obj, err = p.bySource(ctx, c, api.Sites[provider], ref.GetId())
	default:
		return nil, plugin.ErrNotFound("MangaBaka can't look series up by %q IDs", provider)
	}
	if err != nil {
		return nil, lookupError(err)
	}
	r, err := mapping.Series(mapping.FromAPI(obj))
	if err != nil {
		return nil, plugin.Errorf(plugin.CodeInternal, "%v", err)
	}
	return &metadatav1.GetResponse{Entry: r.Entry}, nil
}

// bySource resolves an external ID to MangaBaka's current series for it:
// the active one, or the series a merged one now points to.
func (p *Plugin) bySource(ctx context.Context, c *api.Client, site, id string) (map[string]any, error) {
	if strings.TrimSpace(id) == "" {
		return nil, api.ErrNotFound
	}
	list, err := c.BySource(ctx, site, id)
	if err != nil {
		return nil, err
	}
	for _, obj := range list {
		if fmt.Sprint(obj["state"]) == "active" {
			return obj, nil
		}
	}
	for _, obj := range list {
		target := fmt.Sprint(obj["merged_with"])
		for hop := 0; hop < maxMergeHops && fmt.Sprint(obj["state"]) == "merged" && target != "" && target != "<nil>"; hop++ {
			next, err := c.Series(ctx, target)
			if err != nil {
				return nil, err
			}
			obj, target = next, fmt.Sprint(next["merged_with"])
		}
		if fmt.Sprint(obj["state"]) == "active" {
			return obj, nil
		}
	}
	return nil, api.ErrNotFound
}
