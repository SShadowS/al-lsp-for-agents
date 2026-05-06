package wrapper

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// LoadEnvFile reads ~/.al-lsp/.env (if it exists) and sets each KEY=VALUE
// in the process environment, skipping any key that is already set in the
// real environment. Real env vars always win over the file.
//
// Missing file: silent, returns nil. Malformed lines: silently skipped.
func LoadEnvFile() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return loadEnvFileFrom(filepath.Join(home, ".al-lsp", ".env"))
}

func loadEnvFileFrom(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip trailing comment (only if preceded by whitespace).
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue // no key, or "=value" line
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		// Strip surrounding quotes.
		if len(val) >= 2 {
			first, last := val[0], val[len(val)-1]
			if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if _, present := os.LookupEnv(key); present {
			continue
		}
		_ = os.Setenv(key, val)
	}
	return nil
}
