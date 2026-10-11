# meta-mangabaka

A metadata plugin for [meta](https://github.com/panelarr/meta), the metadata service of [Panelarr](https://github.com/panelarr). It feeds meta from [MangaBaka](https://mangabaka.org), an open manga metadata database that cross-references many other sites:

- **dump:** downloads MangaBaka's nightly SQLite database dump, verifies it, reads it and streams every series to meta;
- **lookup:** searches and fetches single series through the MangaBaka API, to refresh records on demand.

> **Status:** early development. Nothing is released yet. Progress is tracked in meta's [PROGRESS.md](https://github.com/panelarr/meta/blob/main/PROGRESS.md).

## Quick start

```sh
make build
./bin/meta-mangabaka --listen :8903 --api-key change-me
```

Then add it to meta as a remote provider (`http://<host>:8903` with the same key), or let meta run the binary as a managed plugin. See [docs/configuration.md](docs/configuration.md) for the settings, [docs/dump.md](docs/dump.md) for how the dump ingestion works and [docs/mapping.md](docs/mapping.md) for the field mapping.

## MangaBaka data and its licence

MangaBaka's data is published under **CC BY-NC-SA 4.0** (data that MangaBaka takes from other sites stays under those sites' terms). This plugin contains **no MangaBaka data**. It downloads the dump only when the person running it tells it to, keeps attribution on every record it produces, drops links MangaBaka marks as piracy, and respects MangaBaka's API rate limits. If you run it, you are responsible for using the data within that licence.

## Licence

meta-mangabaka is free software under the **GNU General Public License v3.0 or later** ([LICENSE](LICENSE)). This covers the plugin's code only; MangaBaka's data keeps its own licence (see above).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security issues: [SECURITY.md](SECURITY.md).
