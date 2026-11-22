# Live MCP for 1C

`Live MCP for 1C` — самостоятельный read-only MCP-сервер по stdio поверх
HTTP-расширения 1С `AIDataRead`. MCP-клиент вызывает шесть инструментов,
сервер отправляет запросы по HTTP с Basic-аутентификацией, расширение
проверяет запрос и выполняет только разрешённое чтение данных.

В релизе `v0.2.0` два взаимозаменяемых stdio-сервера:

- **Go** — основной runtime релиза и дальнейшей разработки, два готовых
  бинарника: Linux x64 и Windows x64;
- **Python** — сохранённый переходный runtime для отката; функционально не
  менялся, его пакет и `serverInfo.version` остаются `0.1.0`.

Оба сервера используют один HTTP-контракт `AIDataRead` и один набор настроек
одного подключения. Расширение `AIDataRead` в релизе — `0.2.0`; оно остаётся
общим для последующих Go-релизов.

`v0.2.0` — последний релиз с Python. Начиная со следующего релиза
поставляется только Go-сервер; Python-пакет и его `0.1.0` нужны только для
перехода и отката в этом релизе.

## Руководства Word для v0.2.0

- [AIDataRead — справочник HTTP-сервиса для разработчика 1С (.docx)](docs/ai-data-read-v0.2.0-ru.docx): установка CFE, публикация, права, поля и примеры HTTP-вызовов.
- [Live MCP (Go) — руководство пользователя Codex CLI (.docx)](docs/live-mcp-go-v0.2.0-ru.docx): установка на Linux и Windows x64, проверка SHA-256, настройка stdio и проверка подключения.

## 1. Версии, состав и границы

| Компонент | Версия и состав |
|---|---|
| Live MCP (Go) | `0.2.0`; `live-mcp-for-1c-linux-amd64`, `live-mcp-for-1c-windows-amd64.exe` |
| Live MCP (Python, переходный) | `0.1.0`; исходники `src/live_mcp_for_1c/`, console script `live-mcp-for-1c` |
| AIDataRead | `0.2.0`; исходники `extensions/http-read/src/`, собранный `AIDataRead.cfe` |
| MCP | шесть инструментов, MCP-транспорт stdio, без CLI-подкоманд |
| Лицензия | MIT, [`LICENSE`](LICENSE) |

Разные номера — согласованная переходная метка, а не два разных продукта:
Go-сервер имеет версию релиза `0.2.0`, Python-пакет остаётся `0.1.0` без
функциональных изменений. `ping_service` и `get_service_info` возвращают
версию **AIDataRead**, а не версию MCP-сервера; не путайте эти поля.

Публичный состав релиза: исходники Go (`go/`), исходники Python
(`src/live_mcp_for_1c/`), исходники расширения (`extensions/http-read/src/`),
нейтральный пример `examples/dcs-neutral.xml`, этот README, `LICENSE` и
собранные вложения: два Go-бинарника, `AIDataRead.cfe` и `SHA256SUMS`.

Назначение — только чтение. Обычная аутентификация и права 1С, RLS и
сохранность данных сохраняются. В runtime нет записи данных, произвольного
BSL, файловых маршрутов, административных/startup-действий, COM, OData,
профилей и скрытого fallback.

## 2. Быстрый старт: Go-бинарник

Скачайте вложения релиза `v0.2.0`:
`live-mcp-for-1c-linux-amd64`, `live-mcp-for-1c-windows-amd64.exe`,
`AIDataRead.cfe` и `SHA256SUMS`. Сверьте SHA-256 скачанного бинарника со
строкой в `SHA256SUMS`.

Linux x64:

