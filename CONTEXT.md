# Domain language

The names this project uses for its own concepts. Architecture and conventions live in [`CLAUDE.md`](CLAUDE.md); this file is only the glossary.

**Bot User** — one chat account that registered with the bot via `/deckbox <login>`, on one **Platform**. Carries the Platform, that account's ID and handle, and the Deckbox login it claimed. A Deckbox login has at most one Bot User per Platform; a Telegram claimant and a Discord claimant of the same login are independent registrations, not linked, and neither verifies the other. Not every list owner is a Bot User; unregistered owners show up in results without a contact.

**Contact** — how a reader can reach a list owner: one per Bot User claiming the owner's Deckbox login. A search result shows every Contact the owner has, the reader's own Platform first; a Contact from the other Platform is shown in the form that still works from the reader's side, or left out if no such form exists.

**Platform** — a chat service the bot answers on: Telegram or Discord. Every Command behaves the same on both; only the delivery and markup differ.

**Deckbox User** — a profile on deckbox.org, identified by its login. Owns up to three Card Lists and carries the `updated_at` timestamp that drives refresh.

**Card List** — one of a Deckbox User's three lists (inventory, tradelist, wishlist), identified by its Deckbox `listId`. Holds card name → quantity. `BodyHash` is the hash of the export it was parsed from, used to skip unchanged saves.

**Private Card List** — a Card List its owner hid on Deckbox: the profile stops showing it, or its export answers "not found". The bot holds no copy of a Private Card List — its cards are deleted at the Refresh that notices, and the Refresh still succeeds. Deckbox being unreachable is not privacy: a failed fetch keeps the last copy. If the owner makes it public again, the next Refresh reads it as new.

**Quantity** — how many copies of a card name a Card List holds, summed across every printing of it. The export page carries no variation data (set, printing, foil), so a name appearing on several lines is the same card in different variations and the lines are added together. This is deliberate: the quantities are not duplicates to be de-duplicated, and there is no per-variation number to recover.

**Scope** — which of the three Card Lists a search runs against: `tradelist` (plain text search), `wishlist` (`/sell`), or `inventory`. A named type, not a bare string, because both the storage column and the Telegram wording default to the tradelist when they do not recognise the value.

**Command** — one thing a Bot User can ask for, as a method on `chat.Commands` taking a Request and returning Replies. The same Command serves every Platform. The Command owns the wording, the missing-argument case and the Scope; each Platform's adapter owns only the SDK calls that carry them.

**Request** — one incoming message reduced to what a Command needs: the text with any command prefix stripped, the detected language, the Platform, and who sent it from where. A Discord message with no locale (a DM or mention, as opposed to a slash command) is English.

**Reply** — one message to send back, as platform-neutral structure (text, emphasis, links, Contacts) that each Platform formats in its own markup, and whether it quotes the message that triggered it. A Command returns these rather than sending, which is what makes every Command reachable from a test.

**Query** — one parsed search line: the card name with quotes stripped, whether the user asked for an exact match, and the Scope. Built by `ParseQuery`.

**Exact match** — a query the user wrapped in quotes. Equality ignores case, accents and punctuation: `"vitu ghazi"` matches *Vitu-Ghazi, the City-Tree*. Without quotes, search is prefix-based per token.

**Session** — the Deckbox `_tcg_session` cookie the scraper authenticates with, and the rules around it: where it may come from, when it is replaced, and that one rejection causes one login however many workers noticed it.

**Refresh** — re-scraping a Deckbox User's profile and all three Card Lists, then stamping `updated_at`. There is exactly one implementation (`Deckbox.refreshUser`); registration, the background ticker and `/suggestdeckbox` all go through it. A Refresh succeeds when the profile was read and stored — an empty collection is a legitimate answer, not a failure. Only a Deckbox User who could not be read at all stays stale. A Card List found to be private is deleted, not counted as a failure.

**Suggest** — the `/suggestdeckbox` bulk Refresh, which skips any Deckbox User whose data is still fresh.

**Aggregate** — one Card List's contribution to a search result: every queried card it holds, plus the counts search results are ranked by (distinct cards matched, then total quantity).

**Admin Panel** — the web surface at `/` (manage) and `/insights` (analytics), served by the bot process behind Caddy. Its audience is the operator, not Bot Users, so its wording is hardcoded English rather than going through `i18n`.

**Purge** — removing a Deckbox User completely: their three Card Lists, every card row, the FTS rows, the `deckbox_users` row, and every Bot User claiming that login, on every Platform. The login is then unknown to the bot until someone registers it again.

**Unlink** — removing one Bot User — one Platform's claim on a login — leaving the Deckbox User, its Card Lists and any claim from the other Platform in place. For when someone's chat account changes but their Deckbox login does not, or a stranger claimed the login on one Platform.

**Orphan Registration** — a Bot User whose `deckboxLogin` has no Deckbox User: a login claimed via `/deckbox` that never produced a collection, usually a typo. Invisible to search, so the Admin Panel surfaces them.

**Search Term** — the canonicalized text someone searched for, and the key Demand is counted against. Not a Card: search is prefix-per-token, so `bolt` is one Search Term that matches many Cards, and a term that matched nothing has no Card at all.

**Demand** — how often a Search Term was asked for, split into hits and misses. Misses are the valuable half: a term asked for repeatedly that no Card List holds is a gap in the network's supply. Stored as counters per (Search Term, Scope), never as an event log.

**Trade Match** — a Card on one Deckbox User's wishlist that a *different* Deckbox User holds in their tradelist. Derived entirely from existing Card Lists; nothing is stored.
