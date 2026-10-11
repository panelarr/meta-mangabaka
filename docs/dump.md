# Dump ingestion

The `dump` ingestion streams MangaBaka's whole catalogue to meta from its nightly SQLite dump.

## Steps

1. **Checksum.** Fetch `<dump_url>/series.sqlite.zst.sha1` (or `series.full.sqlite.zst.sha1`). If it equals the cursor meta sends (the checksum of the last dump meta ingested), the sync reports *unchanged* and ends. meta can force a run anyway.
2. **Download** `series.sqlite.zst` into `download_dir` as `<name>.<sha1>.part`. An interrupted download resumes with an HTTP `Range` request. If the server ignores the range, the download starts over.
3. **Verify** the SHA-1 of the whole file against the published checksum. On a mismatch the file is deleted and never decompressed, and the sync fails.
4. **Decompress** (zstd) to `series.<sha1>.sqlite`. Unless `keep_downloads` is set, the compressed file is then deleted. Files left by other checksums are cleaned up. If the database for the current checksum is already there, steps 2–4 are skipped.
5. **Read** the database read-only with a pure-Go SQLite driver (no cgo). The dump is one wide `series` table: nested objects are flattened into `parent_child` columns and arrays are JSON text. Column types drift between dumps (a number may be stored as text), so the reader discovers the columns at run time and every field is read tolerantly.
6. **Stream** every series to meta in ID order: one upsert each, a merge event right after every merged series, a checkpoint every `checkpoint_every` records (cursor `<sha1>:<last id>`), and finally the checksum as the cursor for the next run. Rows that can't be mapped (no ID or no title) are skipped and reported as notices.

meta gets `started` first, then progress events for the download, verification and decompression (in bytes), and a `read` progress event whose total is the number of rows.

## Running a real dump by hand

CI never touches MangaBaka. To try the real dump on your machine:

```sh
make real-dump DIR=/path/with/a/few/GB    # or: meta-mangabaka dump --dir DIR [--variant full]
```

It downloads (or reuses) the dump, reads every series through the same code a sync uses, and prints timings, the database size and counts by type and state. It prints statistics only, never records.
