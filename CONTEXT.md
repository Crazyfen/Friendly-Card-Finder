# Domain language

The names this project uses for its own concepts. Architecture and conventions live in [`CLAUDE.md`](CLAUDE.md); this file is only the glossary.

**Bot User** — someone who registered with the bot via `/deckbox <login>`. Carries a Telegram ID and username, and the Deckbox login they claimed. Not every list owner is a Bot User; unregistered owners show up in results without a Telegram mention.

**Deckbox User** — a profile on deckbox.org, identified by its login. Owns up to three Card Lists and carries the `updated_at` timestamp that drives refresh.

**Card List** — one of a Deckbox User's three lists (inventory, tradelist, wishlist), identified by its Deckbox `listId`. Holds card name → quantity. `BodyHash` is the hash of the export it was parsed from, used to skip unchanged saves.

**Quantity** — how many copies of a card name a Card List holds, summed across every printing of it. The export page carries no variation data (set, printing, foil), so a name appearing on several lines is the same card in different variations and the lines are added together. This is deliberate: the quantities are not duplicates to be de-duplicated, and there is no per-variation number to recover.

**Scope** — which of the three Card Lists a search runs against: `tradelist` (plain text search), `wishlist` (`/sell`), or `inventory`. A named type, not a bare string, because both the storage column and the Telegram wording default to the tradelist when they do not recognise the value.

**Command** — one thing a Bot User can ask for, as a method on `telegram.Commands` taking a Request and returning Replies. The Command owns the wording, the missing-argument case and the Scope; `main.go` owns only the Telegram SDK calls that carry them.

**Request** — one incoming message reduced to what a Command needs: the text with any command prefix stripped, the detected language, and who sent it from where.

**Reply** — one message to send back, and whether it is HTML and whether it quotes the message that triggered it. A Command returns these rather than sending, which is what makes every Command reachable from a test.

**Query** — one parsed search line: the card name with quotes stripped, whether the user asked for an exact match, and the Scope. Built by `ParseQuery`.

**Exact match** — a query the user wrapped in quotes. Equality ignores case, accents and punctuation: `"vitu ghazi"` matches *Vitu-Ghazi, the City-Tree*. Without quotes, search is prefix-based per token.

**Session** — the Deckbox `_tcg_session` cookie the scraper authenticates with, and the rules around it: where it may come from, when it is replaced, and that one rejection causes one login however many workers noticed it.

**Refresh** — re-scraping a Deckbox User's profile and all three Card Lists, then stamping `updated_at`. There is exactly one implementation (`Deckbox.refreshUser`); registration, the background ticker and `/suggestdeckbox` all go through it. A Refresh succeeds when the profile was read and stored — an empty collection is a legitimate answer, not a failure. Only a Deckbox User who could not be read at all stays stale.

**Suggest** — the `/suggestdeckbox` bulk Refresh, which skips any Deckbox User whose data is still fresh.

**Aggregate** — one Card List's contribution to a search result: every queried card it holds, plus the counts search results are ranked by (distinct cards matched, then total quantity).

**Admin Panel** — the web surface at `/` (manage) and `/insights` (analytics), served by the bot process behind Caddy. Its audience is the operator, not Bot Users, so its wording is hardcoded English rather than going through `i18n`.

**Purge** — removing a Deckbox User completely: their three Card Lists, every card row, the FTS rows, the `deckbox_users` row, and the Bot User registration claiming that login. The login is then unknown to the bot until someone registers it again.

**Unlink** — removing only the Bot User registration, leaving the Deckbox User and its Card Lists in place. For when someone's Telegram account changes but their Deckbox login does not.

**Orphan Registration** — a Bot User whose `deckboxLogin` has no Deckbox User: a login claimed via `/deckbox` that never produced a collection, usually a typo. Invisible to search, so the Admin Panel surfaces them.

**Search Term** — the canonicalized text someone searched for, and the key Demand is counted against. Not a Card: search is prefix-per-token, so `bolt` is one Search Term that matches many Cards, and a term that matched nothing has no Card at all.

**Demand** — how often a Search Term was asked for, split into hits and misses. Misses are the valuable half: a term asked for repeatedly that no Card List holds is a gap in the network's supply. Stored as counters per (Search Term, Scope), never as an event log.

**Trade Match** — a Card on one Deckbox User's wishlist that a *different* Deckbox User holds in their tradelist. Derived entirely from existing Card Lists; nothing is stored.
