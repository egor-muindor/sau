# Протокол загрузки перевода на smotret-anime.online (Anime 365)

Восстановлен реверсом 2026-09-17. Источники: живой захват трафика (Playwright),
`recon/form.html`, `recon/main.min.js` (функция `initFileUploader`),
`recon/fine-uploader.js` (клиент Fine Uploader 5.x).

Публичного write-API нет. Всё делается эмуляцией браузерных HTTP-запросов.
Публичный read-only API (`/api/...`) годится только для валидации и UX.

## Общие замечания

- Сайт на Yii (PHP). Поля формы именуются `TranslationAdminForm[...]`, submit-кнопка `yt0`.
- CSRF-токен лежит в скрытом поле `csrf` и **ротируется на каждой загрузке страницы**.
- Публичный API отдаёт ошибки с HTTP 200 и телом `{"error":{"code":403,...}}` —
  проверять тело, а не только статус.

## Фаза 1. Логин

```
GET  /users/login                     → из HTML взять hidden input name="csrf"
POST /users/login                     Content-Type: application/x-www-form-urlencoded
     csrf=<token>
     LoginForm[username]=<email>
     LoginForm[password]=<pass>
     yt0=
     dynpage=1
→ 302 + session cookie. Капчи нет.
```

Поля `rememberMe` в форме **нет** — проверено по захвату. Долгоживущая
identity-cookie выдаётся безусловно.

После входа сайт ставит два auth-cookie:

| Cookie | Срок | Назначение |
|---|---|---|
| `PHPSESSID` | сессионная, HttpOnly | текущая сессия, пересоздаётся сервером |
| Yii identity (имя — hash, напр. `aaaa8ed0…`) | ≈30 дней, HttpOnly | вход без пароля |

Плюс `csrf` тоже лежит в cookie.

Поэтому правильная модель для CLI: пароль вводится один раз интерактивно,
дальше живём на identity-cookie, пароль на диск не пишется.

Смена пароля инвалидирует сохранённую сессию (проверено: после смены
сохранённые cookie перестали работать, GET формы отдал страницу логина).

## Фаза 2. Страница формы

```
GET /translations/create?seriesId=<ID>
```

Из ответа парсить:

1. Свежий `csrf`.
2. Инлайн-скрипт с вызовами `initFileUploader({...})` — их **два**
   (видео и субтитры), структура:

```json
{
  "validation": { "allowedExtensions": ["mp4", "mkv", "..."], "itemLimit": 1 },
  "formField":  "TranslationAdminForm_videoFileNew",
  "serverId":   28,
  "serverUrl":  "https://t-time28.melon-soda.org",
  "serverUrls": ["https://t-time28.melon-soda.org", "https://time28.melon-soda.org",
                 "https://time28.anime-on.ru",      "https://t-time28.anime-on.ru"]
}
```

**Серверы выдаются динамически и меняются со временем — кэшировать запрещено.**
Парсить заново перед каждой заливкой. `serverId` меняется вместе с ними и входит
в полезную нагрузку скрытого поля.

Видео и субтитры идут на один и тот же `serverId` — это один загрузчик
с разными списками расширений, а не две подсистемы.

Расширения: видео — `mp4 mkv webm avi mov m4v ts flv ogm wmv mpg mod mmv tod asf
divx 3gp 3g2 m2t mts bik ogg qt rm ram rmvb smk vob`; субтитры — `ass ssa srt vtt webvtt`.

### Канал раздачи

`upload-channel` — это **cookie**, не поле формы. Ставится до GET страницы,
в браузере сопровождается перезагрузкой.

**Канал меняет набор upload-серверов** (проверено живьём):

| Значение | Смысл | `serverId` | Серверы |
|---|---|---|---|
| `0` | Все (CDN + РФ) | 28 | 4 зеркала `t-time28`/`time28` на `melon-soda.org` и `anime-on.ru` |
| `2` | Только CDN | 28 | те же 4 зеркала |
| `3` | Только РФ | 28 | **один**: `https://ru-time28.anime-on.ru/` |
| cookie не выставлена | умолчание сайта | 28 | как `0`/`2` |

