package pbspa

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"

	"github.com/abnmt/spab/spa"
)

// Тест собирает настоящий роутер PocketBase: проверяется не логика spa
// (она проверена у себя), а то, что catch-all встаёт, HEAD доезжает
// без отдельного маршрута и более специфичный /api/… его перебивает.
func mux(t *testing.T) http.Handler {
	t.Helper()

	r := router.NewRouter(func(w http.ResponseWriter, req *http.Request) (*core.RequestEvent, router.EventCleanupFunc) {
		e := new(core.RequestEvent)
		e.Response, e.Request = w, req
		return e, nil
	})

	files := fstest.MapFS{
		"index.html":          {Data: []byte("<!doctype html>оболочка")},
		"dict/index.html":     {Data: []byte("<!doctype html>справочники")},
		"_nuxt/app.a1b2c3.js": {Data: []byte("console.log(1)")},
	}

	r.GET("/api/health", func(e *core.RequestEvent) error {
		e.Response.WriteHeader(http.StatusTeapot)
		return nil
	})

	h, err := Bind(&core.ServeEvent{Router: r}, files)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if !h.Has("index.html") {
		t.Fatal("Bind вернул обработчик без оболочки")
	}

	built, err := r.BuildMux()
	if err != nil {
		t.Fatalf("BuildMux: %v", err)
	}
	return built
}

func TestBind(t *testing.T) {
	m := mux(t)

	cases := []struct {
		method, target, wantBody string
		want                     int
	}{
		{http.MethodGet, "/", "<!doctype html>оболочка", http.StatusOK},
		{http.MethodGet, "/dict?tab=locations", "<!doctype html>справочники", http.StatusOK},
		{http.MethodGet, "/_nuxt/app.a1b2c3.js", "console.log(1)", http.StatusOK},
		{http.MethodHead, "/", "", http.StatusOK},
		{http.MethodGet, "/api/health", "", http.StatusTeapot},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.target, nil)
		r.Header.Set("Sec-Fetch-Mode", "navigate")
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)

		if w.Code != c.want {
			t.Errorf("%s %s: код %d, ждали %d", c.method, c.target, w.Code, c.want)
		}
		if c.wantBody != "" && w.Body.String() != c.wantBody {
			t.Errorf("%s %s: тело %q, ждали %q", c.method, c.target, w.Body.String(), c.wantBody)
		}
	}
}

func TestBindNotBuilt(t *testing.T) {
	r := router.NewRouter(func(w http.ResponseWriter, req *http.Request) (*core.RequestEvent, router.EventCleanupFunc) {
		return new(core.RequestEvent), nil
	})
	if _, err := Bind(&core.ServeEvent{Router: r}, fstest.MapFS{"readme.txt": {Data: []byte("пусто")}}); err != spa.ErrNotBuilt {
		t.Fatalf("ошибка %v, ждали spa.ErrNotBuilt", err)
	}
}