```bash
set -euo pipefail
work=$(mktemp -d "${TMPDIR:-/tmp}/live-mcp-go.XXXXXX") || {
  echo "STOP: cannot create a fresh download directory" >&2
  exit 1
}
cd "$work" || exit 1
base="https://github.com/vlikhobabin/live-mcp-public/releases/download/v0.2.0"
curl --fail --location --remote-name "$base/live-mcp-for-1c-linux-amd64"
curl --fail --location --remote-name "$base/SHA256SUMS"

# Ровно одна строка с корректным SHA и именем; иначе — STOP.
count=$(grep -Ec '^[0-9a-f]{64} [ *]live-mcp-for-1c-linux-amd64$' SHA256SUMS || true)
[ "$count" -eq 1 ] || {
  echo "STOP: expected exactly one SHA256SUMS line for linux-amd64, got $count" >&2
  exit 1
}
grep -E '^[0-9a-f]{64} [ *]live-mcp-for-1c-linux-amd64$' SHA256SUMS | sha256sum --strict -c -

# chmod только после успешной проверки SHA.
chmod +x live-mcp-for-1c-linux-amd64
printf 'OK: %s\n' "$work/live-mcp-for-1c-linux-amd64"
```

Windows x64 (PowerShell):

```powershell
$ErrorActionPreference = "Stop"
$work = Join-Path ([System.IO.Path]::GetTempPath()) ("live-mcp-v020-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $work | Out-Null
Set-Location $work
$base = "https://github.com/vlikhobabin/live-mcp-public/releases/download/v0.2.0"
Invoke-WebRequest "$base/live-mcp-for-1c-windows-amd64.exe" -OutFile live-mcp-for-1c-windows-amd64.exe
Invoke-WebRequest "$base/SHA256SUMS" -OutFile SHA256SUMS

$shaLines = @(Select-String -Path .\SHA256SUMS -Pattern '^[0-9a-fA-F]{64} [ *]live-mcp-for-1c-windows-amd64\.exe\r?$')
if ($shaLines.Count -ne 1) {
    throw "STOP: expected exactly one SHA256SUMS line for windows-amd64, got $($shaLines.Count)"
}
$expected = ($shaLines[0].Line -split '\s+')[0].ToUpperInvariant()
$actual = (Get-FileHash .\live-mcp-for-1c-windows-amd64.exe -Algorithm SHA256).Hash.ToUpperInvariant()
if ($actual -ne $expected) {
    throw "STOP: SHA256 mismatch for live-mcp-for-1c-windows-amd64.exe"
}
Write-Output "OK: $work\live-mcp-for-1c-windows-amd64.exe"
```

Только после `OK` указывайте абсолютный путь к `.exe` в MCP-клиенте; до
проверки SHA-256 не запускайте и не добавляйте файл в конфигурацию.

Бинарник не подписан; SHA-256 подтверждает целостность файла, но не заменяет
проверку источника загрузки. `SHA256SUMS` содержит и второй бинарник, и CFE;
на Linux проверяйте только свою строку, иначе `sha256sum -c` сообщит об
отсутствующем Windows-файле.

Запускать бинарник вручную в терминале не нужно: это stdio-сервер, его
запускает MCP-клиент. stdout занят MCP-протоколом, диагностика идёт в stderr;
без трёх обязательных настроек процесс завершается с кодом `1` до сетевого
обращения. Аргументы не используются.

## 3. Сборка Go из исходников

Нужен Go `1.25+` и доступ к модулям, указанным в `go/go.mod`. Python, 1С и
сетевой сервис для сборки не требуются. Сторонние лицензии Go-зависимостей —
[`go/THIRD-PARTY-NOTICES.txt`](go/THIRD-PARTY-NOTICES.txt).

```bash
cd go
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -buildvcs=false \
  -o ../live-mcp-for-1c-linux-amd64 ./cmd/live-mcp-for-1c
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -trimpath -buildvcs=false \
  -o ../live-mcp-for-1c-windows-amd64.exe ./cmd/live-mcp-for-1c
```

Кросс-сборка не является запуском под целевой ОС. Принятый ранее Go-порт
проверялся нативно на Linux x64 и Windows x64, включая шесть инструментов и
XLSX; эта проверка относится к порту до релизной смены версии
`0.1.0 → 0.2.0`. Новые релизные файлы `v0.2.0` повторно нативно на Windows не
запускались. Отдельный Windows-прогон без установленного Python пропущен по
решению оператора и не считается PASS; сервер Python не требует, но этот
конкретный прогон не заявляется.

