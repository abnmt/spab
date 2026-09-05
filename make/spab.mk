# spab.mk — общие цели сборки и доставки для проектов «Go + Nuxt в одном
# бинарнике». Источник: github.com/abnmt/spab/make/spab.mk
#
# SPAB_VERSION ниже — версия ЭТОЙ копии. Файл вендорится в репозиторий, а
# не тянется по сети при сборке: include по URL make не умеет, submodule
# ломает и checkout в CI, и контекст docker build, а сборка обязана
# работать без сети.
SPAB_VERSION := v0.1.1
#
# Подключение из Makefile проекта:
#
#     BINARY := workshop
#     PKG    := ./cmd/workshop
#     DIST   := internal/webui/dist
#     include make/spab.mk
#
# Все переменные ниже объявлены через ?= и переопределяются ДО include.
# Свои цели проект дописывает после include; строка `## описание` справа
# от цели попадает в `make help` автоматически.

# ── Обязательное ─────────────────────────────────────────────────────────
BINARY ?=
PKG    ?=
ifeq ($(strip $(BINARY)),)
$(error spab.mk: задайте BINARY до include)
endif
ifeq ($(strip $(PKG)),)
$(error spab.mk: задайте PKG до include, например ./cmd/$(BINARY))
endif

# ── Версия ───────────────────────────────────────────────────────────────
# Попадает в бинарник через -X main.version. Из git, если он есть.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS ?= -s -w -X main.version=$(VERSION)

# Go бывает установлен вне PATH — не заставляем разработчика править
# окружение ради одной команды.
GO    ?= $(shell command -v go 2>/dev/null || echo /usr/local/go/bin/go)
GOFMT ?= $(dir $(GO))gofmt

# ── Интерфейс ────────────────────────────────────────────────────────────
WEB     ?= web
# Куда nuxt generate кладёт результат.
WEB_OUT ?= $(WEB)/.output/public
# Куда смотрит //go:embed all:dist. Пусто — интерфейс уже собирается прямо
# туда, копировать нечего (так устроены arrarr и feedbase).
DIST    ?=
PM      ?= pnpm

ifeq ($(PM),npm)
PM_INSTALL ?= npm --prefix $(WEB) ci
PM_RUN     ?= npm --prefix $(WEB) run
else
PM_INSTALL ?= $(PM) -C $(WEB) install --frozen-lockfile
PM_RUN     ?= $(PM) -C $(WEB)
endif

# ── Сборка ───────────────────────────────────────────────────────────────
# Рабочая машина бывает Apple Silicon, сервер — x86; лишняя платформа
# стоит только времени сборки.
PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64
LINT_DIRS ?= ./cmd ./internal

# ── Запуск ───────────────────────────────────────────────────────────────
RUN_ARGS ?=
DEV_ARGS ?= $(RUN_ARGS)
# Окружение только для `make dev`. Нужно там, где nuxt dev ходит в Go-сервер
# напрямую, а не через nitro.devProxy: тогда запросы идут с чужого origin и
# сервер должен разрешить CORS — но только в режиме разработки.
DEV_ENV  ?=

# ── Контейнер и доставка ─────────────────────────────────────────────────
DEPLOY_HOST     ?=
DEPLOY_DIR      ?= /srv/$(BINARY)
DEPLOY_IMAGE    ?= $(BINARY)
DEPLOY_PLATFORM ?=
DOCKER_ARGS     ?=
# Отдельный сборщик: драйвер docker-container умеет и чужие платформы,
# и выгрузку в tar, а его кэш живёт между запусками.
BUILDX          ?= $(BINARY)

# Параллельная сборка тут только вредит: интерфейс вкомпилируется в
# бинарник, который идёт следом, и с -j порядок нарушится.
.NOTPARALLEL:
.DEFAULT_GOAL := help
.PHONY: help web build run dev test lint release release-current image deploy clean spab-update

help: ## показать список целей
	@echo "Цели ($(BINARY) $(VERSION)):"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_-]+:.*?## /{printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Собранный интерфейс копируется туда, откуда его забирает
# //go:embed all:dist. Именно копируется, а не линкуется: go:embed не
# ходит по симлинкам.
web: ## собрать интерфейс
	$(PM_INSTALL)
	$(PM_RUN) generate
ifneq ($(strip $(DIST)),)
	rm -rf $(DIST)
	mkdir -p $(DIST)
	cp -r $(WEB_OUT)/. $(DIST)/
endif

build: web ## собрать интерфейс и бинарник
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

run: build ## собрать и запустить
	./$(BINARY) $(RUN_ARGS)

dev: ## Go-сервер + nuxt dev с горячей перезагрузкой
	@$(if $(strip $(DIST)),mkdir -p $(DIST) && { [ -f $(DIST)/index.html ] || echo '<!doctype html>сборка интерфейса не нужна в режиме dev' > $(DIST)/index.html; },true)
	$(DEV_ENV) $(GO) run $(PKG) $(DEV_ARGS) & \
	  trap 'kill %1 2>/dev/null' EXIT; \
	  $(PM_RUN) dev

test: ## go test ./...
	$(GO) test ./...

lint: ## gofmt -l и go vet
	@test -z "$$($(GOFMT) -l $(LINT_DIRS))" || { echo "gofmt хочет поправить:"; $(GOFMT) -l $(LINT_DIRS); exit 1; }
	$(GO) vet ./...

