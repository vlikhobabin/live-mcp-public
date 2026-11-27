# AIDataRead: чтение данных из 1С для Python-разработчика

Сервис `ai-data-read` — это HTTP-сервис расширения `AIDataRead`, опубликованный
в базе 1С. Вы отправляете ему обычный HTTP `POST` с текстом запроса на языке 1С
и получаете JSON. SOAP, COM и клиент 1С не нужны: только стандартная библиотека
Python и HTTP Basic.

Доступ только на чтение: сервис выполняет `ВЫБРАТЬ`-запросы, изменения данных
отклоняются.

## 0. Что нужно

* Python 3.10+ (примеры используют только стандартную библиотеку:
  `urllib.request`, `base64`, `json`).
* Базовый URL сервиса: `https://1c.example/base/hs/ai-data-read` — подставьте
  адрес своей публикации.
* Логин: `read_user`, пароль выдаётся отдельно (в коде не хранить).
* Аутентификация — HTTP Basic (логин/пароль пользователя ИБ); cookies и токены
  не нужны. Если публикация дополнительно закрыта внешним шлюзом с Basic-
  аутентификацией, учётная запись для шлюза и для 1С должна совпадать: сервис
  получает один заголовок `Authorization`.

## 1. Подключение и проверка доступности

`GET {base}/ping/`

```bash
curl -u 'read_user:<пароль>' \
  https://1c.example/base/hs/ai-data-read/ping/
```

Успех — HTTP 200 и JSON:

```json
{"success": true, "service": "ai-data-read", "status": "ok", "version": "0.2.0"}
```

## 2. Список организаций

Запрос:

```
ВЫБРАТЬ РАЗРЕШЕННЫЕ ПЕРВЫЕ 20
    Ссылка КАК Ссылка,
    Наименование КАК Наименование,
    Код КАК Код,
    ИНН КАК ИНН,
    КПП КАК КПП
ИЗ Справочник.Организации
ГДЕ НЕ ПометкаУдаления
УПОРЯДОЧИТЬ ПО Наименование
```

HTTP-запрос:

```
POST {base}/v1/query/execute/
Content-Type: application/json; charset=utf-8
Authorization: Basic ...

{"query": {"text": "ВЫБРАТЬ РАЗРЕШЕННЫЕ ..."}, "limits": {"maxRows": 20}}
```

Ответ (фрагмент, данные обезличены):

```json
{
  "success": true,
  "result": {
    "rowCount": 2,
    "rows": [
      {
        "Ссылка": {"type": "reference", "typeName": "Справочник.Организации",
                   "uuid": "11111111-1111-1111-1111-111111111111",
                   "presentation": "Организация 1", "empty": false},
        "Наименование": "Организация 1",
        "Код": "00-000001",
        "ИНН": "7700000000",
        "КПП": "770000001"
      },
      {
        "Ссылка": {"type": "reference", "typeName": "Справочник.Организации",
                   "uuid": "22222222-2222-2222-2222-222222222222",
                   "presentation": "Организация 2", "empty": false},
        "Наименование": "Организация 2",
        "Код": "00-000002",
        "ИНН": "7700000001",
        "КПП": ""
      }
    ]
  }
}
```

Python:

```python
rows = api.query(ORGANIZATIONS_QUERY, limits={"maxRows": 20})["result"]["rows"]
for row in rows:
    print(row["Код"], row["Наименование"], row["ИНН"], row["Ссылка"]["uuid"])
```

`Ссылка` — объект ссылки; UUID берите из `["uuid"]`, представление — из
`["presentation"]`.

## 3. Реквизиты организации по коду (UUID)

Запрос с параметром `&Ссылка`:

```
ВЫБРАТЬ РАЗРЕШЕННЫЕ
    Ссылка КАК Ссылка,
    Наименование КАК Наименование,
    НаименованиеПолное КАК НаименованиеПолное,
    Код КАК Код,
    ИНН КАК ИНН,
    КПП КАК КПП,
    ОГРН КАК ОГРН,
    ДатаРегистрации КАК ДатаРегистрации,
    Префикс КАК Префикс
ИЗ Справочник.Организации
ГДЕ Ссылка = &Ссылка
```

Параметр — ссылка на организацию из шага 2:

