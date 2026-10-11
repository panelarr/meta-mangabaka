// SPDX-License-Identifier: GPL-3.0-or-later

// Command meta-mangabaka is a metadata plugin that feeds meta from MangaBaka.
//
// Run it standalone with --listen (default :8903), let meta start it as a
// managed plugin, or use the "dump" subcommand to download and read a dump
// once and print statistics. "healthcheck" probes a running instance, for
// container health checks.
package main

import (
	"fmt"
	"os"

	"github.com/panelarr/meta/sdk/plugin"

	"github.com/panelarr/meta-mangabaka/internal/buildinfo"
	"github.com/panelarr/meta-mangabaka/internal/mangabaka"
)

func main() {
	if len(os.Args) > 1 && !plugin.IsManaged() {
		switch os.Args[1] {
		case "version":
			fmt.Println(buildinfo.Version)
			return
		case "dump":
			os.Exit(runDump(os.Args[2:]))
		case "healthcheck":
			os.Exit(runHealthcheck(os.Args[2:], os.Stderr))
		}
	}
	plugin.Main(mangabaka.New(), plugin.WithDefaultListen(":8903"))
}