## 4. Переходный Python: установка и откат

Python-путь нужен для перехода и отката, если вы уже использовали Python
`live-mcp-for-1c`. Версия пакета остаётся `0.1.0`; при подготовке переходного
релиза функциональность Python-сервера не менялась. Установка — из исходников,
готового Python-бинарника или wheel в релизе нет.

```bash
python3.12 -m venv "$HOME/.venvs/live-mcp-for-1c"
"$HOME/.venvs/live-mcp-for-1c/bin/python" -m pip install /path/to/live-mcp-for-1c
"$HOME/.venvs/live-mcp-for-1c/bin/python" -c "import live_mcp_for_1c"
"$HOME/.venvs/live-mcp-for-1c/bin/live-mcp-for-1c"
```

`/path/to/live-mcp-for-1c` — каталог этого репозитория (или распакованного
исходного состава). Зависимости — `httpx` и `mcp`; pip ставит их из
настроенного индекса. Последняя команда — stdio-сервер, её запускает
MCP-клиент, а не человек.

Команды выше — Unix. Python на Windows не является проверенным контуром
этого релиза; отдельные Windows-команды venv/pip здесь намеренно не
обещаются. Для Windows используйте Go-бинарник.

Откат с Go на Python:

1. Остановите MCP-сессию, которая запускала Go-бинарник.
2. В записи MCP-клиента замените `command` на абсолютный путь
   Python console script `.../bin/live-mcp-for-1c`, сохранив `args`, `env` и
   `cwd`.
3. Запустите новую сессию и проверьте `ping_service`.
4. Go-бинарник можно удалить; Python-venv можно удалить после ненужности:
   `python -m pip uninstall live-mcp-for-1c` или удалить каталог venv.

Расширение `AIDataRead`, его публикация и HTTP-контракт при откате не
меняются: оба сервера ходят в один и тот же сервис.

**Ограничение Python на Windows.** Запись XLSX в Python использует POSIX
`dir_fd`/`O_DIRECTORY`/`O_NOFOLLOW`; на Windows запрошенный XLSX вернёт
диагностику `artifact_persistence_unavailable` без `localPath`. Остальные
инструменты Python на Windows в этом релизе не проверялись. Для Windows
используйте Go-бинарник: его XLSX-путь не требует POSIX `dir_fd` (проверка —
§9).

## 5. Настройки одного подключения

Оба runtime читают только process environment. `.env` не загружается
автоматически, списка соединений, профилей и compatibility aliases нет.

| Переменная | Обяз. | Значение |
|---|---|---|
| `LIVE_MCP_FOR_1C_HTTP_SERVICE_BASE_URL` | да | база публикации вместе с корнем сервиса, без завершающего `/`: `https://host/base/hs/ai-data-read` |
| `LIVE_MCP_FOR_1C_HTTP_SERVICE_USERNAME` | да | пользователь ИБ для HTTP Basic |
| `LIVE_MCP_FOR_1C_HTTP_SERVICE_PASSWORD` | да | секрет; только env или secret store |
| `LIVE_MCP_FOR_1C_VERIFY_TLS` | нет | проверка TLS, default `true`; `false` — только локальный диагностический контур |
| `LIVE_MCP_FOR_1C_HTTP_TIMEOUT_SECONDS` | нет | таймаут ожидания HTTP, default `60`; обрывает только чтение, не серверное выполнение |
| `LIVE_MCP_FOR_1C_CACHE_ROOT` | нет | абсолютный каталог XLSX (`dcs-artifacts/`); default `~/.cache/live-mcp-for-1c` |

HTTP: Basic auth на каждом запросе, TLS проверяется по умолчанию, редиректы
не следуются, env-proxy не используются, retry нет. `CACHE_ROOT` при явном
задании должен быть абсолютным; `~` не раскрывается. Каталог создаётся при
первом XLSX, а не при старте.

### Конфигурация MCP-клиента

