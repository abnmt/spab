package dotenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, ".env", strings.Join([]string{
		"# комментарий",
		"",
		"PLAIN=значение",
		"export EXPORTED=есть",
		`QUOTED="строка # с решёткой"`,
		`ESCAPED="первая\nвторая"`,
		"SINGLE='как есть\\n'",
		"TRAILING=значение # хвостовой комментарий",
		"FROM_ENV=из файла",
		"невалидный ключ=1",
		"БЕЗРАВНО",
	}, "\n"))

	t.Setenv("FROM_ENV", "из окружения")
	for _, k := range []string{"PLAIN", "EXPORTED", "QUOTED", "ESCAPED", "SINGLE", "TRAILING"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	loaded, applied := Load(filepath.Join(dir, "нет.env"), path)
	if loaded != path {
		t.Fatalf("прочитан %q, ждали %q", loaded, path)
	}

	want := map[string]string{
		"PLAIN":    "значение",
		"EXPORTED": "есть",
		"QUOTED":   "строка # с решёткой",
		"ESCAPED":  "первая\nвторая",
		"SINGLE":   `как есть\n`,
		"TRAILING": "значение",
	}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q, ждали %q", k, got, v)
		}
	}

	// Главное правило: окружение сильнее файла. Иначе `KEY=x ./app`
	// молча проигрывает .env, и это ищут отладчиком.
	if got := os.Getenv("FROM_ENV"); got != "из окружения" {
		t.Errorf("файл перекрыл окружение: FROM_ENV = %q", got)
	}
	for _, k := range applied {
		if k == "FROM_ENV" {
			t.Errorf("FROM_ENV попал в applied, хотя не применялся")
		}
	}
	if len(applied) != len(want) {
		t.Errorf("applied = %v, ждали %d имён", applied, len(want))
	}
}

func TestLoadMissing(t *testing.T) {
	loaded, applied := Load("", filepath.Join(t.TempDir(), "нет.env"))
	if loaded != "" || applied != nil {
		t.Fatalf("нашли несуществующий файл: %q, %v", loaded, applied)
	}
}

func TestFindUp(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "web", "app")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	want := write(t, root, ".env", "KEY=1")

	if got := FindUp(".env", deep); got != want {
		t.Fatalf("FindUp = %q, ждали %q", got, want)
	}
	if got := FindUp("нет-такого", deep); got != "" {
		t.Fatalf("FindUp нашёл %q", got)
	}
}
