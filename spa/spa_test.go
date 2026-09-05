package spa

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// Каждая ошибка из истории четырёх проектов должна иметь здесь свой тест:
// библиотека затевалась ровно ради того, чтобы чинить их один раз.

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html>оболочка")},
		"dict/index.html":      {Data: []byte("<!doctype html>справочники")},
		"_nuxt/app.a1b2c3.js":  {Data: []byte("console.log(1)")},
		"sw.js":                {Data: []byte("self.addEventListener()")},
		"manifest.webmanifest": {Data: []byte(`{"name":"app"}`)},
		"favicon.ico":          {Data: []byte("\x00\x00\x01\x00")},
		"_nuxt/big.d4e5f6.css": {Data: bytes.Repeat([]byte("a:hover{color:red}\n"), 200)},
	}
}

func handler(t *testing.T, opts ...Option) *Handler {
	t.Helper()
	h, err := New(testFS(), opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h
}

func get(t *testing.T, h *Handler, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

var navigation = map[string]string{"Sec-Fetch-Mode": "navigate"}
var asset = map[string]string{"Sec-Fetch-Mode": "no-cors"}

// Ссылка /dict?tab=locations обязана открыть справочники на нужной вкладке.
// Редирект на /dict/ терял строку запроса, и вкладка молча пропадала.
func TestDirectoryServedInPlaceKeepsQuery(t *testing.T) {
	h := handler(t)
	w := get(t, h, "/dict?tab=locations", navigation)

	if w.Code != http.StatusOK {
		t.Fatalf("код %d, ждали 200 (редирект тут и есть чинимая ошибка)", w.Code)
	}
	if got := w.Header().Get("Location"); got != "" {
		t.Fatalf("редирект на %q: строка запроса потеряется", got)
	}
	if !strings.Contains(w.Body.String(), "справочники") {
		t.Fatalf("отдали не dict/index.html: %q", w.Body.String())
	}
}

// Промах по хешированному ассету — честная 404. Оболочка со статусом 200
// вместо скрипта роняет старую вкладку на MIME-проверке вместо перезагрузки.
func TestMissingAssetIsNotFound(t *testing.T) {
	h := handler(t)
	for _, target := range []string{"/_nuxt/old.000000.js", "/sw.js.map", "/api/absent"} {
		w := get(t, h, target, asset)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: код %d, ждали 404 (иначе браузер получит HTML вместо ресурса)", target, w.Code)
		}
	}
}

// Ненайденный ресурс без заголовков браузера (curl, старый клиент) тоже
// не должен получать оболочку, если это явно не навигация.
func TestFallbackOnlyForNavigation(t *testing.T) {
	h := handler(t)

	if w := get(t, h, "/thread/42", navigation); w.Code != http.StatusOK {
		t.Errorf("навигация на клиентский маршрут: код %d, ждали оболочку", w.Code)
	}
	if w := get(t, h, "/thread/42", map[string]string{"Accept": "text/html"}); w.Code != http.StatusOK {
		t.Errorf("Accept: text/html как запасной признак навигации: код %d", w.Code)
	}
	if w := get(t, h, "/thread/42", map[string]string{"Accept": "application/json"}); w.Code != http.StatusNotFound {
		t.Errorf("запрос данных: код %d, ждали 404", w.Code)
	}
}

// Манифест PWA обязан приехать как application/manifest+json: как
// text/plain браузер молча отказывается ставить приложение.
func TestWebmanifestContentType(t *testing.T) {
	h := handler(t)
	w := get(t, h, "/manifest.webmanifest", asset)

	if got := w.Header().Get("Content-Type"); got != "application/manifest+json" {
		t.Fatalf("Content-Type %q — PWA не установится", got)
	}
}

func TestCacheRules(t *testing.T) {
	h := handler(t)
	cases := map[string]string{
		"/_nuxt/app.a1b2c3.js":  CacheImmutable,
		"/sw.js":                CacheRevalidate,
		"/manifest.webmanifest": CacheRevalidate,
		"/":                     CacheRevalidate,
		"/dict":                 CacheRevalidate,
	}
	for target, want := range cases {
		w := get(t, h, target, navigation)
		if got := w.Header().Get("Cache-Control"); got != want {
			t.Errorf("%s: Cache-Control %q, ждали %q", target, got, want)
		}
	}
}

// Без валидатора оболочка не ревалидируется, и обновление доезжает
// до браузера по его собственной эвристике, то есть когда-нибудь.
func TestETagGives304(t *testing.T) {
	h := handler(t)
	first := get(t, h, "/", navigation)
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag не выставлен: ревалидировать нечем")
	}

	second := get(t, h, "/", map[string]string{"Sec-Fetch-Mode": "navigate", "If-None-Match": etag})
	if second.Code != http.StatusNotModified {
		t.Fatalf("код %d, ждали 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Fatalf("304 с телом в %d байт", second.Body.Len())
	}

	if w := get(t, h, "/", map[string]string{"Sec-Fetch-Mode": "navigate", "If-None-Match": `W/"чужой"`}); w.Code != http.StatusOK {
		t.Fatalf("чужой ETag: код %d, ждали 200", w.Code)
	}
}

func TestGzip(t *testing.T) {
	h := handler(t)
	w := get(t, h, "/_nuxt/big.d4e5f6.css", map[string]string{
		"Sec-Fetch-Mode":  "no-cors",
		"Accept-Encoding": "gzip",
	})

	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("крупный CSS отдан без сжатия")
	}
	if w.Header().Get("Vary") != "Accept-Encoding" {
		t.Errorf("нет Vary: прокси отдаст сжатое тело клиенту без gzip")
	}
	zr, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatalf("тело не разжимается: %v", err)
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("тело не разжимается: %v", err)
	}
	if !bytes.HasPrefix(body, []byte("a:hover")) {
		t.Fatalf("разжали не то: %q", body[:20])
	}

	// Клиенту без gzip — несжатое, и без Content-Encoding.
	plain := get(t, h, "/_nuxt/big.d4e5f6.css", asset)
	if plain.Header().Get("Content-Encoding") != "" {
		t.Errorf("клиент не просил gzip, а получил сжатое")
	}
}

