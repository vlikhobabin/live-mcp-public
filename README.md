# AIDataRead + live-mcp-for-1c — первая публикация исходников

> **Первая публикация исходников, не релиз.** Это исходный состав публичного
> репозитория. Собранный `AIDataRead.cfe` и его контрольная сумма в Git не
> входят и будут приложены к отдельному будущему GitHub Release.

Репозиторий (22 файла): 6 Python-модулей, `pyproject.toml`, 12 XML/BSL,
`examples/dcs-neutral.xml`, README и `LICENSE` (MIT); без тестов, истории,
private-профилей, адресов и credentials.

## 1. Версии и границы

| Компонент | Значение |
|---|---|
| Пакет/Python | `live-mcp-for-1c` 0.1.0; Python `>=3.12` (проверено 3.12.13); `httpx 0.28.1`, `mcp 1.30.0`; build `setuptools>=77`; stdio, protocol `2025-11-25`, 6 tools |
| Расширение | `AIDataRead` 0.2.0, `AddOn`, совместимость `8.3.27`; CFE в Git не входит (§2.2) |
| Лицензия | MIT; полный текст — `LICENSE` (§8) |
| ОС | Linux проверена; XLSX-запись требует POSIX `dir_fd`, иначе `artifact_persistence_unavailable` |
| Ограничения | без БСП и другие платформы/базы **не проверялись**; универсальность не заявляется |

Предыдущий функциональный контур — один сервер 8.3.27.2214: `K=2 → 20`, СКД
`20/30/14`, итоги `50/14`, общий `64`, XLSX прочитан, отказ
`allowed_select_required`. Это старые результаты прежнего контура, не приёмка
этой публикации; нового функционального прогона 1С и установок в ней нет.

## 2. Исходники расширения

### 2.1. Снимок
12 файлов `extensions/http-read/src/**` входят в этот репозиторий; в двух BSL
удалены только служебные комментарии, код/XML/метаданные не менялись. SHA-256
списка: сначала относительные POSIX-пути сортируются `LC_ALL=C sort`, затем для
них считается `sha256sum` (hex, два пробела, путь), хешируются строки с
завершающим `\n`; корень — `extensions/http-read/src`:

```text
b000de1d4e570b842e82d0010b473d6394981bbf24193699a17bb93141d190df
```

### 2.2. Самостоятельная сборка и отдельный будущий релиз
Рабочий путь получения CFE — собрать его самостоятельно из снимка §2.1 штатным
Конфигуратором (порядок — §4). Это не требует готового бинарника из
репозитория.

Собранный `AIDataRead.cfe` и его контрольная сумма в Git не входят и не
поставляются в этом репозитории. Они будут приложены к отдельному будущему
GitHub Release; ссылка на ещё не существующий Release здесь не приводится.

### 2.3. Границы предыдущей локальной сборки
В предыдущей локальной работе CFE собирался штатно Конфигуратором 8.3.27.2214
из ровно этого снимка §2.1 в новой пустой временной файловой ИБ (создана
`CREATEINFOBASE File=…`, все последующие запуски — только `/F` к ней).
Применения к БД не было: `/UpdateDBCfg`, публикация, запуск приложения и
функциональный прогон не выполнялись. `CheckConfig -ConfigLogIntegrity
-IncorrectReferences -Server -HandlersExistence -Extension AIDataRead`
завершился без ошибок; CFE получен командой `DumpCfg -Extension AIDataRead`.

Граница: `CheckCanApplyConfigurationExtensions` в этой пустой ИБ не прошла по
`РежимСовместимостиИнтерфейса` (у расширения `TaxiEnableVersion8_2`). Флаги
обхода не использовались; применимость к целевой конфигурации здесь не
заявляется — проверьте её на своей БД.

## 3. Python и MCP
```bash
python3.12 -m venv "$HOME/.venvs/live-mcp-for-1c"
BUNDLE="/путь/к/каталогу-репозитория"
"$HOME/.venvs/live-mcp-for-1c/bin/python" -m pip install "$BUNDLE"
```
Обновлять pip не обязательно; зависимости/build-требования ставятся из
настроенного индекса (offline — подготовьте wheel). Проверка: `.../bin/python -c
"import live_mcp_for_1c"`; запуск без env даёт ожидаемый `configuration error`
(сервер запускает MCP-клиент).

| Переменная | Обяз. | Значение |
|---|---|---|
| `LIVE_MCP_FOR_1C_HTTP_SERVICE_BASE_URL` | да | база с корнем сервиса, без `/` на конце |
| `LIVE_MCP_FOR_1C_HTTP_SERVICE_USERNAME` | да | пользователь ИБ |
| `LIVE_MCP_FOR_1C_HTTP_SERVICE_PASSWORD` | да | секрет; только env/secret store |
| `LIVE_MCP_FOR_1C_VERIFY_TLS` | нет | `true` по умолчанию |
| `LIVE_MCP_FOR_1C_HTTP_TIMEOUT_SECONDS` | нет | `60`; не отменяет серверное выполнение |
| `LIVE_MCP_FOR_1C_CACHE_ROOT` | нет | абсолютный каталог XLSX; по умолчанию `$HOME/.cache/live-mcp-for-1c` |