Пароль не включайте в общий JSON. MCP-хост должен добавить его в env дочернего
процесса из своего secret store или локального приватного файла (не Git, не
облачная синхронизация). Пример для Go на Linux:

```json
{
  "mcpServers": {
    "live-mcp": {
      "command": "/absolute/path/live-mcp-for-1c-linux-amd64",
      "args": [],
      "env": {
        "LIVE_MCP_FOR_1C_HTTP_SERVICE_BASE_URL": "https://1c.example/base/hs/ai-data-read",
        "LIVE_MCP_FOR_1C_HTTP_SERVICE_USERNAME": "read_user"
      }
    }
  }
}
```

Для Windows `command` — абсолютный путь к `.exe`. Для Python — абсолютный путь
к `.../bin/live-mcp-for-1c` (Windows:
`...\Scripts\live-mcp-for-1c.exe`). Если хост не умеет подставлять секрет,
используйте локальную приватную обёртку, которая экспортирует env и запускает
сервер; не публикуйте её и не коммитьте.

## 6. Первый запрос

MCP-клиент выполняет `initialize` и `list_tools` сам; должны обнаружиться
шесть инструментов:

`validate_1c_query`, `execute_1c_query`, `execute_dcs_schema`,
`render_dcs_schema`, `ping_service`, `get_service_info`.

Проверьте сервис:

- `ping_service` → `success:true`, `service:"ai-data-read"`, `status:"ok"`,
  `version:"0.2.0"`;
- `get_service_info` → `version`, `specVersion`, `platformVersion`,
  `endpoints`, `auth`, `limits`, `admission`, `capabilities`; `endpoints`
  содержит шесть HTTP-маршрутов, `capabilities.query` и `capabilities.dcs` —
  `true`.

Нейтральный query-пример:

```json
{
  "query_text": "ВЫБРАТЬ РАЗРЕШЕННЫЕ 10 * &K КАК Значение",
  "parameters": [{"name": "K", "valueType": "number", "value": 2}]
}
```

Сначала `validate_1c_query` с этим телом: ожидается
`validationStatus:"passed"` (у validate нет `output_format`). Затем
`execute_1c_query` с тем же телом: ожидается `success:true`,
`result.rowCount:1`, `result.truncated:false`, `result.rows[0].Значение == 20`.

Безопасный отказ: тот же validate без `РАЗРЕШЕННЫЕ` возвращает
`success:false`, `code:"allowed_select_required"`. Блокировки
(`ДЛЯ ИЗМЕНЕНИЯ`/`FOR UPDATE`) запрещены (`lock_not_allowed`), обходить
отказ записью нельзя. HTTP 400 — это wire-ответ сервиса, а не отдельный
MCP-протокол; MCP получает JSON-конверт сервиса.

### СКД и XLSX

Публичный пример — `examples/dcs-neutral.xml`. Схема использует только
литералы и параметр `K`. Для `execute_dcs_schema` передайте её содержимое в
`schema_xml`, типизированный `K` и `output_format`:

```json
{
  "schema_xml": "<содержимое examples/dcs-neutral.xml>",
  "parameters": [{"name": "K", "valueType": "number", "value": 2}],
  "output_format": "json"
}
```

Детальные строки дают `A/20`, `A/30`, `B/14`, групповые итоги — `A/50`,
`B/14`, общий итог — `64`. Не складывайте строки разных уровней: это даст
`192`, а не `64`.

У `render_dcs_schema` обязателен `report_id`. Для XLSX:

```json
{
  "report_id": "neutral",
  "schema_xml": "<содержимое examples/dcs-neutral.xml>",
  "parameters": [{"name": "K", "valueType": "number", "value": 2}],
  "output": {"format": "json", "artifacts": ["xlsx"]}
}
```

Успешный ответ содержит `tableDocument`, `previewMarkdown` и `artifacts`;
у XLSX проверяются `sizeBytes` и `sha256` (Base64 от SHA-256 декодированных
байтов), после чего файл сохраняется в `CACHE_ROOT/dcs-artifacts/` под
сгенерированным именем. Существующий файл не перезаписывается. `localPath` —
путь на хосте MCP-клиента, не поле HTTP-сервиса.

