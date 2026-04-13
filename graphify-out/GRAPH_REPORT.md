# Graph Report - .  (2026-04-12)

## Corpus Check
- Corpus is ~12,077 words - fits in a single context window. You may not need a graph.

## Summary
- 168 nodes · 208 edges · 16 communities detected
- Extraction: 98% EXTRACTED · 2% INFERRED · 0% AMBIGUOUS · INFERRED: 5 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Community Hubs (Navigation)
- [[_COMMUNITY_Test Mocks and Fakes|Test Mocks and Fakes]]
- [[_COMMUNITY_Deployment and Configuration|Deployment and Configuration]]
- [[_COMMUNITY_SQLite Storage Layer|SQLite Storage Layer]]
- [[_COMMUNITY_Domain Types and Interfaces|Domain Types and Interfaces]]
- [[_COMMUNITY_Storage Tests and Benchmarks|Storage Tests and Benchmarks]]
- [[_COMMUNITY_Storage Mock Implementations|Storage Mock Implementations]]
- [[_COMMUNITY_User Refresh Worker Pool|User Refresh Worker Pool]]
- [[_COMMUNITY_SQLite Performance Design|SQLite Performance Design]]
- [[_COMMUNITY_Bot Entry and Handlers|Bot Entry and Handlers]]
- [[_COMMUNITY_Scraper Integration Tests|Scraper Integration Tests]]
- [[_COMMUNITY_Message Utility Tests|Message Utility Tests]]
- [[_COMMUNITY_App Configuration|App Configuration]]
- [[_COMMUNITY_Internationalization|Internationalization]]
- [[_COMMUNITY_Search Data Transfer|Search Data Transfer]]
- [[_COMMUNITY_Error Logging Helper|Error Logging Helper]]
- [[_COMMUNITY_Storage Interface|Storage Interface]]

## God Nodes (most connected - your core abstractions)
1. `SQLiteStorage` - 11 edges
2. `mockOwnerStorage` - 10 edges
3. `fakeStorage` - 10 edges
4. `FriendlyCardFinder` - 10 edges
5. `newTestDB()` - 7 edges
6. `Environment Configuration (.env)` - 7 edges
7. `internal/storage/sqlite Package` - 6 edges
8. `Deckbox Scraper (scrapper.go)` - 6 edges
9. `New()` - 5 edges
10. `FTS5 Full-Text Search` - 5 edges

## Surprising Connections (you probably didn't know these)
- `FriendlyCardFinder` --references--> `SQLite Storage`  [EXTRACTED]
  README.md → README.md  _Bridges community 1 → community 7_

## Hyperedges (group relationships)
- **Card List Scraping and Storage Pipeline** — readme_scrapper, readme_batch_insert, readme_storage_sqlite_package, readme_sqlite [INFERRED 0.88]
- **SQLite Performance Optimization Stack** — readme_wal_mode, readme_dual_connections, readme_fts5, readme_card_lists_fts [INFERRED 0.85]
- **CI/CD Build and Deploy Flow** — readme_github_actions, readme_docker, readme_ghcr, readme_vps_deploy [EXTRACTED 1.00]

## Communities

### Community 0 - "Test Mocks and Fakes"
Cohesion: 0.09
Nodes (5): fakeScraper, fakeStorage, TestSearchCardEmpty(), TestSearchCardGroupsByListId(), TestSearchCardSameCardAcrossLists()

### Community 1 - "Deployment and Configuration"
Cohesion: 0.12
Nodes (23): BOT_TOKEN Env Var, Deckbox.org, internal/deckbox Package, DeckboxSaver Interface (mock), DECKBOX_SESSION_COOKIE Env Var, Docker / docker-compose, Environment Configuration (.env), ENV Var (log level) (+15 more)

### Community 2 - "SQLite Storage Layer"
Cohesion: 0.21
Nodes (9): buildFtsQueryTerm(), createCardListsTable(), createDeckboxUsersTable(), createFtsTable(), createUsersTable(), DB, New(), normalizeASCII() (+1 more)