Shareable JSON **не содержит пароль**; MCP-хост/клиент обязан добавить его в
env дочернего процесса из своего secret store или локального private launch env
(chmod 600, не git и не облачная синхронизация) и форвардить переменную серверу:

```json
{
  "mcpServers": {
    "live-mcp": {
      "command": "/home/<user>/.venvs/live-mcp-for-1c/bin/live-mcp-for-1c",
      "args": [],
      "env": {
        "LIVE_MCP_FOR_1C_HTTP_SERVICE_BASE_URL": "https://1c.example/base/hs/ai-data-read",
        "LIVE_MCP_FOR_1C_HTTP_SERVICE_USERNAME": "read_user"
      }
    }
  }
}
```
Пример локального launcher-а (файл не shareable/не синхронизируется):
`set -a; . "$HOME/.config/live-mcp-for-1c.env"; set +a; exec "$HOME/.venvs/live-mcp-for-1c/bin/live-mcp-for-1c"`.

Инструменты: `ping_service`, `get_service_info`, `validate_1c_query`,
`execute_1c_query`, `execute_dcs_schema`, `render_dcs_schema`; stdout — только MCP, stderr — диагностика.

## 4. 1С: сборка, установка, публикация, права
Установку и обновление БД выполняйте в тестовой копии; **до любого обновления
БД** сохраните текущий CFE, настройки публикации и роли для отката.

### Сборка CFE
Сборка идёт в новой пустой временной файловой ИБ и не требует применения к
БД. Не используйте `/S`, существующие или серверные базы.

1. Создайте ИБ командой `CREATEINFOBASE File="<путь>";`; далее во всех
   командах указывайте только явный `/F "<путь>"`.
2. Сначала загрузите исходники `extensions/http-read/src` в эту же новую ИБ:
   `/LoadConfigFromFiles "<src>" -Extension AIDataRead`. На проверенной
   платформе загрузка создала расширение `AIDataRead` без отдельного ручного
   создания. Если ваша платформа требует именованное расширение, создайте
   `AIDataRead` («Расширения конфигурации» → «Создать») в этой же временной ИБ
   и повторите загрузку. Это самостоятельный штатный путь; готовый CFE из
   отдельного GitHub Release появится позже и в Git не входит.
3. Выполните
   `CheckConfig -ConfigLogIntegrity -IncorrectReferences -Server -HandlersExistence -Extension AIDataRead`
   и проверьте вывод/лог. При ошибках CFE не выгружайте.
4. Выгрузите CFE в явный путь и посчитайте SHA:
   `/DumpCfg "<out.cfe>" -Extension AIDataRead`, затем
   `sha256sum "<out.cfe>"`. Применение к БД для сборки не требуется.

Пароль в командную строку не передавайте.

### Применимость и установка в тестовую копию
1. В отдельной тестовой копии целевой ИБ (не в сборочной временной ИБ)
   загрузите собранный `AIDataRead.cfe` («Расширения конфигурации» →
   «Загрузить из файла»).
2. Выполните `CheckCanApplyConfigurationExtensions -Extension AIDataRead`.
   Только если проверка завершилась успешно и не сообщает о несовместимости,
   примените расширение и обновите конфигурацию БД (`/UpdateDBCfg` или
   «Обновить конфигурацию»). Флаги обхода проверок и пароль в командной строке
   не использовать.
3. Если проверка не прошла, не обходите её: проверьте совместимость на целевой
   конфигурации и устраните причину. `CheckCanApplyConfigurationExtensions` не
   является условием `DumpCfg`; наблюдавшееся на пустой ИБ несовпадение
   описано в §2.3.

`Language.Русский` — заимствованный объект, сопоставляется с базовой
конфигурацией по точному имени `Русский`; одного кода `ru` с другим именем
недостаточно. Перед применением выполните штатную проверку применимости
расширения к целевой конфигурации; не отключайте проверки.

Публикация: «Администрирование → Публикация на веб-сервере». Для отдельной
тестовой публикации включите **только** `AIR_DataRead`; существующие публикации
и чужие сервисы не менять. Используйте HTTPS и Basic-аутентификацию. Эквивалент
в `default.vrd` — полный subtree с обоими флагами false под `httpServices`:

