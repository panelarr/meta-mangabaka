// SPDX-License-Identifier: GPL-3.0-or-later

package dump_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panelarr/meta-mangabaka/internal/dump"
	"github.com/panelarr/meta-mangabaka/internal/fixture"
)

func source(srv *fixture.Server, dir string) *dump.Source {
	return &dump.Source{
		BaseURL:   srv.BaseURL(),
		Variant:   dump.VariantDefault,
		Dir:       dir,
		HTTP:      http.DefaultClient,
		UserAgent: "meta-mangabaka-test",
	}
}

func TestParseChecksum(t *testing.T) {
	sum, err := dump.ParseChecksum("0123456789ABCDEF0123456789ABCDEF01234567  series.sqlite.zst\n")
	if err != nil || sum != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("got %q %v", sum, err)
	}
	for _, bad := range []string{"", "nothex  x", "abc"} {
		if _, err := dump.ParseChecksum(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestFetchVerifiesAndDecompresses(t *testing.T) {
	srv := fixture.NewServer(t)
	dir := t.TempDir()
	src := source(srv, dir)
	var phases []string
	src.Progress = func(phase string, _, _ int64) {
		if len(phases) == 0 || phases[len(phases)-1] != phase {
			phases = append(phases, phase)
		}
	}
	ctx := context.Background()
	sum, err := src.RemoteChecksum(ctx)
	if err != nil || sum != srv.SHA1 {
		t.Fatalf("checksum %q %v", sum, err)
	}
	snap, err := src.Fetch(ctx, sum)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Reused || snap.SHA1 != sum {
		t.Fatalf("snapshot %+v", snap)
	}
	if strings.Join(phases, ",") != "download,verify,decompress" {
		t.Fatalf("phases %v", phases)
	}
	r, err := dump.Open(ctx, snap.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	n, err := r.Count(ctx)
	if err != nil || n != int64(len(fixture.All())) {
		t.Fatalf("count %d %v", n, err)
	}
	// The compressed download is gone; only the database is kept.
	files, _ := os.ReadDir(dir)
	if len(files) != 1 || files[0].Name() != filepath.Base(snap.Path) {
		t.Fatalf("files left: %v", files)
	}
	for _, ua := range srv.UserAgents() {
		if ua != "meta-mangabaka-test" {
			t.Fatalf("user agent %q", ua)
		}
	}

	// Same checksum again: reused, nothing downloaded.
	before := len(srv.Requests())
	snap2, err := src.Fetch(ctx, sum)
	if err != nil || !snap2.Reused || len(srv.Requests()) != before {
		t.Fatalf("reuse: %+v %v, requests %v", snap2, err, srv.Requests()[before:])
	}
}

func TestFetchResumesInterruptedDownload(t *testing.T) {
	srv := fixture.NewServer(t)
	srv.CutDownloadsAfter(int(srv.Size / 3))
	src := source(srv, t.TempDir())
	ctx := context.Background()
	if _, err := src.Fetch(ctx, srv.SHA1); err == nil {
		t.Fatal("interrupted download succeeded")
	}
	srv.CutDownloadsAfter(0)
	if _, err := src.Fetch(ctx, srv.SHA1); err != nil {
		t.Fatal(err)
	}
	reqs := srv.Requests()
	last := reqs[len(reqs)-1]
	if !strings.Contains(last, "bytes=") {
		t.Fatalf("second download did not resume with a range: %v", reqs)
	}
}

func TestFetchRefusesChecksumMismatch(t *testing.T) {
	srv := fixture.NewServer(t)
	srv.PublishWrongChecksum()
	dir := t.TempDir()
	src := source(srv, dir)
	ctx := context.Background()
	sum, err := src.RemoteChecksum(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = src.Fetch(ctx, sum)
	if !errors.Is(err, dump.ErrChecksumMismatch) {
		t.Fatalf("want checksum mismatch, got %v", err)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatalf("files left after a mismatch: %v", files)
	}
}

func TestFetchCleansOldSnapshots(t *testing.T) {
	srv := fixture.NewServer(t)
	dir := t.TempDir()
	src := source(srv, dir)
	old := src.DatabasePath(strings.Repeat("a", 40))
	if err := os.WriteFile(old, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	stalePart := filepath.Join(dir, "series.sqlite.zst."+strings.Repeat("b", 40)+".part")
	if err := os.WriteFile(stalePart, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Fetch(context.Background(), srv.SHA1); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{old, stalePart} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s not cleaned up", filepath.Base(p))
		}
	}
}

func TestFetchCancelled(t *testing.T) {
	srv := fixture.NewServer(t)
	src := source(srv, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := src.Fetch(ctx, srv.SHA1); !errors.Is(err, context.Canceled) {
		t.Fatalf("want canceled, got %v", err)
	}
}