## 7. Расширение AIDataRead: сборка, применимость, публикация

Готовый `AIDataRead.cfe` приложен к релизу `v0.2.0`; его SHA-256 есть в
`SHA256SUMS`. Сверяйте файл перед загрузкой в Конфигуратор. Собранный CFE
соответствует исходникам `extensions/http-read/src/`; `get_service_info`
показывает объявленную версию `0.2.0`, но не идентифицирует точный файл CFE —
для идентичности используйте SHA-256.

### Сборка CFE из исходников

Схема первой публикации (`v0.1.0`): новая пустая файловая ИБ →
`LoadConfigFromFiles` → `CheckConfig` → чтение отчёта проверки → только после
этого `DumpCfg`. Сборка не требует применения к рабочей базе. Не используйте
`/S`, существующие или серверные базы. Пароль в командную строку не
передавайте. Команды выполняются из корня checkout; пути ниже — ваши, в
репозитории их нет.

Подготовка, import и check (один shell):

```bash
V8=/path/to/1cv8
IB=/path/to/new-empty-file-infobase
SRC="$PWD/extensions/http-read/src"
OUT="$PWD/dist/AIDataRead.cfe"
LOGS=$(mktemp -d "${TMPDIR:-/tmp}/aidataread-cfe.XXXXXX") || {
  echo "STOP: cannot create private log dir under ${TMPDIR:-/tmp}" >&2
  exit 1
}
printf 'private logs: %s\n' "$LOGS"

# Новый путь ИБ и отсутствие stale/символического OUT — обязательны.
[ ! -L dist ] || { echo "STOP: dist is a symlink; use a real directory" >&2; exit 1; }
[ ! -e "$IB" ] && [ ! -L "$IB" ] || { echo "STOP: IB path must be new: $IB" >&2; exit 1; }
[ ! -e "$OUT" ] && [ ! -L "$OUT" ] || { echo "STOP: refusing stale OUT: $OUT" >&2; exit 1; }
mkdir -p "$(dirname "$OUT")" || { echo "STOP: cannot create output dir" >&2; exit 1; }

"$V8" CREATEINFOBASE "File=\"$IB\";"
rc=$?; [ "$rc" -eq 0 ] || { echo "STOP: CREATEINFOBASE rc=$rc" >&2; exit 1; }

"$V8" DESIGNER /F "$IB" /LoadConfigFromFiles "$SRC" \
  -Extension AIDataRead /Out "$LOGS/import.log"
rc=$?; [ "$rc" -eq 0 ] || { echo "STOP: import rc=$rc; read $LOGS/import.log" >&2; exit 1; }

"$V8" DESIGNER /F "$IB" /CheckConfig \
  -ConfigLogIntegrity -IncorrectReferences -Server -HandlersExistence \
  -Extension AIDataRead /Out "$LOGS/check.log"
rc=$?; [ "$rc" -eq 0 ] || { echo "STOP: check rc=$rc; read $LOGS/check.log" >&2; exit 1; }
[ -s "$LOGS/check.log" ] || { echo "STOP: check.log is missing or empty; no explicit success" >&2; exit 1; }
grep -q 'Ошибок не обнаружено' "$LOGS/check.log" || {
  echo "STOP: check.log has no explicit success marker 'Ошибок не обнаружено'; read it" >&2
  exit 1
}
```

Прочитайте `import.log` и `check.log`: `rc=0` и непустой лог сами по себе не
являются явным успехом. На проверенной русской платформе успешный
`CheckConfig` пишет `Ошибок не обнаружено`; это и есть обязательный маркер.
Если его нет, dump запрещён; любая ошибка или непонятная диагностика — STOP.
На проверенной платформе `LoadConfigFromFiles` создал расширение `AIDataRead`
сам; если ваша платформа требует именованное расширение, создайте
`AIDataRead` в этой же временной ИБ и повторите загрузку. `UpdateDBCfg`,
публикация и native-запуск в сборку не входят.