Три следствия для реализации:

1. **Пул может состоять из одного хоста** (канал «РФ»). Фейловер и выбор
   «самого быстрого» вырождаются; балансер обязан это переживать.
2. **У РФ-адреса есть завершающий слэш**, у остальных — нет.
   Склейка `serverUrl + "/upload.php"` даст двойной слэш. Нормализовать
   базовый URL обязательно.
3. **`serverId` не различает каналы** — он равен 28 и для `melon-soda`,
   и для `ru-time28`. Поэтому `serverId` сам по себе **не является**
   достаточным признаком «чанки лежат там же»: канал надо хранить в
   состоянии докачки и сравнивать вместе с `serverId`.
   Делят ли РФ- и CDN-хосты одно хранилище — не проверено.

## Фаза 3. Заливка файла (Fine Uploader 5.x, chunked)

Эндпоинт: `{serverUrl}/upload.php`

Параметры, переопределённые сайтом относительно дефолтов библиотеки:

| Параметр | Значение | Дефолт библиотеки |
|---|---|---|
| `chunking.enabled` | `true` | `false` |
| `chunking.mandatory` | `true` | `false` |
| `chunking.concurrent.enabled` | `true` | `false` |
| `chunking.partSize` | `5_000_000` | `2_000_000` |
| `resume.enabled` | `true` | `false` |
| `maxConnections` | `5` | — |
| `retry` | авто, до 4 попыток, задержка 1 с | — |

Каждый чанк — `POST multipart/form-data`:

| Поле | Содержимое |
|---|---|
| `qquuid` | UUID файла (генерируется клиентом, один на весь файл) |
| `qqfilename` | имя файла |
| `qqtotalfilesize` | размер файла в байтах |
| `qqtotalparts` | всего чанков |
| `qqpartindex` | индекс чанка, с нуля |
| `qqpartbyteoffset` | смещение чанка в байтах |
| `qqchunksize` | размер этого чанка |
| `qqfile` | тело чанка |
| `qqresume` | признак возобновления (при докачке) |

Ответ на чанк: `{"success":true,"uuid":"<uuid>","uploadName":null}`

### Выбор хоста для чанка

Зеркала из `serverUrls` — **один сервер с общим хранилищем** (подтверждено:
чанки ушли на разные хосты, финализация на один, файл собрался).

Правило (из `main.min.js`):

1. Если `partIndex < len(serverUrls)` и хост не помечен сбойным → `serverUrls[partIndex]`.
2. Иначе — самый быстрый по замерам (`размер чанка / длительность`), не сбойный.
3. Иначе — случайный не сбойный.
4. Если сбойные все — сбросить отметки и взять случайный.

Сбойный хост помечается в `onChunkError` и исключается из выбора.

### Keep-alive

Пока заливка идёт, каждые **15 секунд**:

```
GET {baseEndpoint}?touch=1&file=/{uuid}
```

Без этого сервер подчищает недозалитое.

### Финализация

Уходит на **хост последнего успешного чанка** (не на базовый):

```
POST {lastChunkHost}/upload.php?done      Content-Type: application/x-www-form-urlencoded
     qquuid=<uuid>&qqfilename=<name>&qqtotalfilesize=<size>&qqtotalparts=<n>
→ {"success":true,"uuid":"<uuid>"}
```

### Удаление

Тоже на хост последнего успешного чанка:

```
DELETE {lastChunkHost}/upload.php?/{uuid}&
→ {"success":true,"uuid":"<uuid>"}
```

## Фаза 4. Скрытое поле формы

После успешной заливки в `TranslationAdminForm[videoFileNew]` кладётся JSON.
Захвачено живьём:

```json
{
  "files": [
    {
      "name": "test-upload.mp4",
      "originalName": "test-upload.mp4",
      "uuid": "580fcb20-5c8c-419c-88d1-5b889298c20f",
      "size": 6531378,
      "status": "upload successful",
      "file": { "qqDropTarget": { "__ym_indexer": 10 }, "qqThumbnailId": 0 },
      "batchId": "9ea18edd-baaf-485c-8a51-b0df0b0a055c",
      "id": 0
    }
  ],
  "endpoint": "https://t-time28.melon-soda.org/upload.php",
  "serverId": 28
}
```