### Community 3 - "Domain Types and Interfaces"
Cohesion: 0.12
Nodes (8): BotUser, CardList, CardListOwnerInfo, DeckboxSaver, DeckboxUser, Scraper, SearchCardResult, extractIDFromElement()

### Community 4 - "Storage Tests and Benchmarks"
Cohesion: 0.18
Nodes (10): benchmarkSearchCard(), BenchmarkSearchCard_FTSDisabled(), BenchmarkSearchCard_FTSEnabled(), newTestDB(), TestClearCardList(), TestGetAllDeckboxUsersWithOldLists(), TestGetDeckboxUser(), TestGetOwnerByListId() (+2 more)

### Community 5 - "Storage Mock Implementations"
Cohesion: 0.14
Nodes (1): mockOwnerStorage

### Community 6 - "User Refresh Worker Pool"
Cohesion: 0.26
Nodes (10): profileFetcher, SuggestDeckboxResult, commandArguments(), GetProfileData(), NewUser(), RefreshStaleUserLists(), refreshUserListsWorker(), runUserRefreshWorkerPool() (+2 more)

### Community 7 - "SQLite Performance Design"
Cohesion: 0.2
Nodes (12): Batch Insert Strategy (~1000 cards), CARD_LIST_BATCH_SIZE Env Var, card_lists_fts Virtual Table, Dual DB Connections (read/write split), FTS5 Full-Text Search, github.com/mattn/go-sqlite3 Driver, Rationale: Batch inserts avoid SQLite param limit, Rationale: Separate read/write connections for concurrency (+4 more)

### Community 8 - "Bot Entry and Handlers"
Cohesion: 0.27
Nodes (6): contextKey, defaultHandler(), main(), sellHandler(), setupLogger(), splitMessage()

### Community 9 - "Scraper Integration Tests"
Cohesion: 0.83
Nodes (3): scraperFromEnv(), TestCardListScrapper(), TestUserScrapper()

### Community 10 - "Message Utility Tests"
Cohesion: 0.67
Nodes (0): 

### Community 11 - "App Configuration"
Cohesion: 0.67
Nodes (1): Config

### Community 12 - "Internationalization"
Cohesion: 0.67
Nodes (0): 

### Community 13 - "Search Data Transfer"
Cohesion: 1.0
Nodes (1): CardSearchDTO

### Community 14 - "Error Logging Helper"
Cohesion: 1.0
Nodes (0): 

### Community 15 - "Storage Interface"
Cohesion: 1.0
Nodes (0): 

## Knowledge Gaps
- **24 isolated node(s):** `contextKey`, `Config`, `BotUser`, `DeckboxUser`, `CardListOwnerInfo` (+19 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **Thin community `Search Data Transfer`** (2 nodes): `CardSearchDTO`, `dto.go`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Error Logging Helper`** (2 nodes): `sl.go`, `Err()`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Storage Interface`** (1 nodes): `storage.go`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `FriendlyCardFinder` connect `Deployment and Configuration` to `SQLite Performance Design`?**
  _High betweenness centrality (0.025) - this node is a cross-community bridge._
- **Why does `Environment Configuration (.env)` connect `Deployment and Configuration` to `SQLite Performance Design`?**
  _High betweenness centrality (0.012) - this node is a cross-community bridge._
- **What connects `contextKey`, `Config`, `BotUser` to the rest of the system?**
  _24 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Test Mocks and Fakes` be split into smaller, more focused modules?**
  _Cohesion score 0.09 - nodes in this community are weakly interconnected._
- **Should `Deployment and Configuration` be split into smaller, more focused modules?**
  _Cohesion score 0.12 - nodes in this community are weakly interconnected._
- **Should `Domain Types and Interfaces` be split into smaller, more focused modules?**
  _Cohesion score 0.12 - nodes in this community are weakly interconnected._
- **Should `Storage Mock Implementations` be split into smaller, more focused modules?**
  _Cohesion score 0.14 - nodes in this community are weakly interconnected._