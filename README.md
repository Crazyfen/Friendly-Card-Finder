# FriendlyCardFinder

FriendlyCardFinder — Telegram-бот для поиска карточек Magic: The Gathering по спискам пользователей Deckbox.org. Бот парсит профили Deckbox, сохраняет инвентари/трейдлисты/вишлисты в SQLite и отвечает на запросы поиска карточек в зарегистрированных списках.

**Ключевые возможности**

- Регистрация Telegram-пользователя с привязкой к логину Deckbox
- Асинхронная загрузка и периодическое обновление списков (inventory, tradelist, wishlist)
- Поиск карточек по именам с поддержкой больших коллекций (батчевые вставки в SQLite) и поиск в вишлистах через команду `/sell` — поддерживаются многострочные запросы (по одной карточке на строку)
- Поддержка FTS5 (если сборка SQLite с FTS5)

**Текущая структура проекта (важные пути)**

- `main.go` — точка входа, регистрация обработчиков Telegram
- `internal/deckbox/` — логика работы с Deckbox (scraper, handler, модели)
- `internal/storage/sqlite/` — реализация хранения в SQLite (батчинг, FTS)

**Требования**

- Go 1.20+ (рекомендуется последняя стабильная версия)
- SQLite (драйвер используется `github.com/mattn/go-sqlite3`)

**Переменные окружения (.env)**
Обязательно установить в окружении или в файле `.env` в корне проекта:

- `BOT_TOKEN` — токен Telegram-бота
- `STORAGE_PATH` — путь к файлу SQLite базы (например `./data/db.sqlite`)
- `DECKBOX_SESSION_COOKIE` — session cookie для доступа к Deckbox (нужен для интеграционных тестов и парсинга страниц)
- `ENV` — `local`/`dev`/`prod` (влияет на уровень логирования)

Дополнительные опции (необязательно):

- `FRESHNESS_TIME_LIMIT_HOURS` — время, после которого считаем данные "устаревшими" (по умолчанию в коде задаётся значение)
- `CARD_LIST_BATCH_SIZE` — размер батча при вставке карточек в БД (по умолчанию 1000)

Пример `.env`:

```
BOT_TOKEN=123456:ABCDEFG
STORAGE_PATH=./data/db.sqlite
DECKBOX_SESSION_COOKIE=your_cookie_here
ENV=local
```

**Сборка и запуск**

Сборка обычным способом:

```bash
go build ./...
./FriendlyCardFinder
```

Запуск без сборки (go run):

```bash
go run main.go
```

Если требуется поддержка FTS5 (для ускоренного поиска), соберите с тегом `fts5`:

```bash
# сборка с FTS5
go build -tags "fts5" ./...
go run -tags "fts5" main.go
```

**Тесты**

Запустить все тесты:

```bash
go test ./...
```

FTS-тесты (требуют сборки драйвера с поддержкой FTS5):

```bash
go test -tags "fts5" ./internal/storage/sqlite -v
```

Интеграционные тесты, которые взаимодействуют с Deckbox, требуют корректного `DECKBOX_SESSION_COOKIE` в окружении и доступа в сеть.

**Особенности реализации и советы для разработчиков**

- Хранение: используется отдельное подключение для записи (один поток) и отдельное для чтения (параллельно). В инициализации применяются PRAGMA: `WAL`, `synchronous = NORMAL`, `busy_timeout = 5000`.
- Батчинг: вставки карточек разбиваются на батчи (~1000 карточек) чтобы не превышать лимит параметров SQLite.
- FTS5: при наличии поддержки создаётся виртуальная таблица `card_lists_fts` и поддерживаются нормализованные токены для поиска без диакритики.
- Парсер Deckbox (`internal/deckbox/scrapper.go`) использует session cookie для доступа к приватным данным и нуждается в периодическом обновлении cookie.

**Разработка новых команд**

- Добавьте обработчик в `main.go` и реализуйте бизнес-логику в `internal/deckbox/handler.go`.
- Для тестирования асинхронных потоков используйте мок `DeckboxSaver` или синхронизацию (channels/waitgroups).

**Запуск через Docker**

Собрать образ локально:

```bash
docker build -t friendlycardfinder .
```

Запустить с `docker compose` (требует `.env` в текущей директории):

```bash
docker compose up -d
```

**CI/CD (GitHub Actions)**

При каждом пуше в ветку `main` автоматически выполняются:

1. `go test -tags fts5 -race ./...`
2. Сборка Docker-образа и публикация в GitHub Container Registry (`ghcr.io/crazyfen/friendlycardfinder`)
3. Деплой на VPS: копирование `docker-compose.yml` по SCP, затем `docker compose pull && up -d`

Необходимые GitHub Secrets:

| Secret            | Описание                                     |
| ----------------- | -------------------------------------------- |
| `SSH_HOST`        | IP или домен VPS                             |
| `SSH_USER`        | Пользователь SSH                             |
| `SSH_PRIVATE_KEY` | Приватный SSH-ключ (полное содержимое файла) |

`.env` на VPS с `BOT_TOKEN`, `STORAGE_PATH`, `DECKBOX_SESSION_COOKIE`, `ENV` необходимо создать вручную один раз в директории `~/friendlycardfinder/`.

**Контакты и вклад**

- Pull requests и issue приветствуются. Пожалуйста, добавляйте тесты для новых фич и следуйте текущему стилю кода.

---

Автор: FriendlyCardFinder — утилита для обмена коллекциями и быстрого поиска карточек через Telegram.