Важно: `endpoint` здесь — **базовый** `serverUrl + /upload.php`, а не хост
последнего чанка. Он же используется для keep-alive.

Поле `file` (`qqDropTarget`, `qqThumbnailId`) — мусор браузерного DOM.
`id` и `batchId` — внутренние счётчики клиента.
Открытый вопрос: читает ли сервер что-то кроме `uuid`/`name`/`size`/`status`.

Для субтитров — то же самое в `TranslationAdminForm[subFileNew]`.
Пустое состояние: `{"files":[],"endpoint":"...","serverId":28}`.

## Фаза 5. Отправка формы

```
POST /translations/create?seriesId=<ID>   Content-Type: application/x-www-form-urlencoded
     csrf=<свежий токен>
     TranslationAdminForm[seriesIdNew]=<ID>
     TranslationAdminForm[episodeNumberNew]=<номер>
     TranslationAdminForm[episodeTypeNew]=<tv|ova|ona|movie|special|tv_special|preview>
     TranslationAdminForm[type]=<raw|subJa|subEn|voiceEn|subUk|voiceUk|subRu|voiceRu>
     TranslationAdminForm[authorsNew]=<авторы>
     TranslationAdminForm[addedByAuthor]=<0|1>
     TranslationAdminForm[videoFileNew]=<JSON из фазы 4>
     TranslationAdminForm[subFileNew]=<JSON или пусто>
     yt0=
```

Озвучка — это `type=voiceRu` (в интерфейсе подписана просто «Озвучка»).

`addedByAuthor` — Yii-паттерн из пары hidden `0` + checkbox `1`;
слать нужно **одно** значение.

### Захвачено живьём

Реальная отправка (серия 1, файл 1 106 621 110 байт ≈ 1,03 ГБ, 455 запросов
к upload-серверам) дала **HTTP 302 — успех**.

### Редирект содержит идентификатор созданного перевода

```
Location: /translations/update/5995522
                               ^^^^^^^ id перевода
```

Это единственный способ узнать id сразу: в API запись появляется с задержкой.
Его следует извлекать из заголовка и сохранять — по нему строится всё остальное.

Публичный адрес перевода **не нужно собирать из слагов** — API отдаёт его готовым
в поле `url`:

```
GET /api/translations?seriesId=36866&type=voiceRu
  url = https://smotret-anime.org/catalog/<слаг-тайтла>-36866/1-seriya-371562/ozvuchka-5995522
```

Структура адреса, если он всё же понадобится вручную:

```
/catalog/{слаг тайтла}-{seriesId}/{слаг серии}-{episodeId}/{слаг типа}-{translationId}
```

Страница редактирования — `/translations/update/{translationId}`.

Замечание: API отдаёт ссылки на домене `smotret-anime.org`, тогда как форма
работает на `smotret-anime.online`. Это зеркала одного сайта; домен в сохранённых
ссылках нормализовать не нужно, но и полагаться на конкретный не стоит.

Тело POST содержало больше полей, чем видно в разметке формы:

```
csrf=<токен>
TranslationAdminForm[seriesIdNew]=36866
TranslationAdminForm[episodeNumberNew]=1
TranslationAdminForm[episodeTypeNew]=tv
TranslationAdminForm[type]=voiceRu
TranslationAdminForm[authorsNew]=<авторы>
TranslationAdminForm[addedByAuthor]=0
upload-channel=2                      ← не только cookie, но и поле формы
upload-channel-mobile=2               ← то же
TranslationAdminForm[videoFileNew]=<JSON>
qqfile=                               ← пустое, от файлового input
TranslationAdminForm[subFileNew]=
qqfile=                               ← пустое, второй загрузчик
yt0=
dynpage=1                             ← как и на логине
```

Три неожиданности:

