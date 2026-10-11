// SPDX-License-Identifier: GPL-3.0-or-later

// Package mangabaka is the MangaBaka metadata plugin.
package mangabaka

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"

	metadatav1 "github.com/panelarr/meta/gen/panelarr/metadata/v1"
	"github.com/panelarr/meta/sdk/plugin"
	"github.com/panelarr/meta/sdk/settings"

	"github.com/panelarr/meta-mangabaka/internal/api"
	"github.com/panelarr/meta-mangabaka/internal/buildinfo"
	"github.com/panelarr/meta-mangabaka/internal/dump"
	"github.com/panelarr/meta-mangabaka/internal/mapping"
)

// UserAgent identifies the plugin to MangaBaka.
func UserAgent() string {
	return "meta-mangabaka/" + buildinfo.Version + " (+https://github.com/panelarr/meta-mangabaka)"
}

// Plugin is the MangaBaka metadata plugin.
type Plugin struct {
	plugin.Unimplemented

	schema *settings.Schema
	// HTTP is used for downloads; tests replace it.
	HTTP *http.Client

	// APIHook, if set, adjusts each new API client (tests use it to fake
	// time and loosen limits).
	APIHook func(*api.Client)

	mu       sync.Mutex
	settings Settings
	values   map[string]any
	api      *api.Client

	syncing sync.Mutex
}

// New returns the plugin with default settings.
func New() *Plugin {
	p := &Plugin{schema: newSchema(), HTTP: defaultHTTPClient()}
	s, values, err := parseSettings(p.schema, nil)
	if err != nil {
		panic(err)
	}
	p.settings, p.values = s, values
	return p
}

func defaultHTTPClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	tr.ResponseHeaderTimeout = 60 * time.Second
	// No overall timeout: a dump download takes minutes. Cancellation comes
	// from the sync's context.
	return &http.Client{Transport: tr}
}

// Settings returns the settings in effect.
func (p *Plugin) Settings() Settings {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.settings
}

// Configure applies settings given as a map (as SetSettings does).
func (p *Plugin) Configure(values map[string]any) error {
	s, full, err := parseSettings(p.schema, values)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// Keep the API client (and its rate buckets) unless what it depends on
	// changed: re-applying the same settings must not reset the limits.
	old := p.settings
	if p.api == nil || old.APIURL != s.APIURL || old.APISearchPerMinute != s.APISearchPerMinute ||
		old.APIGetPerMinute != s.APIGetPerMinute || old.APITimeoutSeconds != s.APITimeoutSeconds {
		p.api = nil
	}
	p.settings, p.values = s, full
	return nil
}

// apiClient returns the API client for the current settings.
func (p *Plugin) apiClient() *api.Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.api == nil {
		s := p.settings
		c := api.New(s.APIURL, UserAgent(), p.HTTP, s.APISearchPerMinute, s.APIGetPerMinute)
		c.Timeout = time.Duration(s.APITimeoutSeconds) * time.Second
		if p.APIHook != nil {
			p.APIHook(c)
		}
		p.api = c
	}
	return p.api
}

