// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	metadatav1 "github.com/panelarr/meta/gen/panelarr/metadata/v1"
	"github.com/panelarr/meta/sdk/plugin"

	"github.com/panelarr/meta-mangabaka/internal/dump"
	"github.com/panelarr/meta-mangabaka/internal/mangabaka"
)

// runDump downloads (or reuses) a dump, reads every series through the same
// code a sync uses, and prints counts and timings. It prints statistics
// only, never records.
func runDump(args []string) int {
	fs := flag.NewFlagSet("dump", flag.ExitOnError)
	dir := fs.String("dir", mangabaka.DefaultDownloadDir(), "download directory")
	variant := fs.String("variant", dump.VariantDefault, "dump variant: default or full")
	url := fs.String("url", dump.DefaultBaseURL, "dump base URL")
	keep := fs.Bool("keep", false, "keep the compressed download")
	_ = fs.Parse(args)

	p := mangabaka.New()
	if err := p.Configure(map[string]any{
		"download_dir": *dir, "dump_variant": *variant, "dump_url": *url, "keep_downloads": *keep,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var (
		start     = time.Now()
		phases    = map[string]time.Duration{}
		phase     string
		phaseAt   = start
		events    = map[string]int64{}
		types     = map[string]int64{}
		states    = map[string]int64{}
		snapshot  string
		lastPrint time.Time
	)
	setPhase := func(p string) {
		if p == phase {
			return
		}
		if phase != "" {
			phases[phase] += time.Since(phaseAt)
		}
		phase, phaseAt = p, time.Now()
	}
	send := func(ev *metadatav1.SyncResponse) error {
		switch e := ev.GetEvent().(type) {
		case *metadatav1.SyncResponse_Started:
			snapshot = e.Started.GetSnapshotId()
		case *metadatav1.SyncResponse_Progress:
			setPhase(e.Progress.GetPhase())
			if e.Progress.GetPhase() == "read" {
				events["expected"] = e.Progress.GetTotal()
			}
			if time.Since(lastPrint) > 5*time.Second {
				lastPrint = time.Now()
				fmt.Fprintf(os.Stderr, "%s: %d / %d bytes\n", e.Progress.GetPhase(), e.Progress.GetDone(), e.Progress.GetTotal())
			}
		case *metadatav1.SyncResponse_Upsert:
			events["upserts"]++
			types[e.Upsert.GetRawType()]++
			states[e.Upsert.GetState().String()]++
			if events["upserts"]%50000 == 0 {
				fmt.Fprintf(os.Stderr, "read %d series\n", events["upserts"])
			}
		case *metadatav1.SyncResponse_Merge:
			events["merges"]++
		case *metadatav1.SyncResponse_Checkpoint:
			events["checkpoints"]++
		case *metadatav1.SyncResponse_Notice:
			events["notices"]++
		case *metadatav1.SyncResponse_Finished:
			setPhase("")
			events["skipped"] = e.Finished.GetSkipped()
		}
		return nil
	}
	err := p.Sync(ctx, &metadatav1.SyncRequest{Kind: plugin.KindDump, Force: true}, send)
	setPhase("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "dump failed:", err)
		return 1
	}

	fmt.Printf("snapshot     %s\n", snapshot)
	fmt.Printf("total time   %s\n", time.Since(start).Round(time.Millisecond))
	for _, ph := range []string{"download", "verify", "decompress", "read"} {
		if d, ok := phases[ph]; ok {
			fmt.Printf("  %-10s %s\n", ph, d.Round(time.Millisecond))
		}
	}
	src := &dump.Source{Variant: *variant, Dir: *dir}
	if st, err := os.Stat(src.DatabasePath(snapshot)); err == nil {
		fmt.Printf("sqlite size  %.1f MiB\n", float64(st.Size())/(1<<20))
	}
	for _, k := range []string{"expected", "upserts", "merges", "skipped", "notices", "checkpoints"} {
		fmt.Printf("%-12s %d\n", k, events[k])
	}
	printCounts("by type", types)
	printCounts("by state", states)
	return 0
}

func printCounts(title string, m map[string]int64) {
	fmt.Println(title)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		name := k
		if name == "" {
			name = "(none)"
		}
		fmt.Printf("  %-28s %d\n", name, m[k])
	}
}
