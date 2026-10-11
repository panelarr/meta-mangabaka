# meta-mangabaka

A metadata plugin for [meta](https://github.com/panelarr/meta), the metadata service of [Panelarr](https://github.com/panelarr). It feeds meta from [MangaBaka](https://mangabaka.org), an open manga metadata database that cross-references many other sites:

- **dump:** downloads MangaBaka's nightly SQLite database dump, verifies it, reads it and streams every series to meta;
- **lookup:** searches and fetches single series through the MangaBaka API, also by AniList, MyAnimeList, MangaUpdates, Kitsu or Anime-Planet ID, to refresh records on demand. It keeps to MangaBaka's rate limits (30 searches and 180 other requests a minute) and backs off when asked to.

> **Status:** early development. Nothing is released yet. Progress is tracked in meta's [PROGRESS.md](https://github.com/panelarr/meta/blob/main/PROGRESS.md).

## Quick start

meta listens on port **8901**; standalone metadata plugins use **8903** and up.

### Docker

```sh
docker run -d --name meta-mangabaka \
  -p 8903:8903 \
  -e PLUGIN_API_KEY=change-me \
  -v mangabaka-data:/data \
  panelarr/meta-mangabaka
```

The image runs as a non-root user, listens on `:8903`, keeps downloads in the `/data` volume (the dump needs about 3.7 GB, see [docs/dump.md](docs/dump.md#disk-space)) and has a built-in health check.

### Binary

```sh
make build
./bin/meta-mangabaka --listen :8903 --api-key change-me
```

### Add it to meta

In meta's configuration (see meta's [configuration docs](https://github.com/panelarr/meta/blob/main/docs/configuration.md)):

```yaml
providers:
  - id: mangabaka
    remote:
      url: http://meta-mangabaka:8903   # the plugin's address as meta sees it
      api_key: change-me                # the plugin's PLUGIN_API_KEY
    settings:
      dump_variant: default
    schedules:
      dump: "0 3 * * *"                 # daily, after MangaBaka builds its dump at 00:00 UTC
```

Or let meta start the binary itself (managed mode); no listen address or key is needed then:

```yaml
providers:
  - id: mangabaka
    managed:
      path: /plugins/meta-mangabaka
    settings:
      download_dir: /data/mangabaka
    schedules:
      dump: "0 3 * * *"
```

Then run a first sync from meta (`meta sync mangabaka`, or `POST /v1/admin/providers/mangabaka/sync`).

## Documentation

- [Running the plugin](docs/running.md): standalone or managed, the image, volumes, health, upgrades
- [Configuration](docs/configuration.md): settings and environment variables
- [Dump ingestion](docs/dump.md) and [API lookups](docs/lookup.md)
- [Field mapping](docs/mapping.md) from MangaBaka to meta's entry schema

## MangaBaka data and its licence

MangaBaka's data is published under **CC BY-NC-SA 4.0** (data that MangaBaka takes from other sites stays under those sites' terms). This plugin contains **no MangaBaka data**. It downloads the dump only when the person running it tells it to, keeps attribution on every record it produces, drops links MangaBaka marks as piracy, and respects MangaBaka's API rate limits. If you run it, you are responsible for using the data within that licence.

## Licence

meta-mangabaka is free software under the **GNU General Public License v3.0 or later** ([LICENSE](LICENSE)). This covers the plugin's code only; MangaBaka's data keeps its own licence (see above).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security issues: [SECURITY.md](SECURITY.md).
