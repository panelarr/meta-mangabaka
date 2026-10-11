// SPDX-License-Identifier: GPL-3.0-or-later

package mangabaka_test

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	metadatav1 "github.com/panelarr/meta/gen/panelarr/metadata/v1"

	"github.com/panelarr/meta-mangabaka/internal/fixture"
	"github.com/panelarr/meta-mangabaka/internal/mangabaka"
)

// forwardProxy is an HTTP proxy that records what it forwards and requires
// credentials.
type forwardProxy struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
	agent []string
}

func newProxy(t *testing.T, user, pass string) *forwardProxy {
	t.Helper()
	fp := &forwardProxy{}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	fp.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != want {
			w.Header().Set("Proxy-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		fp.mu.Lock()
		fp.paths = append(fp.paths, r.URL.Path)
		fp.agent = append(fp.agent, r.UserAgent())
		fp.mu.Unlock()
		out, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out.Header = r.Header.Clone()
		out.Header.Del("Proxy-Authorization")
		res, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer res.Body.Close()
		for k, v := range res.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(w, res.Body)
	}))
	t.Cleanup(fp.Close)
	return fp
}

func (fp *forwardProxy) seen() ([]string, []string) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return append([]string(nil), fp.paths...), append([]string(nil), fp.agent...)
}

func profile(proxyURL string) *metadatav1.SetNetworkProfileRequest {
	return &metadatav1.SetNetworkProfileRequest{Profile: &metadatav1.NetworkProfile{
		Name:      "vpn",
		Proxy:     &metadatav1.Proxy{Url: proxyURL, Username: "vpn", Password: "secret"},
		UserAgent: &metadatav1.UserAgent{Mode: &metadatav1.UserAgent_Fixed{Fixed: "ProfileUA/1"}},
	}}
}

func TestDumpDownloadsThroughTheProfileProxy(t *testing.T) {
	srv := fixture.NewServer(t)
	proxy := newProxy(t, "vpn", "secret")
	p := newPlugin(t, srv)
	if _, err := p.SetNetworkProfile(context.Background(), profile(proxy.URL)); err != nil {
		t.Fatal(err)
	}
	var ev events
	if err := p.Sync(context.Background(), &metadatav1.SyncRequest{Kind: "dump"}, ev.send); err != nil {
		t.Fatal(err)
	}
	if ev.finished() == nil || ev.finished().GetUpserts() == 0 {
		t.Fatalf("sync through the proxy: %v", ev.finished())
	}
	paths, agents := proxy.seen()
	joined := strings.Join(paths, " ")
	if !strings.Contains(joined, ".sha1") || !strings.Contains(joined, ".sqlite.zst") {
		t.Fatalf("the proxy didn't carry the download: %v", paths)
	}
	for _, a := range agents {
		if a != "ProfileUA/1" {
			t.Fatalf("user agent through the proxy: %q", a)
		}
	}
	if len(srv.UserAgents()) != len(paths) {
		t.Fatalf("requests reached the dump server outside the proxy: %d direct vs %d proxied", len(srv.UserAgents()), len(paths))
	}

	// Back to direct: the proxy sees nothing more.
	if _, err := p.SetNetworkProfile(context.Background(), &metadatav1.SetNetworkProfileRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := p.Sync(context.Background(), &metadatav1.SyncRequest{Kind: "dump", Force: true}, (&events{}).send); err != nil {
		t.Fatal(err)
	}
	if after, _ := proxy.seen(); len(after) != len(paths) {
		t.Fatalf("direct sync went through the proxy: %v", after)
	}
}

func TestLookupsGoThroughTheProfileProxy(t *testing.T) {
	apiSrv := fixture.NewAPIServer(t)
	proxy := newProxy(t, "vpn", "secret")
	p := lookupPlugin(t, apiSrv)
	if _, err := p.SetNetworkProfile(context.Background(), profile(proxy.URL)); err != nil {
		t.Fatal(err)
	}
	if _, err := get(t, p, "mangabaka", "101"); err != nil {
		t.Fatal(err)
	}
	paths, _ := proxy.seen()
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "/series/101") {
		t.Fatalf("lookup through the proxy: %v", paths)
	}
}

func TestBadProfileIsRejected(t *testing.T) {
	p := mangabaka.New()
	_, err := p.SetNetworkProfile(context.Background(), &metadatav1.SetNetworkProfileRequest{Profile: &metadatav1.NetworkProfile{Proxy: &metadatav1.Proxy{Url: "ftp://nope"}}})
	if err == nil {
		t.Fatal("bad proxy accepted")
	}
}
