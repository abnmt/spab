// Package spa раздаёт собранный Nuxt SPA из fs.FS.
//
// Своим обработчиком, а не apis.Static или http.FileServerFS, по причинам,
// каждая из которых в проектах-предках уже стоила отдельного расследования
// с одинаковым описанием «интерфейс не обновляется, и ничего не помогает»:
//
//  1. Редирект на слэш теряет строку запроса. Nuxt раскладывает страницы
//     каталогами (dist/dict/index.html), а Static на каталоге без
//     завершающего слэша отвечает 301 на URL.Path+"/" — без query. Ссылка
//     /dict?tab=locations доезжала до адресата уже без вкладки. Здесь
//     каталог отдаётся своим index.html прямо по исходному адресу.
//
//  2. Откат на оболочку для любого ненайденного пути ломает приложение
//     молча. Старый index.html просит чанк со старым хешем, получает
//     HTML со статусом 200 и падает на разборе модуля. Так же отвечал бы
//     и /sw.js в сборке, куда он не попал: проверка обновления worker'а
//     падает, и старый worker живёт вечно, отдавая старую оболочку.
//     Откат делается только для навигации, всё прочее честно 404.
//
//  3. У embed.FS нулевое время изменения, поэтому http.ServeContent не
//     ставит Last-Modified, а ETag не считает никто. Оболочка уезжала в
//     браузер вовсе без валидаторов: ревалидировать нечем, 304 невозможен,
//     дальше каждый браузер применяет свою эвристику — отсюда «у меня
//     обновилось, а у тебя нет».
//
//  4. Тип .webmanifest не входит ни во встроенную таблицу Go, ни в
//     /etc/mime.types alpine и distroless. Без него манифест приезжает как
//     text/plain, и браузер отказывается ставить PWA — молча.
//
//  5. Сжатия не было вовсе, а оболочка весит под семьсот килобайт.
//
// Статика читается в память целиком один раз при создании: хеш и сжатая
// копия считаются на старте, на запросе не остаётся ни ввода-вывода, ни
// компрессии, ни обращения к файловой системе — только поиск по карте,
// поэтому выйти за пределы встроенной статики нельзя ни при каком пути.
// Плата — сжатые копии в памяти, порядка мегабайта на собранную SPA.
//
// Зависимостей у пакета нет намеренно: его подключают и приложения без
// PocketBase. Адаптер к роутеру PocketBase — отдельный модуль
// github.com/abnmt/spab/pbspa.
package spa

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

const (
	// CacheImmutable — для файлов, имя которых содержит хеш содержимого.
	// Такой файл не может измениться по построению, и перепроверять его
	// незачем целый год.
	CacheImmutable = "public, max-age=31536000, immutable"
	// CacheRevalidate — для всего остального: оболочки, service worker'а,
	// манифеста и иконок. no-cache значит не «не кэшируй», а «кэшируй, но
	// каждый раз спрашивай» — с ETag это дешёвый 304, а не полная загрузка.
	// Именно этого заголовка не хватало, чтобы пересборка доезжала до
	// браузера сама.
	CacheRevalidate = "no-cache"
)

// ErrNotBuilt означает, что интерфейс не собран: в переданной fs.FS нет
// оболочки. Это не ошибка запуска приложения — без фронта обязаны
// подниматься и API, и админка PocketBase.
var ErrNotBuilt = errors.New("spa: интерфейс не собран (нет index.html)")

// Handler раздаёт статику. Создаётся New и после этого неизменен.
type Handler struct {
	files map[string]*file
	index *file
	bytes int
}

type file struct {
	body  []byte
	gz    []byte // nil, если сжимать нечего или сжатие не окупилось
	etag  string
	mime  string
	cache string
}

type config struct {
	index       string
	immutable   []string
	compress    bool
	minCompress int
	mimes       map[string]string
}

// Option настраивает Handler. Умолчания рассчитаны на `nuxt generate`.
type Option func(*config)