```python
parameters = [
    {
        "name": "Ссылка",
        "valueType": "reference",
        "typeName": "Справочник.Организации",
        "uuid": organization_uuid,
    }
]
```

Ответ — ровно одна строка `result.rows[0]`:

```json
{
  "Ссылка": {"type": "reference", "typeName": "Справочник.Организации",
             "uuid": "11111111-1111-1111-1111-111111111111",
             "presentation": "Организация 1", "empty": false},
  "Наименование": "Организация 1",
  "НаименованиеПолное": "Общество с ограниченной ответственностью «Организация 1»",
  "Код": "00-000001",
  "ИНН": "7700000000",
  "КПП": "770000001",
  "ОГРН": "1027700000000",
  "ДатаРегистрации": "2020-01-01T00:00:00",
  "Префикс": "О1"
}
```

Особенности:

* `КПП` и `ОГРН` могут быть пустыми строками `""`;
* незаполненная дата приходит как `"0001-01-01T00:00:00"` — это «пустая дата»
  1С, а не ошибка;
* `ДатаРегистрации` — строка ISO (`YYYY-MM-DDTHH:MM:SS`).

### Реквизиты справочника (пример конфигурации)

| Поле | Тип | Комментарий |
|---|---|---|
| `Ссылка` | ссылка | UUID — идентификатор записи |
| `Наименование` | строка | краткое наименование |
| `НаименованиеПолное` | строка | полное наименование |
| `Код` | строка | код организации |
| `ИНН` | строка | |
| `КПП` | строка | может быть пустым |
| `ОГРН` | строка | может быть пустым |
| `ДатаРегистрации` | дата | ISO; пустая — `0001-01-01T00:00:00` |
| `Префикс` | строка | |

Набор полей зависит от конфигурации: сервис не отдаёт метаданные, список
реквизитов фиксируется текстом запроса.

## 4. Как читать значения в JSON

| Тип 1С | JSON | Как достать |
|---|---|---|
| строка | `"..."` | как есть |
| число | `1` | как есть |
| булево | `true` / `false` | как есть |
| дата | `"2020-01-01T00:00:00"` | как строка ISO |
| пустая дата | `"0001-01-01T00:00:00"` | проверяйте это значение |
| ссылка | `{"type":"reference","typeName":"...","uuid":"...","presentation":"...","empty":false}` | `value["uuid"]`, `value["presentation"]` |
| перечисление | `{"type":"enum","typeName":"...","name":"...","presentation":"...","empty":false}` | `value["name"]`, `value["presentation"]` |
| пустая ссылка | `{"uuid":"00000000-...","empty":true}` | проверяйте `empty` |
| пусто (`Неопределено`/`Null`) | `null` | проверяйте `is None` |

## 5. Ошибки и ограничения

| HTTP | Что значит |
|---|---|
| `200` | успех (всегда проверяйте `success is True`) |
| `400` | ошибка запроса: JSON `{"success": false, "code": "...", "message": "..."}` |
| `401` | неверный логин/пароль |
| `403` | у учётной записи нет права `Use` на метод (ответ — текст, не JSON) |
| `404` | неверный путь: в URL должен быть `/hs/ai-data-read` |

Частые коды `400`:

* `query_parse_error` — ошибка в тексте запроса (чаще всего неверное имя
  реквизита или таблицы);
* `allowed_select_required` — забыли `ВЫБРАТЬ РАЗРЕШЕННЫЕ`;
* `invalid_parameters` / `unknown_reference_type` — ошибка в `parameters`.

Ограничения на один запрос: до 5000 строк, 200 колонок, 200 000 ячеек,
4 МБ ответа; `limits.maxRows` можно уменьшить, увеличить сверх максимума нельзя.

## 6. Полный рабочий пример

