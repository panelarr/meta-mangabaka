# Configuration

## Running the plugin

| Mode | How |
|---|---|
| **Standalone** | `meta-mangabaka --listen :8903 --api-key KEY` (or `PLUGIN_LISTEN`, `PLUGIN_API_KEY`). Run by hand with no flags, it listens on `:8903`. Add it to meta as a remote provider by URL and key |
| **Managed** | meta starts the binary itself; nothing to configure here |

`GET /healthz` answers without a key, for container health checks.

## Settings

meta shows these as a form (the plugin publishes a JSON Schema) and sends them to the plugin. Every setting has a default.

| Setting | Default | What |
|---|---|---|
| `dump_variant` | `default` | `default` or `full`. The full dump adds every source site's raw response; meta doesn't need it and it is much larger |
| `download_dir` | `$MANGABAKA_DOWNLOAD_DIR`, else `<user cache dir>/meta-mangabaka` | Where the dump is downloaded and decompressed. Needs a few GB free |
| `dump_url` | `https://api.mangabaka.org/v1/database` | The directory the dump files are published in. Change it only for a mirror |
| `api_url` | `https://api.mangabaka.org` | MangaBaka's API, for lookups |
| `checkpoint_every` | `1000` | Records between checkpoints, where meta commits a sync's progress |
| `keep_downloads` | `false` | Keep the compressed dump after decompressing it |

## Environment

| Variable | What |
|---|---|
| `MANGABAKA_DOWNLOAD_DIR` | Default for `download_dir` |
| `PLUGIN_LISTEN`, `PLUGIN_API_KEY` | Standalone address and API key |
| `HTTPS_PROXY`, `NO_PROXY` | Standard proxy settings for downloads |

## Schedule

The plugin suggests running the dump sync daily at 03:00 (`0 3 * * *`). MangaBaka builds its dump at 00:00 UTC. meta decides the real schedule. A sync whose dump hasn't changed since the last run ends at once without downloading anything.
