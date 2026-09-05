// Package pbspa вешает раздачу SPA на роутер PocketBase.
//
// Отдельный модуль, а не подпакет github.com/abnmt/spab: требования в Go
// живут на уровне модуля, поэтому общий go.mod притащил бы PocketBase со
// всеми его зависимостями и тем, кому нужен один spa.
package pbspa

import (
	"io/fs"

	"github.com/pocketbase/pocketbase/core"

	"github.com/abnmt/spab/spa"
)

// Bind регистрирует catch-all на всё, что не перехватили более
// специфичные маршруты: /api/… и /_/… объявлены точнее, чем "/{path...}",
// поэтому админка и API остаются на месте.
//
// Возвращает обработчик — по нему приложение может проверить, что в
// сборку попало то, чего оно ждёт:
//
//	h, err := pbspa.Bind(se, files)
//	if err != nil {
//	    log.Warn("фронтенд не встроен", "err", err)
//	} else if !h.Has("sw.js") {
//	    log.Warn("service worker не встроен: PWA не установится, а уже установленная не обновится")
//	}
//
// Ошибка spa.ErrNotBuilt отказом запуска быть не должна: API и админка
// PocketBase нужны ровно там, где отлаживают бэкенд без фронта.
func Bind(se *core.ServeEvent, files fs.FS, opts ...spa.Option) (*spa.Handler, error) {
	h, err := spa.New(files, opts...)
	if err != nil {
		return nil, err
	}
	// Только GET: http.ServeMux, на котором собран роутер PocketBase,
	// направляет сюда и HEAD — отдельный маршрут для него не нужен и
	// приведёт к конфликту шаблонов.
	se.Router.GET("/{path...}", func(e *core.RequestEvent) error {
		h.ServeHTTP(e.Response, e.Request)
		return nil
	})
	return h, nil
}