```python
import base64
import json
import os
import urllib.error
import urllib.request


class AIDataRead:
    def __init__(self, base_url: str, username: str, password: str, timeout: float = 60.0):
        self.base_url = base_url.rstrip("/")
        self._auth = "Basic " + base64.b64encode(
            f"{username}:{password}".encode("utf-8")
        ).decode("ascii")
        self._timeout = timeout

    def ping(self) -> dict:
        return self._request("GET", "/ping/")

    def query(self, text: str, parameters: list[dict] | None = None,
              limits: dict | None = None) -> dict:
        body: dict = {"query": {"text": text}}
        if parameters:
            body["parameters"] = parameters
        if limits:
            body["limits"] = limits
        return self._request("POST", "/v1/query/execute/", body)

    def _request(self, method: str, path: str, body: dict | None = None) -> dict:
        data = None
        headers = {"Accept": "application/json", "Authorization": self._auth}
        if body is not None:
            data = json.dumps(body, ensure_ascii=False).encode("utf-8")
            headers["Content-Type"] = "application/json; charset=utf-8"
        request = urllib.request.Request(
            self.base_url + path, data=data, headers=headers, method=method
        )
        try:
            with urllib.request.urlopen(request, timeout=self._timeout) as response:
                raw = response.read()
        except urllib.error.HTTPError as error:
            raw = error.read()
            try:
                payload = json.loads(raw)
            except json.JSONDecodeError:
                raise RuntimeError(f"HTTP {error.code}: {raw[:300]!r}") from error
            raise RuntimeError(
                f"HTTP {error.code}, code={payload.get('code')}, "
                f"message={payload.get('message')}"
            ) from error
        except urllib.error.URLError as error:
            raise RuntimeError(f"Transport error: {error.reason}") from error
        return json.loads(raw)


ORGANIZATIONS_QUERY = """
ВЫБРАТЬ РАЗРЕШЕННЫЕ ПЕРВЫЕ 20
    Ссылка КАК Ссылка,
    Наименование КАК Наименование,
    Код КАК Код,
    ИНН КАК ИНН,
    КПП КАК КПП
ИЗ Справочник.Организации
ГДЕ НЕ ПометкаУдаления
УПОРЯДОЧИТЬ ПО Наименование
"""

ORGANIZATION_QUERY = """
ВЫБРАТЬ РАЗРЕШЕННЫЕ
    Ссылка КАК Ссылка,
    Наименование КАК Наименование,
    НаименованиеПолное КАК НаименованиеПолное,
    Код КАК Код,
    ИНН КАК ИНН,
    КПП КАК КПП,
    ОГРН КАК ОГРН,
    ДатаРегистрации КАК ДатаРегистрации,
    Префикс КАК Префикс
ИЗ Справочник.Организации
ГДЕ Ссылка = &Ссылка
"""


def main() -> None:
    api = AIDataRead(
        "https://1c.example/base/hs/ai-data-read",
        "read_user",
        os.environ["AIDR_PASSWORD"],
    )

    ping = api.ping()
    if ping.get("success") is not True:
        raise RuntimeError("Сервис недоступен")
    print("Сервис:", ping["service"], ping["version"])

    rows = api.query(ORGANIZATIONS_QUERY, limits={"maxRows": 20})["result"]["rows"]
    print("Организаций:", len(rows))
    for row in rows[:5]:
        print(f'{row["Код"]} | {row["Наименование"]} | ИНН {row["ИНН"]}')

    organization_uuid = rows[0]["Ссылка"]["uuid"]
    parameters = [{
        "name": "Ссылка",
        "valueType": "reference",
        "typeName": "Справочник.Организации",
        "uuid": organization_uuid,
    }]
    detail = api.query(ORGANIZATION_QUERY, parameters=parameters)["result"]["rows"][0]
    print("Реквизиты:", json.dumps(detail, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
```

Запуск:

```bash
AIDR_PASSWORD='<пароль>' python example.py
```

## 7. Полезные замечания

* В каждом запросе обязателен `ВЫБРАТЬ РАЗРЕШЕННЫЕ`, иначе сервис вернёт
  `allowed_select_required`.
* `ВЫБРАТЬ *` через этот сервис **не работает**: платформа принимает `*`, но
  результат содержит служебное значение типа «Результат запроса», и сервис
  отвечает `400 unsupported_result_value`. Перечисляйте поля явно.
* Подставляйте значения только через `parameters` (как `&Ссылка` в примере),
  не склеивайте текст запроса — это защита от инъекций.
* Пароль храните в переменной окружения или секрете, не в коде.
* Только один `SELECT`-запрос за вызов; изменения данных запрещены.
* Сервис не отдаёт метаданные: список реквизитов фиксируется в тексте запроса.
* Если публикация работает по HTTP, Basic-пароль передаётся без шифрования —
  используйте HTTPS или доверенную сеть.
