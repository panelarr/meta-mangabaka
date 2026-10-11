// SPDX-License-Identifier: GPL-3.0-or-later

package dump_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panelarr/meta-mangabaka/internal/dump"
	"github.com/panelarr/meta-mangabaka/internal/fixture"
)

func TestDSNIsAbsolute(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	dsn, err := dump.DSN("data/series.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dsn, "file:///") || !strings.HasSuffix(dsn, "?mode=ro&immutable=1") {
		t.Fatalf("dsn %q", dsn)
	}
	if !strings.Contains(dsn, "/data/series.sqlite") {
		t.Fatalf("dsn %q lost the path", dsn)
	}
}

func TestOpenRelativePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "data"), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if err := fixture.Build(context.Background(), "data/series.sqlite"); err != nil {
		t.Fatal(err)
	}
	r, err := dump.Open(context.Background(), "./data/series.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	n, err := r.Count(context.Background())
	if err != nil || n != int64(len(fixture.All())) {
		t.Fatalf("count %d %v", n, err)
	}
}