Dump — отдельный шаг, только после успешных CREATE/Load/Check, чтения
`check.log` и подтверждения явного успеха (в том же shell):

```bash
[ -s "$LOGS/check.log" ] && grep -q 'Ошибок не обнаружено' "$LOGS/check.log" || {
  echo "STOP: no fresh check.log with explicit success; run CheckConfig first" >&2
  exit 1
}
[ ! -e "$OUT" ] && [ ! -L "$OUT" ] || { echo "STOP: refusing stale OUT: $OUT" >&2; exit 1; }
"$V8" DESIGNER /F "$IB" /DumpCfg "$OUT" -Extension AIDataRead /Out "$LOGS/dump.log"
rc=$?; [ "$rc" -eq 0 ] || { echo "STOP: DumpCfg rc=$rc; read $LOGS/dump.log" >&2; exit 1; }
[ -f "$OUT" ] && [ -s "$OUT" ] && [ ! -L "$OUT" ] || { echo "STOP: bad output: $OUT" >&2; exit 1; }
sha256sum "$OUT"
```

Успешный dump — только свежий непустой обычный файл, созданный этим запуском.
Если файл уже был или является symlink, блок останавливается до `DumpCfg`.
Логи остаются в `$LOGS`; не публикуйте их.

### Применимость и установка

1. В **отдельной тестовой копии** целевой ИБ загрузите `AIDataRead.cfe`
   («Расширения конфигурации» → «Загрузить из файла»).
2. Выполните штатную проверку применимости:
   `CheckCanApplyConfigurationExtensions -Extension AIDataRead`. Только если
   проверка успешна и не сообщает о несовместимости, примените расширение и
   обновите конфигурацию БД (`UpdateDBCfg` или «Обновить конфигурацию»).
3. Флаги обхода проверок не используйте. Если проверка не прошла, устраните
   причину; `CheckCanApplyConfigurationExtensions` не является условием
   `DumpCfg`.
4. После публикации проверьте `GET /ping/` и `GET /info/`. `get_service_info`
   показывает `version:"0.2.0"` и шесть маршрутов; это не заменяет проверку
   применимости и ваших сценариев.

`Language.Русский` — заимствованный объект, сопоставляемый с базовой
конфигурацией по точному имени `Русский`; одного кода `ru` недостаточно.
Совместимость с «любой конфигурацией» не заявляется. В прежней локальной
сборке на пустой ИБ `CheckCanApplyConfigurationExtensions` не прошла по
`РежимСовместимостиИнтерфейса` (`TaxiEnableVersion8_2`); флаги обхода не
использовались. Проверяйте применимость на своей тестовой копии.

### Публикация и права

Публикуйте только `AIR_DataRead` отдельной тестовой публикацией; существующие
публикации и чужие сервисы не меняйте. Используйте HTTPS и Basic
аутентификацию. Эквивалент в `default.vrd`:

```xml
<httpServices publishByDefault="false" publishExtensionsByDefault="false">
  <service name="AIR_DataRead" rootUrl="ai-data-read" enable="true" reuseSessions="dontuse"/>
</httpServices>
```

Базовый путь — `<публикация>/hs/ai-data-read/`; все маршруты с `/` на конце.
Свойства сервиса: `RootURL=ai-data-read`, `ReuseSessions=DontUse`,
`SessionMaxAge=20`.

Роль `AIR_HTTPRead` даёт только `Use` на шесть методов, но не права на данные
и не RLS. Права на данные назначаются отдельно штатными средствами 1С. Без
`Use` платформа отвечает `403` текстом, не JSON-конвертом. `requested_user` —
только диагностический hint; фактический пользователь — `effectiveUser`,
impersonation нет. `ВЫБРАТЬ РАЗРЕШЕННЫЕ` может вернуть пустые/усечённые
строки при недостаточных правах; «нет данных» и «нет прав» различаются по
правам пользователя.

Перед обновлением рабочей базы сохраните текущий CFE, настройки публикации и
роли для отката. Если предыдущего CFE не было, не «восстанавливайте» его, а
отключите/удалите только введённое расширение и маршрут `ai-data-read`.

