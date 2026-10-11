// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok\n") }))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer bad.Close()

	if code := runHealthcheck([]string{"--url", ok.URL}, io.Discard); code != 0 {
		t.Errorf("healthy: exit %d", code)
	}
	if code := runHealthcheck([]string{"--url", bad.URL}, io.Discard); code != 1 {
		t.Errorf("unhealthy: exit %d", code)
	}
	if code := runHealthcheck([]string{"--url", "http://127.0.0.1:1/healthz", "--timeout", "200ms"}, io.Discard); code != 1 {
		t.Errorf("down: exit %d", code)
	}
	if code := runHealthcheck([]string{"--nope"}, io.Discard); code != 2 {
		t.Errorf("bad flag: exit %d", code)
	}
}

func TestHealthURL(t *testing.T) {
	t.Setenv("PLUGIN_LISTEN", ":9100")
	if got := healthURL(); got != "http://127.0.0.1:9100/healthz" {
		t.Errorf("got %q", got)
	}
	t.Setenv("PLUGIN_LISTEN", "")
	if got := healthURL(); got != "http://127.0.0.1:8903/healthz" {
		t.Errorf("got %q", got)
	}
}