// WithIndex меняет имя файла оболочки. По умолчанию index.html.
func WithIndex(name string) Option {
	return func(c *config) { c.index = name }
}

// WithImmutable задаёт префиксы путей, которые кэшируются навсегда, —
// целиком, вместо умолчания. Умолчание — "_nuxt/": только там имена
// содержат хеш содержимого. У оболочки, worker'а, манифеста и иконок
// такого имени нет, и вечный кэш для них означал бы, что обновление не
// доедет никогда.
func WithImmutable(prefixes ...string) Option {
	return func(c *config) { c.immutable = prefixes }
}

// WithoutCompression отключает предварительное сжатие. Нужно там, где
// сжимает слой перед приложением, — иначе память тратится дважды.
func WithoutCompression() Option {
	return func(c *config) { c.compress = false }
}

// WithMIME добавляет или переопределяет тип по расширению (с точкой).
func WithMIME(ext, typ string) Option {
	return func(c *config) {
		if c.mimes == nil {
			c.mimes = map[string]string{}
		}
		c.mimes[strings.ToLower(ext)] = typ
	}
}

// New читает статику целиком и готовит её к раздаче.
//
// Ошибка означает либо нечитаемую fs.FS, либо ErrNotBuilt — отсутствие
// оболочки. Второе стоит логировать предупреждением, а не падать.
func New(fsys fs.FS, opts ...Option) (*Handler, error) {
	cfg := config{
		index:       "index.html",
		immutable:   []string{"_nuxt/"},
		compress:    true,
		minCompress: 1024,
	}
	for _, o := range opts {
		o(&cfg)
	}

	h := &Handler{files: map[string]*file{}}
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		f := &file{
			body:  body,
			etag:  `"` + hex.EncodeToString(sum[:16]) + `"`,
			mime:  mimeType(name, &cfg),
			cache: cacheFor(name, &cfg),
		}
		if cfg.compress {
			f.gz = compress(name, body, cfg.minCompress)
		}
		h.files[name] = f
		h.bytes += len(body)

		// Страница /login лежит файлом login/index.html — отдаём его
		// прямо по адресу /login, без редиректа на слэш (причина 1
		// в описании пакета).
		if path.Base(name) == cfg.index {
			dir := path.Dir(name)
			if dir == "." {
				dir = ""
			}
			h.files[dir] = f
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	h.index = h.files[""]
	if h.index == nil {
		return nil, ErrNotBuilt
	}
	return h, nil
}

// Dist возвращает подкаталог собранной статики и проверяет, что она
// действительно собрана. Заменяет одинаковый web/embed.go в приложениях:
//
//	//go:embed all:dist
//	var embedded embed.FS
//	files, err := spa.Dist(embedded, "dist")
//
// Префикс all: в директиве обязателен: без него Go молча пропустит
// каталог _nuxt, куда Nuxt складывает весь JS и CSS, и бинарник соберётся
// без единого ассета.
func Dist(fsys fs.FS, dir string) (fs.FS, error) {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		return nil, err
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, ErrNotBuilt
	}
	return sub, nil
}

// Has сообщает, есть ли файл в собранной статике. Нужно для проверок
// вроде «а собрался ли service worker»: его отсутствие — не отказ
// запуска, но и не мелочь, потому что уже установленная PWA без него не
// обновится.
func (h *Handler) Has(name string) bool {
	_, ok := h.files[name]
	return ok
}

// Size — суммарный размер несжатой статики в байтах.
func (h *Handler) Size() int { return h.bytes }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}

	f := h.files[cleanPath(r.URL.Path)]
	if f == nil {
		// Откат на оболочку — только для навигации (причина 2).
		if !isNavigation(r) {
			http.NotFound(w, r)
			return
		}
		f = h.index
	}

	head := w.Header()
	head.Set("Content-Type", f.mime)
	head.Set("Cache-Control", f.cache)
	head.Set("ETag", f.etag)
	head.Set("X-Content-Type-Options", "nosniff")
	if f.gz != nil {
		head.Set("Vary", "Accept-Encoding")
	}

	if etagMatches(r.Header.Get("If-None-Match"), f.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	body := f.body
	if f.gz != nil && acceptsGzip(r) {
		body = f.gz
		head.Set("Content-Encoding", "gzip")
	}
	head.Set("Content-Length", strconv.Itoa(len(body)))

	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	w.Write(body)
}

