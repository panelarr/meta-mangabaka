// SPDX-License-Identifier: GPL-3.0-or-later

package mangabaka_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	metadatav1 "github.com/panelarr/meta/gen/panelarr/metadata/v1"
	"github.com/panelarr/meta/sdk/client"
	"github.com/panelarr/meta/sdk/conformance"
	"github.com/panelarr/meta/sdk/plugin"

	"github.com/panelarr/meta-mangabaka/internal/fixture"
	"github.com/panelarr/meta-mangabaka/internal/mangabaka"
)

func settingsFor(t *testing.T, srv *fixture.Server, extra map[string]any) map[string]any {
	s := map[string]any{"download_dir": t.TempDir(), "dump_url": srv.BaseURL(), "checkpoint_every": float64(5)}
	for k, v := range extra {
		s[k] = v
	}
	return s
}

func newPlugin(t *testing.T, srv *fixture.Server) *mangabaka.Plugin {
	t.Helper()
	p := mangabaka.New()
	if err := p.Configure(settingsFor(t, srv, nil)); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestConformance(t *testing.T) {
	srv := fixture.NewServer(t)
	rep := conformance.Run(t, client.InProcess(mangabaka.New()), conformance.Options{
		Settings: settingsFor(t, srv, nil),
	})
	total := int64(len(fixture.All()))
	if rep.Events["upsert"] != total-fixture.ExpectedSkipped || rep.Events["merge"] != 2 || rep.Events["checkpoint"] < 5 {
		t.Fatalf("events %v", rep.Events)
	}
	if rep.Events["progress"] == 0 {
		t.Errorf("no progress events")
	}
}

type events struct {
	all []*metadatav1.SyncResponse
}

func (e *events) send(ev *metadatav1.SyncResponse) error {
	e.all = append(e.all, ev)
	return nil
}

func (e *events) started() *metadatav1.SyncStarted {
	return e.all[firstOf(e.all, "started")].GetStarted()
}
func (e *events) finished() *metadatav1.SyncFinished { return e.all[len(e.all)-1].GetFinished() }

func firstOf(all []*metadatav1.SyncResponse, kind string) int {
	for i, ev := range all {
		if (kind == "started" && ev.GetStarted() != nil) || (kind == "upsert" && ev.GetUpsert() != nil) {
			return i
		}
	}
	return -1
}

func TestSyncStreamsTheSnapshot(t *testing.T) {
	srv := fixture.NewServer(t)
	p := newPlugin(t, srv)
	var ev events
	if err := p.Sync(context.Background(), &metadatav1.SyncRequest{Kind: plugin.KindDump}, ev.send); err != nil {
		t.Fatal(err)
	}
	st := ev.started()
	if st.GetSnapshotId() != srv.SHA1 || !st.GetFullSnapshot() || firstOf(ev.all, "started") != 0 {
		t.Fatalf("started %v", st)
	}
	var readTotal int64
	for _, e := range ev.all {
		if p := e.GetProgress(); p.GetPhase() == "read" {
			readTotal = p.GetTotal()
		}
	}
	if readTotal != int64(len(fixture.All())) {
		t.Fatalf("read progress total %d", readTotal)
	}
	f := ev.finished()
	if f.GetCursor() != srv.SHA1 || f.GetSkipped() != fixture.ExpectedSkipped || f.GetMerges() != 2 {
		t.Fatalf("finished %v", f)
	}
	var lastID string
	var merges []string
	var cursors []string
	for _, e := range ev.all {
		switch {
		case e.GetUpsert() != nil:
			lastID = e.GetUpsert().GetRef().GetId()
		case e.GetMerge() != nil:
			merges = append(merges, e.GetMerge().GetMerged().GetId()+"->"+e.GetMerge().GetInto().GetId())
			if e.GetMerge().GetMerged().GetId() != lastID {
				t.Errorf("merge not right after its record")
			}
		case e.GetCheckpoint() != nil:
			cursors = append(cursors, e.GetCheckpoint().GetCursor())
		case e.GetNotice() != nil:
			if e.GetNotice().GetRef().GetId() != "105" {
				t.Errorf("notice %v", e.GetNotice())
			}
		}
	}
	if strings.Join(merges, ",") != "103->101,109->102" {
		t.Errorf("merges %v", merges)
	}
	if len(cursors) == 0 || !strings.HasPrefix(cursors[0], srv.SHA1+":") || cursors[len(cursors)-1] != srv.SHA1+":"+lastID {
		t.Errorf("cursors %v", cursors)
	}
}

func TestSyncUnchangedAndForce(t *testing.T) {
	srv := fixture.NewServer(t)
	p := newPlugin(t, srv)
	var ev events
	if err := p.Sync(context.Background(), &metadatav1.SyncRequest{Kind: plugin.KindDump, Cursor: srv.SHA1}, ev.send); err != nil {
		t.Fatal(err)
	}
	if !ev.started().GetUnchanged() || len(ev.all) != 2 {
		t.Fatalf("unchanged run sent %d events", len(ev.all))
	}
	for _, r := range srv.Requests() {
		if strings.Contains(r, ".zst ") {
			t.Fatalf("unchanged run downloaded the dump: %v", srv.Requests())
		}
	}
	ev = events{}
	if err := p.Sync(context.Background(), &metadatav1.SyncRequest{Kind: plugin.KindDump, Cursor: srv.SHA1, Force: true}, ev.send); err != nil {
		t.Fatal(err)
	}
	if firstOf(ev.all, "upsert") < 0 {
		t.Fatal("forced run sent no records")
	}
}

func TestSyncChecksumMismatch(t *testing.T) {
	srv := fixture.NewServer(t)
	srv.PublishWrongChecksum()
	p := newPlugin(t, srv)
	var ev events
	err := p.Sync(context.Background(), &metadatav1.SyncRequest{Kind: plugin.KindDump}, ev.send)
	if plugin.ErrorCode(err) != plugin.CodeUnavailable || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("got %v", err)
	}
	if firstOf(ev.all, "upsert") >= 0 {
		t.Fatal("records sent from an unverified dump")
	}
}

