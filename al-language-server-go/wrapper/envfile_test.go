package wrapper

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFileBasic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := `# comment
KEY1=value1
KEY2 = value2
KEY3="quoted value"
KEY4='single quoted'
EMPTY=
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	// Pre-existing env var must NOT be overwritten by file.
	t.Setenv("KEY1", "from-env")
	// KEY5 only in env, no file entry
	t.Setenv("KEY5", "alive")

	if err := loadEnvFileFrom(path); err != nil {
		t.Fatalf("loadEnvFileFrom: %v", err)
	}

	cases := []struct {
		key, want string
	}{
		{"KEY1", "from-env"},      // env wins
		{"KEY2", "value2"},        // file populates
		{"KEY3", "quoted value"},  // double-quotes stripped
		{"KEY4", "single quoted"}, // single-quotes stripped
		{"KEY5", "alive"},         // pre-existing untouched
		{"EMPTY", ""},             // empty value OK
	}
	for _, c := range cases {
		if got := os.Getenv(c.key); got != c.want {
			t.Errorf("%s = %q, want %q", c.key, got, c.want)
		}
	}
}

func TestLoadEnvFileMissingIsNotError(t *testing.T) {
	if err := loadEnvFileFrom(filepath.Join(t.TempDir(), "does-not-exist.env")); err != nil {
		t.Errorf("missing file should be silent, got %v", err)
	}
}

func TestLoadEnvFileSkipsBadLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := `GOOD=ok
this line has no equals
=novalue
ALSOGOOD=fine
`
	os.WriteFile(path, []byte(content), 0600)
	if err := loadEnvFileFrom(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("GOOD") != "ok" {
		t.Errorf("GOOD not loaded")
	}
	if os.Getenv("ALSOGOOD") != "fine" {
		t.Errorf("ALSOGOOD not loaded after bad lines")
	}
}