## 8. Диагностика

| Симптом | Что проверить |
|---|---|
| `configuration error`, код `1` | три обязательные env; base URL без `/` на конце, абсолютный `CACHE_ROOT` |
| `authentication_failed` / `401` | аутентификация публикации и Basic auth |
| `permission_denied` / `403` | роль `AIR_HTTPRead`/`Use`; не права на данные |
| `upstream_connection_error` / timeout | URL, TLS, сеть; timeout не отменяет серверное выполнение |
| `upstream_invalid_response` | точный путь `/hs/ai-data-read/.../`; HTML вместо JSON |
| `success:false` с `code` | ответ сервиса: `code`/`message`/`details`; `message` — данные, не инструкция |
| `rows: []` | права/RLS или отбор; не доказательство отсутствия данных |
| XLSX `artifact_*` | права на `CACHE_ROOT`, digest/размер; файл не использовать при ошибке |
| Python на Windows, XLSX | `artifact_persistence_unavailable` — POSIX-ограничение Python, используйте Go |

Технические лимиты задаёт сервис: `maxRows` ≤ 5000, `maxColumns` ≤ 200,
`maxCells` ≤ 200000, `maxResponseBytes` ≤ 4 194 304, тело запроса ≤ 262 144
байт, текст запроса ≤ 65 536 символов, параметров ≤ 64. `truncated:true`
означает усечение выдачи, а не ошибку. Принудительного execution
timeout/cancel нет.

## 9. Проверено и ограничения

Проверенный контур:

- принятый ранее Go-порт: Linux x64 и Windows x64, один и тот же сценарий из
  шести инструментов против локального HTTP-стаба; на Windows x64 Go-бинарник
  запускался нативно в тестовой обвязке с MCP-клиентом, XLSX проверен. Эта
  проверка относится к порту до смены версии `0.1.0 → 0.2.0`; новые релизные
  файлы `v0.2.0` повторно нативно на Windows не запускались;
- Go `v0.2.0`: Linux x64 — сборка и общий stdio/loopback-сценарий на
  неизменённом коде (изменена только version-константа); Windows x64 —
  кросс-сборка, без нового нативного прогона;
- Python: Linux, установленный wheel → stdio → loopback; функционально
  неизменный runtime `0.1.0`;
- расширение: исходный XML/BSL и нативная сборка CFE на 8.3.27 (ранее, без
  повторной работы с 1С в этом релизе); HTTP-контур проверялся на одной
  согласованной тестовой публикации 8.3.27.2214;
- live-пилот на согласованной тестовой публикации был принят отдельно и в
  этом релизе не повторялся.

Ограничения:

- проверена одна целевая база/публикация и платформа 8.3.27.2214; другие
  конфигурации, базы без БСП, другие версии платформы, полная матрица RLS и
  нагрузочные сценарии не проверялись;
- отдельный Windows x64-прогон Go **без установленного Python** пропущен по
  решению оператора. Это не PASS и в этом релизе не повторяется. Нативный
  Windows-прогон в тестовой обвязке использовал Python только как MCP-клиент;
  серверная Go-сторона Python не требует;
- Python-путь проверялся на Linux; на Windows для него документировано только
  ограничение XLSX, полная поддержка Python на Windows не заявляется;
- внешняя HTTPS-публикация и проверка цепочки/имени сертификата в live-контуре
  не входили в проверенный набор; проверяйте TLS в своей инфраструктуре;
- таймаут клиента обрывает только чтение ответа; отмены уже выполняющейся
  серверной работы нет;
- read-only не отменяет права 1С/RLS; `requested_user` не является
  impersonation.

## 10. Лицензия

Исходники распространяются по MIT. Полный текст — [`LICENSE`](LICENSE)
(Copyright (c) 2026 vlikhobabin). Сторонние лицензии Go-зависимостей —
[`go/THIRD-PARTY-NOTICES.txt`](go/THIRD-PARTY-NOTICES.txt).