func (p *Plugin) GetInfo(context.Context, *metadatav1.GetInfoRequest) (*metadatav1.GetInfoResponse, error) {
	return &metadatav1.GetInfoResponse{Info: &metadatav1.PluginInfo{
		Id:          mapping.Provider,
		Name:        "MangaBaka",
		Version:     buildinfo.Version,
		Description: "Manga, manhwa and manhua metadata from MangaBaka: the nightly database dump for the whole catalogue, and the API for searches and fresh single records. Cross-references many other sites.",
		Homepage:    "https://github.com/panelarr/meta-mangabaka",
		Provider:    mapping.Provider,
		Protocols:   []string{plugin.Protocol},
		Ingestions: []*metadatav1.Ingestion{{
			Kind:        plugin.KindDump,
			Description: "Downloads the nightly SQLite dump (built at 00:00 UTC), verifies its SHA-1, and streams every series.",
			// Dumps are built nightly; one sync at a time and at most a few
			// per hour is plenty and keeps the load on MangaBaka low.
			Limits: &metadatav1.Limits{
				MaxConcurrency: 1,
				RateLimits: []*metadatav1.RateLimit{
					{Operation: "sync", Requests: 4, Per: durationpb.New(time.Hour)},
				},
			},
			ScheduleHint: "0 3 * * *",
			FullSnapshot: true,
		}, {
			Kind:        plugin.KindLookup,
			Description: "Searches and fetches single series through MangaBaka's API, also by AniList, MyAnimeList, MangaUpdates, Kitsu and Anime-Planet IDs.",
			// MangaBaka's published per-IP limits. The plugin keeps to them
			// itself as well.
			Limits: &metadatav1.Limits{
				MaxConcurrency: 4,
				RateLimits: []*metadatav1.RateLimit{
					{Operation: "search", Requests: api.SearchPerMinute, Per: durationpb.New(time.Minute)},
					{Operation: "get", Requests: api.GetPerMinute, Per: durationpb.New(time.Minute)},
				},
			},
		}},
		Capabilities: []string{
			"titles", "descriptions", "people", "companies", "tags", "covers",
			"relations", "adaptations", "links", "external_ids", "stats",
			"lookup_by_external_id",
		},
		CommonServices: []*metadatav1.CommonService{{
			Name:    "network_profile",
			Purpose: "downloads and API calls through the hoster's proxy",
		}},
		Attribution: mapping.Attribution,
	}}, nil
}

func (p *Plugin) GetSettingsSchema(context.Context, *metadatav1.GetSettingsSchemaRequest) (*metadatav1.GetSettingsSchemaResponse, error) {
	return &metadatav1.GetSettingsSchemaResponse{Schema: p.schema.Struct()}, nil
}

func (p *Plugin) GetSettings(context.Context, *metadatav1.GetSettingsRequest) (*metadatav1.GetSettingsResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, err := structpb.NewStruct(p.values)
	if err != nil {
		return nil, err
	}
	return &metadatav1.GetSettingsResponse{Settings: st}, nil
}

func (p *Plugin) SetSettings(_ context.Context, req *metadatav1.SetSettingsRequest) (*metadatav1.SetSettingsResponse, error) {
	if err := p.Configure(req.GetSettings().AsMap()); err != nil {
		return nil, plugin.ErrInvalidArgument("%v", err)
	}
	return p.settingsResponse()
}

func (p *Plugin) settingsResponse() (*metadatav1.SetSettingsResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, err := structpb.NewStruct(p.values)
	if err != nil {
		return nil, err
	}
	return &metadatav1.SetSettingsResponse{Settings: st}, nil
}

func (p *Plugin) Health(context.Context, *metadatav1.HealthRequest) (*metadatav1.HealthResponse, error) {
	dir := p.Settings().DownloadDir
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return &metadatav1.HealthResponse{
			Status:  metadatav1.HealthStatus_HEALTH_STATUS_DOWN,
			Message: fmt.Sprintf("download directory %s is not usable: %v", dir, err),
		}, nil
	}
	return &metadatav1.HealthResponse{Status: metadatav1.HealthStatus_HEALTH_STATUS_OK}, nil
}

func (p *Plugin) source(s Settings, w *plugin.SyncWriter) *dump.Source {
	src := &dump.Source{
		BaseURL:       s.DumpURL,
		Variant:       s.DumpVariant,
		Dir:           s.DownloadDir,
		HTTP:          p.HTTP,
		UserAgent:     UserAgent(),
		KeepDownloads: s.KeepDownloads,
	}
	if w != nil {
		src.Progress = throttledProgress(w)
	}
	return src
}