// cleanPath приводит путь запроса к ключу карты. Выхода за пределы
// статики здесь быть не может: карта строится из fs.FS, и "../" просто
// не найдётся.
func cleanPath(p string) string {
	name := strings.TrimPrefix(p, "/")
	if cleaned := path.Clean(name); cleaned != "." {
		return cleaned
	}
	return ""
}

// isNavigation отличает переход по адресу от загрузки ресурса страницей.
//
// Sec-Fetch-Mode шлют все актуальные браузеры и не шлют curl и старьё,
// поэтому Accept остаётся запасным признаком.
func isNavigation(r *http.Request) bool {
	if mode := r.Header.Get("Sec-Fetch-Mode"); mode != "" {
		return mode == "navigate"
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// etagMatches разбирает If-None-Match: список через запятую, звёздочка и
// слабые валидаторы. Сравнение слабое — тела у нас всё равно неизменны.
func etagMatches(header, etag string) bool {
	if header == "" {
		return false
	}
	if strings.TrimSpace(header) == "*" {
		return true
	}
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		candidate = strings.TrimPrefix(candidate, "W/")
		if candidate == etag {
			return true
		}
	}
	return false
}

// acceptsGzip намеренно не разбирает q-значения: «gzip;q=0» в реальном
// трафике не встречается, а полноценный разбор Accept-Encoding — это
// втрое больше кода ради случая, которого нет.
func acceptsGzip(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
}

func cacheFor(name string, cfg *config) string {
	for _, prefix := range cfg.immutable {
		if strings.HasPrefix(name, prefix) {
			return CacheImmutable
		}
	}
	return CacheRevalidate
}

// extraTypes — то, чего нет ни во встроенной таблице Go, ни в образе без
// пакета mime-types: alpine и distroless приезжают без /etc/mime.types,
// и там mime.TypeByExtension отвечает пустой строкой на половину сборки.
// Своя таблица вместо mime.AddExtensionType ещё и потому, что библиотека
// не должна править глобальное состояние процесса.
var extraTypes = map[string]string{
	".webmanifest": "application/manifest+json",
	".map":         "application/json",
	".ico":         "image/x-icon",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".ttf":         "font/ttf",
	".otf":         "font/otf",
	".eot":         "application/vnd.ms-fontobject",
	".txt":         "text/plain; charset=utf-8",
	".webp":        "image/webp",
	".avif":        "image/avif",
}

func mimeType(name string, cfg *config) string {
	ext := strings.ToLower(path.Ext(name))
	if t, ok := cfg.mimes[ext]; ok {
		return t
	}
	if t, ok := extraTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}

// compressible — форматы, которые сжатие действительно уменьшает. png,
// woff2 и прочее уже сжаты внутри, и второй проход стоит процессора при
// нулевом выигрыше.
var compressible = map[string]bool{
	".html": true, ".js": true, ".mjs": true, ".css": true, ".json": true,
	".webmanifest": true, ".svg": true, ".txt": true, ".xml": true, ".map": true,
}

func compress(name string, body []byte, min int) []byte {
	// Короткие ответы сжимать незачем: заголовок gzip съедает выигрыш.
	if len(body) < min || !compressible[strings.ToLower(path.Ext(name))] {
		return nil
	}
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil
	}
	if _, err := w.Write(body); err != nil {
		return nil
	}
	if err := w.Close(); err != nil {
		return nil
	}
	// Если сжатие почти ничего не дало, отдавать две копии смысла нет.
	if buf.Len() >= len(body)*9/10 {
		return nil
	}
	return buf.Bytes()
}
