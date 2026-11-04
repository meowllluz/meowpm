# meow

Пакетный менеджер для **Meow Linux** — небольшого дистрибутива на базе Void Linux.
Написан на Go, работает с настоящими бинарными пакетами XBPS (`.xbps`) и официальными
репозиториями Void, но вместо xbps занимается установкой, удалением и обновлением
пакетов сам.

English: [README.md](README.md)

```
$ meow sync
$ sudo meow install neovim
the following packages will be installed:
  libgcc-14.2.0_3
  luajit-2.1.1736781742_1
  neovim-0.10.2_1
total: 3. proceed? [Y/n]
```

## Что умеет

| Команда | Что делает |
|---|---|
| `meow sync` | Скачивает `<arch>-repodata` из репозиториев, разбирает `index.plist`, обновляет кеш |
| `meow install <pkg>...` | Ставит пакет со всеми зависимостями (`-y` — без вопросов, `-n` — dry-run) |
| `meow remove <pkg>...` | Удаляет пакет (`-R` — каскадно с осиротевшими автозависимостями, `-n`, `-y`) |
| `meow upgrade` | Обновляет всё установленное до версий из индекса (`-n`) |
| `meow repair` | Доводит до конца прерванную транзакцию по журналу |
| `meow list` | Список установленных пакетов |
| `meow search <query>` | Поиск по имени и описанию в кеше индекса |
| `meow info <pkg>` | Имя, версия, архитектура, описание из индекса |
| `meow enable / disable <svc>` | Включение и выключение runit-сервиса (`/etc/sv` ↔ `/var/service`) |
| `meow etc-update` | Слияние конфигов, обновлённых пакетами (`*.meow-new`) |
| `meow clean` | Чистка кеша пакетов и логов старше 30 дней |
| `meow log [txid]` | Список транзакций или содержимое лога конкретной |

Отдельно:

- `meow --root <path> <cmd>` — работа с альтернативным корнем (готовая директория ОС),
  удобно для тестов без chroot.

## Как это работает

**Зависимости.** Разрешение идёт по `run_depends` из индекса: имена пакетов, `provides`
(в том числе виртуальные вроде `cmd:awk`) и версии (`awk>=0`, `foo-1.2_1`).
Обход — трёхцветный DFS: порядок установки топологический, циклы ловятся и печатаются
как `circular dependency: a -> b`. Если зависимость удовлетворяют несколько пакетов
и явного выбора нет — установка останавливается со списком кандидатов, а не угадывает.

**Журнал и `repair`.** Перед каждой транзакцией в `/var/db/meow/journal.json` пишется
очередь пакетов с фазами. Ctrl+C не убивает операцию на полуслове: текущий пакет
доводится до конца, журнал сохраняется. `meow repair` при следующем запуске дочистит
частично распакованные файлы, доиграет post-install скрипты и доставит оставшееся.

**Конфиги в `/etc`.** Если пакет приносит новый вариант изменённого пользователем
конфига, старый не затирается: файл остаётся как есть, а новый кладётся рядом с
суффиксом `.meow-new`, затем `meow etc-update` спрашивает `[r]eplace [k]eep [d]elete`.
При удалении пакета изменённый конфиг сохраняется как `<файл>.meow-save`.

**Проверка целостности.** Пакет сверяется с `filename-sha256` из индекса, кеш
(`/var/cache/meow`) переиспользуется только при совпадении хеша.

**Версии.** `CompareVersions` — порт `xbps_cmpver`: dewey-сравнение сегментов плюс
отдельная ревизия после `_` (`1.2.3_4` > `1.2.3_1`), с `alpha < beta < pre < rc < pl`.

**Никаких автоматических хуков.** meow не запускает триггеры и post-install хуки.
Обновлять ли кеш, пересобирать initramfs и перезапускать сервис — решает админ:

- после установки или обновления meow печатает в stderr подсказки — `dracut --force`
  (ядро), `ldconfig` (библиотеки), `fc-cache -fv` (шрифты), `update-ca-certificates`,
  `udevadm control --reload-rules`, `update-desktop-database`,
  `glib-compile-schemas`, … — и на этом его участие заканчивается;
- INSTALL/REMOVE скрипты пакета запускаются, но только с фазами, которые определяет
  XBPS (`pre`/`post`, `pre-remove`/`post-remove`), никаких дополнительных;
- слияние конфигов — всегда отдельная команда (`meow etc-update`), никогда не
  побочный эффект обновления;
- сервисы включаются явно (`meow enable <svc>`), за спиной ничего не стартует
  и не перезапускается.

**Диагностика.** Данные (`list`, `search`, `info`, `log`) идут в stdout и нормально
пайпятся; прогресс, предупреждения и подсказки — в stderr.

## Сборка