// throttledProgress forwards progress at most every second, plus the end of
// each phase.
func throttledProgress(w *plugin.SyncWriter) dump.ProgressFunc {
	var last time.Time
	var lastPhase string
	return func(phase string, done, total int64) {
		now := time.Now()
		finished := total > 0 && done >= total
		if phase == lastPhase && !finished && now.Sub(last) < time.Second {
			return
		}
		last, lastPhase = now, phase
		_ = w.Progress(phase, done, total, "bytes")
	}
}

// Sync runs a dump sync: fetch the dump (unless the cursor shows it is the
// one meta already has), then stream every series.
func (p *Plugin) Sync(ctx context.Context, req *metadatav1.SyncRequest, send plugin.SendFunc) error {
	if req.GetKind() != plugin.KindDump {
		return plugin.ErrInvalidArgument("sync kind %q is not supported; use %q", req.GetKind(), plugin.KindDump)
	}
	if !p.syncing.TryLock() {
		return plugin.Errorf(plugin.CodeFailedPrecondition, "a sync is already running")
	}
	defer p.syncing.Unlock()

	s := p.Settings()
	w := plugin.NewSyncWriter(send)
	src := p.source(s, w)

	sum, err := src.RemoteChecksum(ctx)
	if err != nil {
		return plugin.Errorf(plugin.CodeUnavailable, "%v", err)
	}
	if req.GetCursor() == sum && !req.GetForce() {
		if err := w.Started(&metadatav1.SyncStarted{Kind: plugin.KindDump, SnapshotId: sum, FullSnapshot: true, Unchanged: true}); err != nil {
			return err
		}
		return w.Finished(sum)
	}

	// Started goes first; the record count is only known once the dump is
	// downloaded, so it comes later as a "read" progress total.
	if err := w.Started(&metadatav1.SyncStarted{Kind: plugin.KindDump, SnapshotId: sum, FullSnapshot: true}); err != nil {
		return err
	}
	snap, err := src.Fetch(ctx, sum)
	if err != nil {
		if errors.Is(err, dump.ErrChecksumMismatch) {
			return plugin.Errorf(plugin.CodeUnavailable, "%v", err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return plugin.Errorf(plugin.CodeUnavailable, "%v", err)
	}
	return Stream(ctx, snap, s.CheckpointEvery, w)
}

// Stream reads a fetched snapshot and sends its records. The caller has
// sent SyncStarted; Stream ends the sync with a checkpoint and SyncFinished.
func Stream(ctx context.Context, snap *dump.Snapshot, checkpointEvery int64, w *plugin.SyncWriter) error {
	r, err := dump.Open(ctx, snap.Path)
	if err != nil {
		return plugin.Errorf(plugin.CodeInternal, "%v", err)
	}
	defer func() { _ = r.Close() }()
	count, err := r.Count(ctx)
	if err != nil {
		return plugin.Errorf(plugin.CodeInternal, "%v", err)
	}
	if err := w.Progress("read", 0, count, "records"); err != nil {
		return err
	}

	var lastID string
	w.CheckpointEvery = checkpointEvery
	w.Cursor = func(int64) string { return snap.SHA1 + ":" + lastID }

	err = r.Each(ctx, func(row dump.Row) error {
		res, err := mapping.Series(mapping.Row(row))
		if err != nil {
			var ref *metadatav1.ExternalId
			if id := mapping.ID(mapping.Row(row)); id != "" {
				ref = mapping.Ref(id)
			}
			return w.Skip(ref, err.Error())
		}
		lastID = res.Entry.GetRef().GetId()
		if err := w.Upsert(res.Entry); err != nil {
			return err
		}
		if res.MergedInto != "" {
			return w.Merge(mapping.Ref(lastID), mapping.Ref(res.MergedInto))
		}
		return nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if err := w.Checkpoint(snap.SHA1 + ":" + lastID); err != nil {
		return err
	}
	return w.Finished(snap.SHA1)
}
