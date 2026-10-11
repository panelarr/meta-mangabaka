# Lookups through the API

The `lookup` ingestion answers meta's `Search` and `Get` calls with MangaBaka's REST API (`api_url`, default `https://api.mangabaka.org`). meta uses it for on-demand searches and to refresh a single record that has gone stale between dumps.

Lookup results are mapped exactly like dump rows: the API's nested series object is flattened into the dump's column form (`cover.raw.url` becomes `cover_raw_url`, arrays become JSON text, booleans 0/1) and goes through the same mapping. A series looked up through the API and the same series read from the dump give the same record, which the tests check for every fixture series.

## Calls

| meta call | API request | Notes |
|---|---|---|
| `Search` | `GET /v1/series/search?q=&page=&limit=` | `limit` 1–100 (default 10). Type and content-rating filters are sent as repeated `type=` / `content_rating=` keys (light and web novels map to `novel`, erotic to `erotica`). The page token is the page number. MangaBaka pages only through the first 12,000 results, so later pages come back empty without a request. Search never returns merged series |
| `Get` with a `mangabaka` ID | `GET /v1/series/{id}` | A merged series comes back as merged, with `merged_into` set; meta follows the merge. Deleted or unknown IDs are `not_found` |
| `Get` with an `anilist`, `mal`, `mangaupdates`, `kitsu` or `animeplanet` ID | `GET /v1/source/{site}/{id}?with_merged_series=true` | Returns MangaBaka's active series for that ID. When only a merged series carries the ID, the plugin follows `merged_with` (up to three hops) to the series it was merged into |
| `Get` with any other provider | none | `not_found`: MangaBaka has no lookup for it |

## Rate limits

MangaBaka allows, per IP: **30 searches a minute** and **180 other requests a minute** (a leaky bucket; cached answers don't count). The plugin declares both limits in `GetInfo` (operations `search` and `get`) so meta can enforce them, and it also keeps to them itself with two separate token buckets, spacing requests evenly (no bursts). If other MangaBaka clients share your IP, lower `api_search_per_minute` and `api_get_per_minute`.

## Back-off

- **429 Too Many Requests:** the plugin waits for `Retry-After` when the API sends one (seconds or an HTTP date), otherwise backs off exponentially: 1 s, 2 s, 4 s, 8 s (up to 30 s) with up to 25 % jitter. After 4 retries, or when `Retry-After` asks for more than 2 minutes, it gives up and answers `resource_exhausted` with the retry delay as a hint (`google.rpc.RetryInfo`), so meta can try again later.
- **5xx and network errors:** retried with the same back-off, then reported as `unavailable`.
- **400:** reported as `invalid_argument` with MangaBaka's message.

Every wait stops when meta cancels the call. Requests send the plugin's `User-Agent` (`meta-mangabaka/<version> (+https://github.com/panelarr/meta-mangabaka)`), and each one times out after `api_timeout_seconds`.
