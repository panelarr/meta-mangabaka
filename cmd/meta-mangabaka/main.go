// SPDX-License-Identifier: GPL-3.0-or-later

// Command meta-mangabaka is a metadata plugin that feeds meta from MangaBaka.
package main

import (
	"fmt"
	"os"

	"github.com/panelarr/meta-mangabaka/internal/buildinfo"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(buildinfo.Version)
		return
	}
	fmt.Fprintln(os.Stderr, "usage: meta-mangabaka version")
	os.Exit(2)
}
