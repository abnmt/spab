# spab

Общий фундамент четырёх приложений «Go + Nuxt в одном бинарнике» —
[arrarr](https://github.com/abnmt/arrarr),
[feedbase](https://github.com/abnmt/feedbase),
[nomore](https://github.com/abnmt/nomore) и
[workshop](https://github.com/abnmt/workshop).

Не общий рантайм: доменной логики у этих проектов не пересекается ни
строки, PocketBase есть у трёх, PrimeVue — у трёх, но это разные тройки.
Общее — раздача собранного интерфейса, сборка и доставка.

## Что здесь

| Артефакт | Что | Версия |
|---|---|---|
| `github.com/abnmt/spab` | `spa`, `browser`, `dotenv` — Go-модуль без единой зависимости | теги `vX.Y.Z` |
| `github.com/abnmt/spab/pbspa` | адаптер `spa` к роутеру PocketBase, отдельный модуль | теги `pbspa/vX.Y.Z` |
| `make/spab.mk` | цели `web`, `build`, `release`, `image`, `deploy`, `clean` | вендорится в проект |
| `make/Dockerfile.tmpl` | шаблон трёхстадийной сборки | копируется руками |
| `.github/workflows/app-docker.yml` | публикация образа приложения в GHCR | `workflow_call` по тегу |

Nuxt-слои (`@abnmt/nuxt-base`, `@abnmt/nuxt-base-primevue`) — вторая
итерация, их предусловие — переезд всех четырёх на pnpm.

## Использование

Раздача SPA без PocketBase:

```go
//go:embed all:dist
var embedded embed.FS

files, err := spa.Dist(embedded, "dist")   // ошибка = интерфейс не собран
handler, err := spa.New(files)
http.Handle("/", handler)
```

С PocketBase:

```go
app.OnServe().BindFunc(func(se *core.ServeEvent) error {
    files, err := spa.Dist(embedded, "dist")
    if err != nil {
        log.Warn("фронтенд не встроен — соберите web/ и пересоберите бинарник", "err", err)
        return se.Next()
    }
    h, err := pbspa.Bind(se, files)
    if err != nil {
        log.Error("встроенная статика не читается", "err", err)
    } else if !h.Has("sw.js") {
        log.Warn("service worker не встроен: PWA не установится, а уже установленная не обновится")
    }
    return se.Next()
})
```

Директива `//go:embed` обязана остаться в репозитории приложения:
встраивать файлы из зависимого модуля Go не умеет, обойти это нельзя, и
именно поэтому библиотека принимает готовую `fs.FS`.

Сборка:

```makefile
BINARY := workshop
PKG    := ./cmd/workshop
DIST   := internal/webui/dist
include make/spab.mk
```

## Разработка

```bash
GOWORK=off go test ./...        # корневой модуль
cd pbspa && GOWORK=off go test ./...
```

`go.work` в корне нужен только для локальной правки двух модулей сразу.
CI гоняет всё с `GOWORK=off`: workspace умеет прятать забытый `require`,
а потребитель работает без него.

Обратная сторона: без workspace `pbspa` тянет корневой модуль по сети —
`require github.com/abnmt/spab v0.1.0`, никакого `replace` там больше нет.
С `go.work` (то есть просто `go test ./...` из корня) сеть не нужна:
берётся соседний каталог.

Репозиторий публичный не из принципа, а потому что иначе `go mod download`
внутри `docker build` требует токена — в каждом из четырёх приложений и на
каждой машине, где собирают образ руками; `GITHUB_TOKEN` рабочего процесса
чужой репозиторий не открывает, то есть понадобился бы PAT в секретах
четырёх репозиториев. Здесь только библиотека, `make/` и workflow'ы —
разбор устройства самих приложений лежит отдельно.

Корневой модуль обязан оставаться без зависимостей — это проверяется в
CI отдельным шагом. Всё, что тянет чужой код, живёт в подмодулях.
