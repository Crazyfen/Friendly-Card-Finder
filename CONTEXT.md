# Domain language

The names this project uses for its own concepts. Architecture and conventions live in [`CLAUDE.md`](CLAUDE.md); this file is only the glossary.

**Bot User** — someone who registered with the bot via `/deckbox <login>`. Carries a Telegram ID and username, and the Deckbox login they claimed. Not every list owner is a Bot User; unregistered owners show up in results without a Telegram mention.

**Deckbox User** — a profile on deckbox.org, identified by its login. Owns up to three Card Lists and carries the `updated_at` timestamp that drives refresh.

**Card List** — one of a Deckbox User's three lists (inventory, tradelist, wishlist), identified by its Deckbox `listId`. Holds card name → quantity. `BodyHash` is the hash of the export it was parsed from, used to skip unchanged saves.

**Scope** — which of the three Card Lists a search runs against: `tradelist` (plain text search), `wishlist` (`/sell`), or `inventory`.

**Query** — one parsed search line: the card name with quotes stripped, whether the user asked for an exact match, and the Scope. Built by `ParseQuery`.

**Exact match** — a query the user wrapped in quotes. Equality ignores case, accents and punctuation: `"vitu ghazi"` matches *Vitu-Ghazi, the City-Tree*. Without quotes, search is prefix-based per token.

**Refresh** — re-scraping a Deckbox User's profile and all three Card Lists, then stamping `updated_at`. There is exactly one implementation (`Deckbox.refreshUser`); registration, the background ticker and `/suggestdeckbox` all go through it.

**Suggest** — the `/suggestdeckbox` bulk Refresh, which skips any Deckbox User whose data is still fresh.

**Aggregate** — one Card List's contribution to a search result: every queried card it holds, plus the counts search results are ranked by (distinct cards matched, then total quantity).