Нужен Go 1.27+ и `git`. Единственная зависимость — `howett.net/plist`.

```sh
git clone https://github.com/meowllluz/meowpm.git
cd meowpm
go build -o meow .
go test ./...
sudo install -Dm755 meow /usr/bin/meow
```

Во время работы нужны `curl`, `tar`, `zstd` (Void'овский tar вытаскивает `index.plist`
из `.tar.zst` через внешний zstd), `shadow` и опционально `runit` (для `enable`/`disable`).

## Настройка

`/etc/meow/pm.conf`:

```ini
# обязательный: основной репозиторий
REPO_MAIN=https://repo-default.voidlinux.org/current
# необязательный: nonfree
REPO_NONFREE=https://repo-default.voidlinux.org/current/nonfree

# архитектура, по умолчанию x86_64
ARCH=x86_64

# чем повышать права в подсказках (doas/sudo), по умолчанию doas
ESCALATOR=doas

# пакеты, которые meow не должен трогать (через запятую).
# base-files в Meow Linux держит брендинг дистрибутива.
IGNORE_PACKAGES=base-files
```

Первый запуск:

```sh
sudo meow sync
meow search nvim
sudo meow install neovim
sudo meow upgrade -n     # посмотреть, что обновится
```

`IGNORE_PACKAGES` — жёсткий запрет: и установка, и обновление, и разрешение
зависимостей упадут, если пакет оттуда нужен явно или как зависимость.

## Структура данных

```
/var/db/meow/
  installed.list        имя|версия|дата|auto  (по строке на пакет)
  <pkg>.json            файлы с sha256, зависимости, provides, conflicts, статус
  <pkg>.install         INSTALL-скрипт пакета, если был
  <pkg>.remove          REMOVE-скрипт пакета, если был
  journal.json          незавершённая транзакция (есть только во время операции)
  log/                  логи sync/install/upgrade/repair
/var/cache/meow/
  repo_<repo>.json      кеш индекса
  repo_<repo>.plist     распакованный index.plist (для отладки)
  search_<repo>.txt     плоский индекс для поиска
  <pkg>-<ver>.<arch>.tar.zst  скачанные пакеты
```

Скрипты пакетов вызываются как `sh <script> <phase> <update>`, где фаза —
`pre`/`post` для INSTALL и `pre-remove`/`post-remove` для REMOVE, `update` —
`yes`/`no`. В окружении: `PKGNAME`, `VERSION`, `UPDATE`, `PKG_DESTDIR`.

## Тесты

```sh
go test ./...                        # юнит-тесты: версии, resolve, парсинг files.plist, БД
sudo ./scripts/test-chroot.sh        # интеграционный прогон в chroot (свежий Void rootfs)
ROOT=/tmp/void sudo ./scripts/test-chroot.sh   # то же в свой каталог
```

Интеграционный скрипт скачивает актуальный Void glibc rootfs, поднимает в нём
`curl/tar/shadow/runit/zstd` через нативный xbps, собирает meow внутри и прогоняет
`sync → install → list/info/search → remove`.

## Установка на обычный Void

Meow можно попробовать и на существующем Void, не переставляя систему: мигратор
[xbps2meow](https://github.com/meowllluz/xbps2meow) переносит базу `/var/db/xbps`
в формат meow, после чего xbps удаляется, а meow продолжает обновлять те же пакеты
из тех же репозиториев. Пакеты, пришедшие из xbps, попадают в базу с `Auto=false`,
поэтому `remove -R` не считает их сиротами.

## Чего пока нет

Честный список, чтобы не искать зря: понижение версии (downgrade) намеренно не
поддерживается, нет hold/pinning версий, установки локальных `.xbps`-файлов,
проверки подписи репозитория (только sha256 пакета), хуков и `meow autoremove`
отдельной командой (каскад живёт в `remove -R`).

## О проекте

Личный проект: делает один человек, а большая часть кода на Go написана ИИ-агентом
по текстовому описанию того, как всё должно работать. Затевался в первую очередь
для собственных нужд и публикуется как есть — для тех, кому пригодится, и на случай,
если когда-нибудь будет доведён до ума. Issue приветствуются.

Всё, что здесь публикуется, помечается `*by AI agent`.

## Ссылки

- Формат `.xbps` и `index.plist` — в `void-packages` `Manual.md` и исходниках
  [void-linux/xbps](https://github.com/void-linux/xbps) (`lib/package_unpack.c`).
- Дистрибутив, для которого он пишется: Meow Linux (Void + runit, systemd-boot,
  графической оболочки из коробки нет).
- Скрипты сборки системы: `scripts/build-rootfs.sh`, `live/build-iso.sh`, `live/meow-install`.

Лицензия: GNU GPL v2 (см. [LICENSE](LICENSE)).

*by AI agent