```xml
<httpServices publishByDefault="false" publishExtensionsByDefault="false">
  <service name="AIR_DataRead" rootUrl="ai-data-read" enable="true" reuseSessions="dontuse"/>
</httpServices>
```
Базовый путь — `<публикация>/hs/ai-data-read/`, все маршруты с `/` на конце;
свойства: `RootURL=ai-data-read`, `ReuseSessions=DontUse`, `SessionMaxAge=20`.
`requested_user` — только диагностический hint; фактический пользователь —
`effectiveUser`.

Роль `AIR_HTTPRead` даёт только `Use` на 6 методов, не права на данные/RLS;
права на данные — отдельно. Без `Use` — `403`; с `Use`, но без прав, возможны
пустые/усечённые строки или `500 internal_error`.

`get_service_info` показывает объявленную версию расширения, но **не**
идентифицирует точный установленный CFE; для идентичности — SHA CFE и записи
установки.

## 5. Первый запрос
После `ping_service`/`get_service_info` (`status:"ok"`, `version:"0.2.0"`) выполните два шага: `validate_1c_query`:

```json
{"query_text":"ВЫБРАТЬ РАЗРЕШЕННЫЕ 10 * &K КАК Значение","parameters":[{"name":"K","valueType":"number","value":2}]}
```
и `execute_1c_query` с теми же полями плюс `"output_format":"json"`. Ожидается
`success:true`, `result.rowCount:1`, `result.truncated:false`,
`result.rows[0].Значение == 20`. `validate_1c_query` возвращает
`validationStatus:"passed"` и не принимает `output_format`.

Безопасный отказ: `validate_1c_query` без `РАЗРЕШЕННЫЕ` даёт
`success:false`, `code:"allowed_select_required"`. В MCP приходит JSON-конверт
сервиса; **HTTP 400 описывает wire-ответ, это не отдельный MCP-протокол.**
Блокировки запрещены (`lock_not_allowed`); не обходите отказ записью.

## 6. СКД и XLSX
Поддерживается один локальный источник, один `DataSetQuery`, выбранные поля,
группировка, `Сумма`/`Количество`, сортировка и простые отборы; несколько
наборов/связей, вычисляемые/пользовательские поля, оформление, макеты и внешние
функции не поддерживаются; сырой XML в HTTP-теле не принимается.

Нейтральный пример — `examples/dcs-neutral.xml`; передайте его содержимое в
`schema_xml`. `execute_dcs_schema`:

```json
{"schema_xml":"<содержимое examples/dcs-neutral.xml>","parameters":[{"name":"K","valueType":"number","value":2}],"output_format":"json"}
```
`render_dcs_schema` для XLSX:

```json
{"report_id":"neutral","schema_xml":"<содержимое examples/dcs-neutral.xml>","parameters":[{"name":"K","valueType":"number","value":2}],"output":{"format":"json","artifacts":["xlsx"]}}
```
Ожидается `renderStatus:"passed"`; артефакт содержит `contentOmitted:true`,
`localPath`, `sizeBytes`, `sha256` (Base64 от SHA-256). MCP проверяет
Base64/размер/digest и создаёт файл в `CACHE_ROOT/dcs-artifacts/` эксклюзивно,
`0600`, без перезаписи; `fileName` — не путь. При ошибке файл не создаётся,
диагностика — в `diagnostics`.

## 7. Диагностика, откат, ограничения

| Симптом | Что проверить |
|---|---|
| `configuration error` | три обязательные env; base URL без `/` на конце |
| `authentication_failed` / 401 | аутентификация публикации и Basic auth |
| `permission_denied` / 403 | роль `AIR_HTTPRead`/`Use`; не права на данные |
| `upstream_connection_error` / timeout | URL, TLS, сеть; timeout не отменяет сервер |
| `upstream_invalid_response` | точный путь `/hs/ai-data-read/.../`; HTML вместо JSON |
| `success:false` с `code` | ответ сервиса: `code`/`message`/`details` |
| `rows: []` | права/RLS или отбор; не доказательство отсутствия данных |
| XLSX `artifact_*` | права на `CACHE_ROOT`, digest/размер; файл не использовать |

Откат: Python — остановить клиент, `pip uninstall live-mcp-for-1c`/удалить venv.
1С — если предыдущий CFE был, восстановить его, публикацию и роли; если это
первая установка и предыдущего CFE нет, не «восстанавливать» несуществующий
файл, а отключить/удалить только введённое расширение и маршрут `ai-data-read`.
Обновить БД; на рабочей базе — только по штатной процедуре с резервной копией.
Старый CFE не ставить вместо очищенной сборки.

Read-only не отменяет права 1С/RLS; `requested_user` — не impersonation;
таймаут не отменяет серверное выполнение; без БСП и другие контуры не
проверялись. Это первая публикация исходников, не релиз; CFE и его контрольная
сумма — отдельный будущий GitHub Release.

## 8. Лицензия
Исходники распространяются по MIT. Полный текст — [LICENSE](LICENSE)
(Copyright (c) 2026 vlikhobabin).
