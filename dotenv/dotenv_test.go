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

// Разбор строки — там, где .env расходится с «просто split по =»:
// пароль с решёткой, адрес с двоеточиями и ключ, которого не бывает.
func TestParseLine(t *testing.T) {
	cases := []struct {
		in         string
		key, value string
		ok         bool
	}{
		{`TMDB_API_KEY=abc123`, "TMDB_API_KEY", "abc123", true},
		{`  SPACED = value  `, "SPACED", "value", true},
		{`export EXPORTED=1`, "EXPORTED", "1", true},
		{`QUOTED="hello world"`, "QUOTED", "hello world", true},
		{`SINGLE='raw \n value'`, "SINGLE", `raw \n value`, true},
		{`ESCAPED="line\nbreak"`, "ESCAPED", "line\nbreak", true},
		{`WITH_COMMENT=value # пояснение`, "WITH_COMMENT", "value", true},
		{`EMPTY=`, "EMPTY", "", true},
		{`URL=socks5://user:pass@host:1080`, "URL", "socks5://user:pass@host:1080", true},

		// Пароль с решёткой — обычное дело, и он не должен обрезаться.
		{`RT_PASSWORD=p#ssw0rd`, "RT_PASSWORD", "p#ssw0rd", true},
		{`RT_PASSWORD="pass with # inside"`, "RT_PASSWORD", "pass with # inside", true},

		{`# просто комментарий`, "", "", false},
		{``, "", "", false},
		{`   `, "", "", false},
		{`БЕЗ_РАВНО`, "", "", false},
		{`=значение`, "", "", false},
		{`1BAD=x`, "", "", false},
		{`BAD-KEY=x`, "", "", false},
	}

	for _, c := range cases {
		key, value, ok := parseLine(c.in)
		if ok != c.ok {
			t.Errorf("parseLine(%q): ok = %v, ждали %v", c.in, ok, c.ok)
			continue
		}
		if ok && (key != c.key || value != c.value) {
			t.Errorf("parseLine(%q) = (%q, %q), ждали (%q, %q)",
				c.in, key, value, c.key, c.value)
		}
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
