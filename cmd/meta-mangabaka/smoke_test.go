// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/panelarr/meta/sdk/client"
)

// TestSmokeRunningPlugin checks a running instance, e.g. the container
// image: SMOKE_URL=http://127.0.0.1:8903 [SMOKE_API_KEY=...] go test -run Smoke ./cmd/...
func TestSmokeRunningPlugin(t *testing.T) {
	base := os.Getenv("SMOKE_URL")
	if base == "" {
		t.Skip("set SMOKE_URL to check a running plugin")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("healthz: %v %v", res, err)
	}
	_ = res.Body.Close()

	c := client.Remote(base, client.RemoteOptions{APIKey: os.Getenv("SMOKE_API_KEY")}) // gRPC
	info, err := c.GetInfo(ctx)
	if err != nil {
		t.Fatalf("GetInfo over gRPC: %v", err)
	}
	if info.GetId() != "mangabaka" || len(info.GetIngestions()) != 2 {
		t.Fatalf("info: %v", info)
	}
	settings, err := c.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if dir := settings.GetSettings().GetFields()["download_dir"].GetStringValue(); dir != "/data" && os.Getenv("SMOKE_EXPECT_DATA") != "" {
		t.Fatalf("download_dir: %q", dir)
	}
	t.Logf("%s %s, download_dir %s", info.GetName(), info.GetVersion(), settings.GetSettings().GetFields()["download_dir"].GetStringValue())
}
