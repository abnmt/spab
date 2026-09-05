// Package dotenv читает файл вида KEY=VALUE и раскладывает его в
// окружение.
//
// Своя реализация вместо библиотеки: формат простой, а тянуть
// зависимость в модуль, который затевался ради нулевого графа
// зависимостей, не хочется.
package dotenv

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Load читает первый существующий файл из списка и выставляет из него
// переменные окружения.
//
// Переменные, уже заданные в окружении, не перезаписываются: это обычное
// поведение dotenv, и оно единственное разумное — иначе `KEY=x ./app`
// молча проигрывал бы файлу. Отсюда же и порядок работы с разбором
// аргументов: сначала Load, потом парсер — правило «.env не перекрывает
// окружение» соблюдается само собой.
//
// Возвращает путь прочитанного файла (пустой, если ни одного не нашлось)
// и имена применённых переменных — их стоит показать в логе, потому что
// «а откуда взялось это значение» иначе выясняется отладчиком.
func Load(paths ...string) (loaded string, applied []string) {
	for _, path := range paths {
		if path == "" {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			key, value, ok := parseLine(scanner.Text())
			if !ok {
				continue
			}
			if _, exists := os.LookupEnv(key); exists {
				continue // окружение сильнее файла
			}
			if err := os.Setenv(key, value); err == nil {
				applied = append(applied, key)
			}
		}
		return path, applied
	}
	return "", nil
}

// DefaultPaths — где искать файл: в текущем каталоге и рядом с
// бинарником. Первый найденный выигрывает.
func DefaultPaths() []string {
	paths := []string{".env"}
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), ".env"))
	}
	return paths
}

// FindUp ищет файл вверх по дереву каталогов, начиная с start (пустой
// start — текущий каталог). Нужно для корневого .env: Go-сервер
// запускают и из корня репозитория, и из web/ рядом с nuxt dev, и файл
// должен находиться одинаково в обоих случаях.
//
// Пустая строка означает, что файла нет нигде до корня файловой системы.
func FindUp(name, start string) string {
	dir := start
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return ""
		}
		dir = wd
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, name)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func parseLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")

	i := strings.Index(line, "=")
	if i <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:i])
	value = strings.TrimSpace(line[i+1:])

	if key == "" || !validKey(key) {
		return "", "", false
	}

	// Кавычки снимаем; внутри двойных разворачиваем \n и \".
	switch {
	case len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`):
		value = value[1 : len(value)-1]
		value = strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\"`, `"`, `\\`, `\`).Replace(value)
	case len(value) >= 2 && strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'"):
		value = value[1 : len(value)-1] // одинарные кавычки — без раскрытия
	default:
		// Комментарий в конце строки отсекаем только у незакавыченного
		// значения: в пароле решётка встречается сплошь и рядом.
		if j := strings.Index(value, " #"); j >= 0 {
			value = strings.TrimSpace(value[:j])
		}
	}
	return key, value, true
}

func validKey(key string) bool {
	for i, r := range key {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