func TestSyncCancel(t *testing.T) {
	srv := fixture.NewServer(t)
	p := newPlugin(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	err := p.Sync(ctx, &metadatav1.SyncRequest{Kind: plugin.KindDump}, func(ev *metadatav1.SyncResponse) error {
		if ev.GetUpsert() != nil {
			n++
			if n == 3 {
				cancel()
			}
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want canceled, got %v (after %d upserts)", err, n)
	}
}

func TestSyncRejectsOtherKinds(t *testing.T) {
	p := mangabaka.New()
	err := p.Sync(context.Background(), &metadatav1.SyncRequest{Kind: plugin.KindDelta}, func(*metadatav1.SyncResponse) error { return nil })
	if plugin.ErrorCode(err) != plugin.CodeInvalidArgument {
		t.Fatalf("got %v", err)
	}
}

func TestSettings(t *testing.T) {
	p := mangabaka.New()
	c := client.InProcess(p)
	ctx := context.Background()
	got, err := c.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := got.GetSettings().AsMap()
	if m["dump_variant"] != "default" || m["checkpoint_every"] != float64(1000) || m["dump_url"] == "" {
		t.Fatalf("defaults %v", m)
	}
	for _, bad := range []map[string]any{
		{"dump_variant": "huge"},
		{"checkpoint_every": float64(0)},
		{"surprise": true},
	} {
		st, _ := structpb.NewStruct(bad)
		if _, err := c.SetSettings(ctx, &metadatav1.SetSettingsRequest{Settings: st}); plugin.ErrorCode(err) != plugin.CodeInvalidArgument {
			t.Errorf("%v: got %v", bad, err)
		}
	}
	st, _ := structpb.NewStruct(map[string]any{"dump_variant": "full"})
	res, err := c.SetSettings(ctx, &metadatav1.SetSettingsRequest{Settings: st})
	if err != nil || res.GetSettings().AsMap()["dump_variant"] != "full" || p.Settings().DumpVariant != "full" {
		t.Fatalf("set: %v %v", res, err)
	}
}

func TestUserAgent(t *testing.T) {
	srv := fixture.NewServer(t)
	p := newPlugin(t, srv)
	if err := p.Sync(context.Background(), &metadatav1.SyncRequest{Kind: plugin.KindDump}, func(*metadatav1.SyncResponse) error { return nil }); err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	for _, ua := range srv.UserAgents() {
		if !strings.HasPrefix(ua, "meta-mangabaka/") || !strings.Contains(ua, "github.com/panelarr/meta-mangabaka") {
			t.Fatalf("user agent %q", ua)
		}
	}
}
