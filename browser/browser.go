// Package browser открывает интерфейс в браузере после старта.
//
// Мелочь, но заметная: приложение запускают руками из терминала, и лишний
// шаг «скопируй адрес, открой вкладку» раздражает. При этом на сервере
// браузера нет, поэтому по умолчанию открываем только там, где это
// осмысленно, и никогда не считаем неудачу ошибкой запуска.
package browser

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Open ждёт, пока сервер начнёт отвечать, и открывает адрес в браузере.
// Возвращает причину, по которой не стал открывать, — её стоит
// залогировать, чтобы «почему ничего не открылось» не превращалось
// в загадку.
func Open(ctx context.Context, url string) error {
	if reason := skipReason(); reason != "" {
		return errors.New(reason)
	}
	// Открывать раньше, чем сервер поднялся, — верный способ показать
	// пользователю страницу с ошибкой соединения.
	if err := WaitReady(ctx, url, 10*time.Second); err != nil {
		return err
	}
	return launch(url)
}

// skipReason объясняет, почему открывать не надо. Пустая строка — надо.
func skipReason() string {
	switch runtime.GOOS {
	case "darwin", "windows":
		return ""
	case "linux":
		// В контейнере и по SSH браузера нет. Xvfb (DISPLAY=:99) формально
		// есть, но открывать там окно бессмысленно.
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return "нет графической сессии (DISPLAY не задан)"
		}
		if inContainer() {
			return "запуск в контейнере"
		}
		return ""
	default:
		return "платформа " + runtime.GOOS + " не поддерживается"
	}
}

func inContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	data, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	s := string(data)
	return strings.Contains(s, "docker") || strings.Contains(s, "containerd") ||
		strings.Contains(s, "kubepods") || strings.Contains(s, "lxc")
}

// WaitReady опрашивает адрес, пока не ответит или не выйдет время.
func WaitReady(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: time.Second}

	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("сервер не ответил за %s", timeout)
}

func launch(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	// Отвязываем вывод: иначе xdg-open засоряет лог приложения.
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("не запустить браузер: %w", err)
	}
	// Процесс браузера живёт своей жизнью, но зомби оставлять не стоит.
	go func() { _ = cmd.Wait() }()
	return nil
}

// LocalURL превращает адрес прослушивания в адрес, который имеет смысл
// открывать: 0.0.0.0 и :: в браузере не работают.
//
// Адрес без порта возвращается как есть — угадывать за приложение, на
// каком порту оно на самом деле слушает, хуже, чем показать то, что дали.
func LocalURL(addr string) string {
	addr = strings.TrimSpace(addr)
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}
