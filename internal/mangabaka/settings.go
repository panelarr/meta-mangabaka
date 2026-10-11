// SPDX-License-Identifier: GPL-3.0-or-later

package mangabaka

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/panelarr/meta/sdk/settings"

	"github.com/panelarr/meta-mangabaka/internal/dump"
)

// DefaultAPIURL is MangaBaka's API.
const DefaultAPIURL = "https://api.mangabaka.org"

// Settings are the plugin's settings. meta stores them and sends them with
// SetSettings.
type Settings struct {
	DumpVariant     string `json:"dump_variant"`
	DownloadDir     string `json:"download_dir"`
	DumpURL         string `json:"dump_url"`
	APIURL          string `json:"api_url"`
	CheckpointEvery int64  `json:"checkpoint_every"`
	KeepDownloads   bool   `json:"keep_downloads"`
}

// DefaultDownloadDir is $MANGABAKA_DOWNLOAD_DIR, or a directory in the
// user's cache directory. The container image sets MANGABAKA_DOWNLOAD_DIR
// to its data volume.
func DefaultDownloadDir() string {
	if d := os.Getenv("MANGABAKA_DOWNLOAD_DIR"); d != "" {
		return d
	}
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "meta-mangabaka")
	}
	return filepath.Join(os.TempDir(), "meta-mangabaka")
}

func schemaDoc() string {
	doc := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"title":                "MangaBaka",
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"dump_variant": map[string]any{
				"type":        "string",
				"enum":        []string{dump.VariantDefault, dump.VariantFull},
				"default":     dump.VariantDefault,
				"title":       "Dump variant",
				"description": "\"default\" is enough for meta. \"full\" also contains every source site's raw response and is much larger.",
			},
			"download_dir": map[string]any{
				"type":        "string",
				"minLength":   1,
				"default":     DefaultDownloadDir(),
				"title":       "Download directory",
				"description": "Where the dump is downloaded and decompressed. Needs a few GB of free space.",
			},
			"dump_url": map[string]any{
				"type":        "string",
				"format":      "uri",
				"default":     dump.DefaultBaseURL,
				"title":       "Dump URL",
				"description": "The directory the dump files are published in. Change it only to use a mirror.",
			},
			"api_url": map[string]any{
				"type":        "string",
				"format":      "uri",
				"default":     DefaultAPIURL,
				"title":       "API URL",
				"description": "MangaBaka's API, used for lookups.",
			},
			"checkpoint_every": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     100000,
				"default":     1000,
				"title":       "Checkpoint every",
				"description": "Records between checkpoints, where meta commits a sync's progress.",
			},
			"keep_downloads": map[string]any{
				"type":        "boolean",
				"default":     false,
				"title":       "Keep downloads",
				"description": "Keep the compressed dump after it was decompressed.",
			},
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func newSchema() *settings.Schema {
	s, err := settings.Parse(schemaDoc())
	if err != nil {
		panic(err)
	}
	return s
}

// parseSettings validates values, fills defaults and decodes them.
func parseSettings(schema *settings.Schema, values map[string]any) (Settings, map[string]any, error) {
	if err := schema.Validate(values); err != nil {
		return Settings{}, nil, err
	}
	full := schema.WithDefaults(values)
	b, err := json.Marshal(full)
	if err != nil {
		return Settings{}, nil, err
	}
	var s Settings
	if err := json.Unmarshal(b, &s); err != nil {
		return Settings{}, nil, fmt.Errorf("settings: %w", err)
	}
	// Work with an absolute download directory, so a relative setting
	// means the same thing everywhere (SQLite URIs, file names in logs).
	if s.DownloadDir, err = filepath.Abs(s.DownloadDir); err != nil {
		return Settings{}, nil, fmt.Errorf("settings: download_dir: %w", err)
	}
	return s, full, nil
}
