# How MangaBaka fields map to meta's entry schema

Each row of the dump's `series` table becomes one `ProviderEntry` with provider key `mangabaka` and the series ID as its ID. The table below describes fields only; no MangaBaka data is reproduced here.

| MangaBaka | Entry schema | Notes |
|---|---|---|
| `id`, `canonical_url` | `ref`, `url` | |
| `state` | `state` | `active`, `merged` → merged, `deleted` → removed |
| `merged_with` | `merged_into` + a merge event | Read as a number whether stored as number or text |
| `type` | `type` + `raw_type` | manga, manhwa, manhua, novel, oel; anything else → other |
| `status` | `status` + `raw_status` | releasing, completed, hiatus, cancelled, upcoming, unknown |
| `content_rating` | `content_rating` + raw | `erotica` → erotic |
| `title` | first title, primary | Language and kind from the matching `titles[]` entry |
| `titles[]` | titles | Traits native / official / alternative → kind; `is_primary` and `note` kept |
| `native_title`, `romanized_title` | titles | Native in the original language; romanized as `<lang>-Latn` |
| `secondary_titles_<lang>` | titles | Type → kind. Language `unknown` → none, legacy `ja-ro` → `ja-Latn`, `es-la` → `es-419`. Identical text in the same language is kept once |
| `description` | one description, `en`, Markdown | |
| `authors[]`, `artists[]` | credits (author, artist) | Plain names: persons without IDs |
| `publishers[]` | company credits | Type `Original` → original publisher, others → publisher, raw type kept; the publisher ID becomes the external ID `mangabaka:publisher/<id>` |
| `is_licensed` | `licenses` (`en`) | Read as English licensing |
| `genres[]` | tags with `is_genre`, first and in this order | `slice_of_life` → "Slice of Life". A `tags_v2` genre node of the same name adds its details |
| `tags_v2[]` | tags | Name, description, parent (by `parent_id`, else from `name_path`), `path` (the parents' names from `name_path`, root first), level, implied-by, genre and explicit flags, content rating, series count, weight; tag ID → `mangabaka:tag/<id>`. The spoiler flag is set on the assignment. Genre nodes missing from `genres[]` follow the genres; the other nodes stay tags even when they share a genre's name ("Settings › Fantasy" is not the genre "Fantasy") |
| `tags[]` | tags | Only when there are no `tags_v2` |
| `links_v2[]` | links | retailer → store, publisher → publisher, webplatform → read, info → database, social → social, news → news, other → other. **Links typed `piracy` are dropped.** The untyped `links[]` list is not used |
| `relationships_v2[]` | relations | Relation type, chronology, manual flag, note; target `mangabaka:<id>`. The older `relationships_<kind>` columns are used only when there are no v2 relations |
| `anime_start`, `anime_end`, `has_anime` | one anime adaptation | Positions kept as free text |
| `final_volume`, `total_chapters` | `progress.final`, `progress.total` / `progress.latest` | Chapters are the total for finished works, the latest otherwise |
| `published_start_date`, `published_end_date` (+ `_is_estimated`), `year` | `dates.start`, `dates.end` | `YYYY-MM-DD` or ISO timestamps; `year` when there is no start date |
| `original_language` | `original_language` | |
| `cover_raw_*`, `cover_x{150,250,350}_x{1,2,3}` | one cover image | Size, format, blurhash, thumbhash; variants by width and pixel density |
| `source_<site>_id` | external IDs | anilist, my_anime_list → `mal`, manga_updates → `mangaupdates`, kitsu, anime_planet → `animeplanet`, anime_news_network → `ann`, shikimori; with links to the record |
| `rating`, `popularity_global_current` | `stats.rating` (0–100), `stats.popularity_rank` | Other sites' ratings stay in `raw` |
| `last_updated_at` | `updated_at` | |
| the whole row | `raw` | JSON text columns are stored as JSON. Three exceptions: `source_*_response` columns (other sites' raw responses, only in the full dump) are left out; links typed `piracy` and the untyped `links[]` list are left out |

Every record carries MangaBaka's attribution (CC BY-NC-SA 4.0).