func TestSmallFilesAreNotCompressed(t *testing.T) {
	h := handler(t)
	w := get(t, h, "/_nuxt/app.a1b2c3.js", map[string]string{
		"Sec-Fetch-Mode":  "no-cors",
		"Accept-Encoding": "gzip",
	})
	if w.Header().Get("Content-Encoding") != "" {
		t.Fatalf("четырнадцать байт сжаты — заголовок gzip больше выигрыша")
	}
}

func TestHeadHasHeadersButNoBody(t *testing.T) {
	h := handler(t)
	r := httptest.NewRequest(http.MethodHead, "/", nil)
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("код %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("HEAD с телом")
	}
	if w.Header().Get("Content-Length") == "" {
		t.Errorf("HEAD без Content-Length бесполезен для проверки живости")
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h := handler(t)
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("код %d, ждали 405", w.Code)
	}
	if w.Header().Get("Allow") == "" {
		t.Errorf("405 без Allow")
	}
}

// Выход за пределы встроенной статики невозможен по построению: карта
// собрана из fs.FS, обращения к файловой системе нет вовсе.
func TestNoTraversal(t *testing.T) {
	h := handler(t)
	for _, target := range []string{"/../go.mod", "/..%2fgo.mod", "/dict/../../etc/passwd"} {
		w := get(t, h, target, asset)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: код %d, ждали 404", target, w.Code)
		}
	}
}

func TestNotBuilt(t *testing.T) {
	if _, err := New(fstest.MapFS{"readme.txt": {Data: []byte("пусто")}}); err != ErrNotBuilt {
		t.Fatalf("ошибка %v, ждали ErrNotBuilt", err)
	}
	if _, err := Dist(fstest.MapFS{"dist/readme.txt": {Data: []byte("пусто")}}, "dist"); err != ErrNotBuilt {
		t.Fatalf("Dist: ошибка %v, ждали ErrNotBuilt", err)
	}
	files, err := Dist(fstest.MapFS{"dist/index.html": {Data: []byte("ok")}}, "dist")
	if err != nil {
		t.Fatalf("Dist: %v", err)
	}
	if _, err := New(files); err != nil {
		t.Fatalf("New поверх Dist: %v", err)
	}
}

func TestHasAndSize(t *testing.T) {
	h := handler(t)
	if !h.Has("sw.js") {
		t.Error("Has не видит sw.js — на этой проверке держится предупреждение о PWA")
	}
	if h.Has("sw.js.map") {
		t.Error("Has нашёл то, чего нет")
	}
	if h.Size() == 0 {
		t.Error("Size пустой")
	}
}

func TestOptions(t *testing.T) {
	h, err := New(testFS(), WithoutCompression(), WithImmutable("_nuxt/", "assets/"), WithMIME(".ico", "image/vnd.microsoft.icon"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	w := get(t, h, "/_nuxt/big.d4e5f6.css", map[string]string{
		"Sec-Fetch-Mode":  "no-cors",
		"Accept-Encoding": "gzip",
	})
	if w.Header().Get("Content-Encoding") != "" {
		t.Errorf("WithoutCompression не подействовал")
	}
	if got := get(t, h, "/favicon.ico", asset).Header().Get("Content-Type"); got != "image/vnd.microsoft.icon" {
		t.Errorf("WithMIME не подействовал: %q", got)
	}
}
