# Running the plugin

meta-mangabaka is one binary that runs in two ways. Both speak the same protocol to meta.

| | Standalone (remote) | Managed |
|---|---|---|
| Who starts it | You: a container, a service manager | meta, as a child process |
| How meta reaches it | gRPC at a URL you configure, with an API key | A local socket set up by meta |
| Listen address | `--listen :8903` / `PLUGIN_LISTEN` | none |
| Good for | Containers, another machine, sharing one plugin | A single machine, no extra service |

Standalone plugins for meta use ports from **8903** up (meta itself is on 8901).

## The container image

`panelarr/meta-mangabaka` runs the plugin standalone:

- **User:** non-root (uid 65532), on a minimal base image with no shell.
- **Port:** `8903` (set `PLUGIN_LISTEN` to change it).
- **API key:** `PLUGIN_API_KEY`. Set one; without it, anyone who can reach the port can use the plugin.
- **Volume:** `/data` holds the downloaded dump. Give it about 3.7 GB, more with `keep_downloads` or the full dump ([disk space](dump.md#disk-space)). Keeping the volume between restarts means an unchanged dump is never downloaded twice.
- **Health:** the image's health check runs `meta-mangabaka healthcheck`, which probes `GET /healthz` on the local port. Use the same command, or `GET /healthz` (no key needed), from an orchestrator.

```sh
docker run -d --name meta-mangabaka -p 8903:8903 \
  -e PLUGIN_API_KEY=change-me -v mangabaka-data:/data panelarr/meta-mangabaka
```

### Building the image yourself

Until meta publishes tagged versions, this repo builds against a sibling `meta` checkout (`go.mod` has `replace github.com/panelarr/meta => ../meta`). The Dockerfile receives that checkout as a named build context and copies only the parts it needs (the generated protocol code and the SDK):

```sh
git clone https://github.com/panelarr/meta
git clone https://github.com/panelarr/meta-mangabaka
cd meta-mangabaka
make docker                    # = docker build --build-context meta=../meta -t panelarr/meta-mangabaka:dev .
```

Set `META_DIR` if your meta checkout lives elsewhere. Release images are built by GoReleaser from the release binaries (`Dockerfile.release`).

## Managed mode

Point meta at the binary (`providers[].managed.path`). meta starts it, checks it, restarts it when it crashes and stops it on shutdown. Settings come from meta's configuration; `MANGABAKA_DOWNLOAD_DIR` in the plugin's environment (`managed.env`) or the `download_dir` setting decide where the dump goes.

## Health and logs

- `GET /healthz` answers `ok` while the process serves (standalone mode, no key needed).
- The `Health` call reports `down` when the settings can't work (for example, an unwritable download directory).
- Logs are JSON lines on stderr. In managed mode meta collects them into its own log.

## Upgrades

- **Image:** pull the new tag and recreate the container with the same `/data` volume. The downloaded dump is reused if it is still current.
- **Binary:** replace it and restart (standalone), or restart meta (managed).
- The plugin keeps no state besides the download directory; meta stores everything it ingests and resumes from its own cursors.