1. **`upload-channel` и `upload-channel-mobile` отправляются и как поля формы**,
   а не только как cookie. Слать оба, значение то же.
2. **Два пустых поля `qqfile`** — файловые input внутри формы. Браузер их шлёт;
   воспроизводить для совместимости.
3. **`dynpage=1`** присутствует и здесь, не только в логине.

### Задержка появления в API

После успешной отправки перевод появляется в публичном API **не сразу**, а спустя
несколько минут. Проверено: сразу после отправки полный обход 382 переводов тайтла
новой записи не нашёл; спустя несколько минут записей стало 383 и перевод нашёлся.

Транскодирование при этом не потребовалось — задержка относится к индексации,
а не к обработке видео.

Следствие для автоматизации: **проверка дубля через API не видит только что
отправленный перевод**. Нескольких минут с избытком хватает, чтобы упавшая сразу
после отправки программа при перезапуске не нашла свою же публикацию и создала дубль.
Поэтому нельзя решать «дубля нет — значит можно отправить повторно» после неясного
исхода отправки; в этом случае нужно спрашивать человека.

### Сайт переписывает строку авторов

Отправлено: `JamClub (Jam, Oriko)`
Сохранено и отдано API: `JamClub (Jam & Oriko)`

Запятая между участниками заменена на амперсанд. Поэтому сравнивать свою строку
авторов с `authorsSummary` дословно бесполезно — сверка обязана нормализовать
разделители (`,`, `&`, ` и `), регистр и пробелы.

Пример записи, созданной этой отправкой:

```
id=5995522  episodeId=371562  typeKind=voice  typeLang=ru  isActive=1
qualityType=tv  1920x1080  authorsSummary="JamClub (Jam & Oriko)"
```

## Публичный read-only API

База: `https://smotret-anime.online/api`

### Главная ловушка

**API молча игнорирует неизвестные параметры и неизвестные сегменты пути**,
возвращая HTTP 200 и правдоподобные, но неверные данные. Ошибки не будет.
Клиент обязан перепроверять результат у себя, а не доверять тому, что фильтр применился.

Подтверждённые случаи:

| Запрос | Что происходит |
|---|---|
| `/series/{id}/episodes` | **не существует**; отдаёт список посторонних сериалов |
| `/translations?episodeNumber=N` | параметр игнорируется, выдача не отфильтрована |
| `/translations?type=voiceRu` | работает |
| `/translations?seriesId=N` | работает |

Плюс общее: ошибки приходят с HTTP 200 и телом `{"error":{"code":403,...}}` —
проверять тело, а не статус.

### Рабочие эндпоинты

```
GET /series/{id}                  карточка тайтла
GET /episodes?seriesId={id}       эпизоды: id, episodeInt, episodeFull, episodeType
GET /translations?seriesId={id}&type={type}&limit=N
                                  переводы: id, episodeId, typeKind, typeLang, authorsSummary
```

Для полного обхода использовать `feed=id` и `afterId`, а не `offset`
(offset деградирует на сотнях тысяч записей).

### Проверка дубля перед повторной отправкой формы

Фильтра по номеру серии нет, поэтому связка такая:

1. `GET /episodes?seriesId={id}` → найти `id` эпизода по `episodeInt`.
2. `GET /translations?seriesId={id}&type={type}` → отобрать записи с этим `episodeId`.
3. Сверить `authorsSummary` со своим значением.

Совпадение по всем трём (эпизод, тип, авторы) означает, что перевод уже опубликован.

Замечание о реальности: на популярном онгоинге у одной серии бывает
7–9 разных озвучек, поэтому совпадения только по эпизоду и типу недостаточно —
сверка авторов обязательна.

## Что осталось открытым

1. Точная форма ответа при успешной и при отклонённой отправке формы (фаза 5).
2. Минимально достаточный набор полей в `files[]` — сервер может игнорировать
   браузерный мусор, но это не подтверждено.
3. Поведение `qqresume` на практике: сколько живёт частичная загрузка на сервере
   и что происходит при смене набора `serverUrls` между попытками.
