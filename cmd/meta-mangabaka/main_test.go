// SPDX-License-Identifier: GPL-3.0-or-later

package main_test

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/panelarr/meta/sdk/client"
	"github.com/panelarr/meta/sdk/conformance"

	"github.com/panelarr/meta-mangabaka/internal/fixture"
)

func build(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a binary")
	}
	bin := filepath.Join(t.TempDir(), "meta-mangabaka")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Env = append(cmd.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// TestManagedBinary builds the plugin and runs the conformance harness
// against it as a managed plugin (go-plugin handshake, gRPC).
func TestManagedBinary(t *testing.T) {
	bin := build(t)
	srv := fixture.NewServer(t)
	c, err := client.Managed(bin, client.ManagedOptions{Env: []string{"MANGABAKA_DOWNLOAD_DIR=" + t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	rep := conformance.Run(t, c, conformance.Options{
		Settings: map[string]any{"dump_url": srv.BaseURL()},
	})
	if rep.Info.GetId() != "mangabaka" || rep.Events["upsert"] == 0 {
		t.Fatalf("report %v %v", rep.Info.GetId(), rep.Events)
	}
}

// TestDumpCommand runs the manual "dump" command against the fixture and
// checks it prints statistics.
func TestDumpCommand(t *testing.T) {
	bin := build(t)
	srv := fixture.NewServer(t)
	out, err := exec.Command(bin, "dump", "--dir", t.TempDir(), "--url", srv.BaseURL()).Output()
	if err != nil {
		t.Fatalf("dump: %v\n%s", err, out)
	}
	for _, want := range []string{"snapshot     " + srv.SHA1, "upserts      " + strconv.Itoa(len(fixture.All())-fixture.ExpectedSkipped), "merges       2", "sqlite size"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