# CGO выключен: PocketBase живёт на modernc.org/sqlite (чистый Go),
# поэтому кросс-компиляция обходится без тулчейнов целевых платформ.
release: web ## кросс-сборки в build/
	@mkdir -p build
	@for target in $(PLATFORMS); do \
	  os=$${target%/*}; arch=$${target#*/}; \
	  echo "→ $$os/$$arch"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
	    $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o build/$(BINARY)-$$os-$$arch $(PKG) || exit 1; \
	done
	@ls -lh build/

release-current: web ## сборка под текущую ОС и архитектуру
	@mkdir -p build
	@os="$$($(GO) env GOOS)"; arch="$$($(GO) env GOARCH)"; \
	  echo "→ $$os/$$arch"; \
	  CGO_ENABLED=0 GOOS="$$os" GOARCH="$$arch" \
	    $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "build/$(BINARY)-$$os-$$arch" $(PKG) || exit 1; \
	  ls -lh "build/$(BINARY)-$$os-$$arch"

image: ## собрать образ под эту машину
	docker build --build-arg VERSION=$(VERSION) $(DOCKER_ARGS) \
		-t $(DEPLOY_IMAGE):$(VERSION) -t $(DEPLOY_IMAGE):latest .

# ── Доставка на сервер без реестра ───────────────────────────────────────
#
#     make deploy DEPLOY_HOST=user@nas
#
# Образ собирается здесь и переливается по ssh. Ни токена GHCR на сервере,
# ни приватного пакета, ни ожидания сборки в CI.
#
# Платформа определяется по САМОМУ СЕРВЕРУ, а не по этой машине: рабочая
# машина бывает arm, домашний сервер чаще x86, и собранный по умолчанию
# образ ответил бы «exec format error» — ошибка, на которую легко
# потратить полчаса. Переопределяется: DEPLOY_PLATFORM=linux/amd64.
#
# На сервере в compose.yml должно быть `image: $(DEPLOY_IMAGE):latest`
# вместо `build: .` — иначе compose пойдёт собирать сам.
deploy: ## собрать и перелить образ на сервер (DEPLOY_HOST=user@nas)
	@test -n "$(DEPLOY_HOST)" || { echo "укажите цель: make deploy DEPLOY_HOST=user@nas"; exit 1; }
	@docker buildx inspect $(BUILDX) >/dev/null 2>&1 || \
		docker buildx create --name $(BUILDX) --driver docker-container --bootstrap >/dev/null
	@plat="$(DEPLOY_PLATFORM)"; \
	if [ -z "$$plat" ]; then \
	  arch=$$(ssh $(DEPLOY_HOST) uname -m) || { echo "сервер $(DEPLOY_HOST) недоступен"; exit 1; }; \
	  case "$$arch" in \
	    x86_64|amd64)  plat=linux/amd64 ;; \
	    aarch64|arm64) plat=linux/arm64 ;; \
	    *) echo "не знаю, что собирать под архитектуру сервера: $$arch"; exit 1 ;; \
	  esac; \
	fi; \
	tar=build/$(DEPLOY_IMAGE)-$(VERSION).tar; \
	mkdir -p build; \
	echo "  $(DEPLOY_HOST): $$plat, версия $(VERSION)"; \
	docker buildx build --builder $(BUILDX) --platform $$plat \
		--build-arg VERSION=$(VERSION) $(DOCKER_ARGS) \
		-t $(DEPLOY_IMAGE):$(VERSION) -t $(DEPLOY_IMAGE):latest \
		-o type=docker,dest=$$tar . && \
	echo "  переливаю $$(du -h $$tar | cut -f1) на $(DEPLOY_HOST)" && \
	gzip -c $$tar | ssh $(DEPLOY_HOST) 'docker load' && \
	rm -f $$tar && \
	ssh $(DEPLOY_HOST) 'cd $(DEPLOY_DIR) && docker compose up -d' && \
	echo "готово: $(DEPLOY_IMAGE):$(VERSION) работает на $(DEPLOY_HOST)"

# WEB_OUT назван отдельно от $(WEB)/.output: у проекта, который собирает
# интерфейс прямо в место embed'а, он лежит вообще в другом каталоге
# (`nitro.output.publicDir`), и без этой строки сборка пережила бы clean.
clean: ## удалить сборочные артефакты
	rm -rf $(BINARY) build $(DIST) $(WEB_OUT) $(WEB)/.output $(WEB)/.nuxt

# Обновление самой копии. Версия называется явно: молчаливое подтягивание
# «последней» ломало бы сборку в момент, который к правкам в проекте
# отношения не имеет.
SPAB_UPDATE_URL ?= https://raw.githubusercontent.com/abnmt/spab/$(SPAB_TO)/make/spab.mk
spab-update: ## обновить spab.mk (SPAB_TO=v0.2.0)
	@test -n "$(SPAB_TO)" || { echo "укажите версию: make spab-update SPAB_TO=v0.2.0"; exit 1; }
	@curl -fsSL $(SPAB_UPDATE_URL) -o $(lastword $(MAKEFILE_LIST)).new \
	  && mv $(lastword $(MAKEFILE_LIST)).new $(lastword $(MAKEFILE_LIST)) \
	  && echo "spab.mk обновлён до $(SPAB_TO) — проверьте diff перед коммитом"
