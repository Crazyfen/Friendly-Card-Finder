# Graph Report - .  (2026-04-17)

## Corpus Check
- 16 files · ~14,385 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 178 nodes · 213 edges · 17 communities detected
- Extraction: 98% EXTRACTED · 2% INFERRED · 0% AMBIGUOUS · INFERRED: 5 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Community Hubs (Navigation)
- [[_COMMUNITY_Community 0|Community 0]]
- [[_COMMUNITY_Community 1|Community 1]]
- [[_COMMUNITY_Community 2|Community 2]]
- [[_COMMUNITY_Community 3|Community 3]]
- [[_COMMUNITY_Community 4|Community 4]]
- [[_COMMUNITY_Community 5|Community 5]]
- [[_COMMUNITY_Community 6|Community 6]]
- [[_COMMUNITY_Community 7|Community 7]]
- [[_COMMUNITY_Community 8|Community 8]]
- [[_COMMUNITY_Community 9|Community 9]]
- [[_COMMUNITY_Community 10|Community 10]]
- [[_COMMUNITY_Community 11|Community 11]]
- [[_COMMUNITY_Community 12|Community 12]]
- [[_COMMUNITY_Community 13|Community 13]]
- [[_COMMUNITY_Community 14|Community 14]]
- [[_COMMUNITY_Community 15|Community 15]]
- [[_COMMUNITY_Community 16|Community 16]]

## God Nodes (most connected - your core abstractions)
1. `SQLiteStorage` - 11 edges
2. `mockOwnerStorage` - 10 edges
3. `fakeStorage` - 10 edges
4. `FriendlyCardFinder` - 10 edges
5. `App` - 7 edges
6. `newTestDB()` - 7 edges
7. `Environment Configuration (.env)` - 7 edges
8. `internal/storage/sqlite Package` - 6 edges
9. `Deckbox Scraper (scrapper.go)` - 6 edges
10. `FTS5 Full-Text Search` - 5 edges

## Surprising Connections (you probably didn't know these)
- `FriendlyCardFinder` --references--> `SQLite Storage`  [EXTRACTED]
  README.md → README.md  _Bridges community 1 → community 7_

## Hyperedges (group relationships)
- **Card List Scraping and Storage Pipeline** — readme_scrapper, readme_batch_insert, readme_storage_sqlite_package, readme_sqlite [INFERRED 0.88]
- **SQLite Performance Optimization Stack** — readme_wal_mode, readme_dual_connections, readme_fts5, readme_card_lists_fts [INFERRED 0.85]
- **CI/CD Build and Deploy Flow** — readme_github_actions, readme_docker, readme_ghcr, readme_vps_deploy [EXTRACTED 1.00]

## Communities

### Community 0 - "Community 0"
Cohesion: 0.08
Nodes (5): fakeScraper, fakeStorage, TestSearchCardEmpty(), TestSearchCardGroupsByListId(), TestSearchCardSameCardAcrossLists()

### Community 1 - "Community 1"
Cohesion: 0.12
Nodes (23): BOT_TOKEN Env Var, Deckbox.org, internal/deckbox Package, DeckboxSaver Interface (mock), DECKBOX_SESSION_COOKIE Env Var, Docker / docker-compose, Environment Configuration (.env), ENV Var (log level) (+15 more)

### Community 2 - "Community 2"
Cohesion: 0.1
Nodes (12): BotUser, CardList, CardListOwnerInfo, CardListWithOwner, DeckboxSaver, DeckboxUser, MultiCardSearchResult, Scraper (+4 more)

### Community 3 - "Community 3"
Cohesion: 0.22
Nodes (6): buildFtsQueryTerm(), createFtsTable(), DB, New(), normalizeASCII(), SQLiteStorage

### Community 4 - "Community 4"
Cohesion: 0.18
Nodes (10): benchmarkSearchCard(), BenchmarkSearchCard_FTSDisabled(), BenchmarkSearchCard_FTSEnabled(), newTestDB(), TestClearCardList(), TestGetAllDeckboxUsersWithOldLists(), TestGetDeckboxUser(), TestGetOwnerByListId() (+2 more)

### Community 5 - "Community 5"
Cohesion: 0.14
Nodes (1): mockOwnerStorage

### Community 6 - "Community 6"
Cohesion: 0.23
Nodes (12): profileFetcher, refreshResult, SuggestDeckboxResult, GetProfileData(), NewUser(), RefreshStaleUserLists(), refreshUserListsWorker(), runUserRefreshWorkerPool() (+4 more)

### Community 7 - "Community 7"
Cohesion: 0.2
Nodes (12): Batch Insert Strategy (~1000 cards), CARD_LIST_BATCH_SIZE Env Var, card_lists_fts Virtual Table, Dual DB Connections (read/write split), FTS5 Full-Text Search, github.com/mattn/go-sqlite3 Driver, Rationale: Batch inserts avoid SQLite param limit, Rationale: Separate read/write connections for concurrency (+4 more)

### Community 8 - "Community 8"
Cohesion: 0.25
Nodes (4): App, main(), sendHTMLReply(), setupLogger()

### Community 9 - "Community 9"
Cohesion: 0.83
Nodes (3): scraperFromEnv(), TestCardListScrapper(), TestUserScrapper()

### Community 10 - "Community 10"
Cohesion: 0.67
Nodes (0): 

### Community 11 - "Community 11"
Cohesion: 0.67
Nodes (1): Config

### Community 12 - "Community 12"
Cohesion: 0.67
Nodes (0): 

### Community 13 - "Community 13"
Cohesion: 1.0
Nodes (1): CardSearchDTO

### Community 14 - "Community 14"
Cohesion: 1.0
Nodes (0): 

### Community 15 - "Community 15"
Cohesion: 1.0
Nodes (0): 

### Community 16 - "Community 16"
Cohesion: 1.0
Nodes (0): 

## Knowledge Gaps
- **26 isolated node(s):** `Config`, `BotUser`, `DeckboxUser`, `CardListOwnerInfo`, `CardListWithOwner` (+21 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **Thin community `Community 13`** (2 nodes): `CardSearchDTO`, `dto.go`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 14`** (2 nodes): `sl.go`, `Err()`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 15`** (2 nodes): `split.go`, `SplitMessage()`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 16`** (1 nodes): `storage.go`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `FriendlyCardFinder` connect `Community 1` to `Community 7`?**
  _High betweenness centrality (0.022) - this node is a cross-community bridge._
- **Why does `Environment Configuration (.env)` connect `Community 1` to `Community 7`?**
  _High betweenness centrality (0.011) - this node is a cross-community bridge._
- **What connects `Config`, `BotUser`, `DeckboxUser` to the rest of the system?**
  _26 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Community 0` be split into smaller, more focused modules?**
  _Cohesion score 0.08 - nodes in this community are weakly interconnected._
- **Should `Community 1` be split into smaller, more focused modules?**
  _Cohesion score 0.12 - nodes in this community are weakly interconnected._
- **Should `Community 2` be split into smaller, more focused modules?**
  _Cohesion score 0.1 - nodes in this community are weakly interconnected._
- **Should `Community 5` be split into smaller, more focused modules?**
  _Cohesion score 0.14 - nodes in this community are weakly interconnected._