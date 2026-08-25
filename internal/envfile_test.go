package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestFile tulis file dengan permission aman di direktori test.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestParseDotEnvLine(t *testing.T) {
	cases := []struct {
		line      string
		wantKey   string
		wantValue string
		wantOK    bool
	}{
		{"KEY=value", "KEY", "value", true},
		{"KEY = value", "KEY", "value", true},
		{"export KEY=value", "KEY", "value", true},
		{"export KEY=value # inline comment", "KEY", "value", true},
		{`KEY="quoted value"`, "KEY", "quoted value", true},
		{"KEY='single value'", "KEY", "single value", true},
		{"NATS_URL=nats://127.0.0.1:4222", "NATS_URL", "nats://127.0.0.1:4222", true},
		{"VAL= value dengan spasi", "VAL", "value dengan spasi", true},
		{`EMPTY=""`, "EMPTY", "", true},
		{"EMPTY=", "EMPTY", "", true},
		{"# komentar", "", "", false},
		{"; komentar", "", "", false},
		{"", "", "", false},
		{"GARIS-TANPA-SAMA", "", "", false},
		{"1BADKEY=value", "", "", false},
		{"BAD KEY=value", "", "", false},
	}
	for _, tc := range cases {
		key, val, ok := parseDotEnvLine(tc.line)
		if ok != tc.wantOK {
			t.Errorf("parseDotEnvLine(%q) ok = %v, want %v", tc.line, ok, tc.wantOK)
			continue
		}
		if ok && (key != tc.wantKey || val != tc.wantValue) {
			t.Errorf("parseDotEnvLine(%q) = (%q,%q), want (%q,%q)", tc.line, key, val, tc.wantKey, tc.wantValue)
		}
	}
}

func TestLoadDotEnvBasic(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	restore := withEnv(map[string]string{
		EnvFileVar:     "",
		adatrackEnvVar:    "",
		"DOTENV_A":     "",
		"DOTENV_B":     "",
		"DOTENV_EMPTY": "",
	})
	defer restore()

	writeTestFile(t, filepath.Join(tmp, DefaultEnvFile), `
# komentar utk dokumentasi
DOTENV_A=hello
export DOTENV_B="world dengan spasi"
DOTENV_EMPTY=
`)

	loaded := LoadEnvFiles()
	if len(loaded) != 1 {
		t.Fatalf("expected 1 loaded file, got %v", loaded)
	}
	if got := os.Getenv("DOTENV_A"); got != "hello" {
		t.Errorf("DOTENV_A = %q, want hello", got)
	}
	if got := os.Getenv("DOTENV_B"); got != "world dengan spasi" {
		t.Errorf("DOTENV_B = %q, want world dengan spasi", got)
	}
}

func TestLoadDotEnvOSEnvWins(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	restore := withEnv(map[string]string{
		EnvFileVar:     "",
		adatrackEnvVar:    "",
		"WIN_TEST_OS":  "from-os",
		"WIN_TEST_DOT": "",
	})
	defer restore()

	writeTestFile(t, filepath.Join(tmp, DefaultEnvFile),
		"WIN_TEST_OS=from-dotenv\nWIN_TEST_DOT=only-dotenv\n")
	LoadEnvFiles()

	if got := os.Getenv("WIN_TEST_OS"); got != "from-os" {
		t.Errorf("WIN_TEST_OS = %q, want from-os (OS env harus menang)", got)
	}
	if got := os.Getenv("WIN_TEST_DOT"); got != "only-dotenv" {
		t.Errorf("WIN_TEST_DOT = %q, want only-dotenv", got)
	}
}

func TestLoadDotEnvadatrackEnvDelta(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	restore := withEnv(map[string]string{
		EnvFileVar:      "",
		adatrackEnvVar:     "development",
		"adatrack_ONLY":    "",
		"BASE_ONLY":     "",
		"BASE_OVERRIDE": "",
	})
	defer restore()

	writeTestFile(t, filepath.Join(tmp, DefaultEnvFile), `BASE_ONLY=from-base
BASE_OVERRIDE=base-value
`)
	writeTestFile(t, filepath.Join(tmp, DefaultEnvFile+".development"), `BASE_OVERRIDE=dev-value
adatrack_ONLY=from-dev
`)

	loaded := LoadEnvFiles()
	if len(loaded) != 2 {
		t.Fatalf("expected 2 loaded files, got %v", loaded)
	}
	if got := os.Getenv("BASE_ONLY"); got != "from-base" {
		t.Errorf("BASE_ONLY = %q, want from-base", got)
	}
	if got := os.Getenv("adatrack_ONLY"); got != "from-dev" {
		t.Errorf("adatrack_ONLY = %q, want from-dev", got)
	}
	// .env dibaca SETELAH .env.development -> nilai base menang untuk
	// variabel yang sama (file belakangan menimpa yang lebih dulu).
	if got := os.Getenv("BASE_OVERRIDE"); got != "base-value" {
		t.Errorf("BASE_OVERRIDE = %q, want base-value (base dibaca terakhir)", got)
	}
}

func TestLoadEnvFilesDoesNotOverrideOS(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	restore := withEnv(map[string]string{
		EnvFileVar:    "",
		adatrackEnvVar:   "",
		"SERVER_ADDR": ":9999",
		"TCP_PORT":    "",
	})
	defer restore()

	writeTestFile(t, filepath.Join(tmp, DefaultEnvFile), `SERVER_ADDR=:1111
TCP_PORT=7000
`)

	LoadEnvFiles()

	if got := os.Getenv("SERVER_ADDR"); got != ":9999" {
		t.Errorf("SERVER_ADDR = %q, want :9999 (OS env menang)", got)
	}
	if got := os.Getenv("TCP_PORT"); got != "7000" {
		t.Errorf("TCP_PORT = %q, want 7000 (dari .env)", got)
	}
}

func TestLoadDotEnvExplicitPath(t *testing.T) {
	tmp := t.TempDir()
	secret := t.TempDir()
	t.Chdir(tmp)

	restore := withEnv(map[string]string{
		EnvFileVar:  filepath.Join(secret, "custom.env"),
		adatrackEnvVar: "",
		"EXPLICIT":  "",
	})
	defer restore()

	writeTestFile(t, filepath.Join(secret, "custom.env"), "EXPLICIT=from-explicit\n")
	loaded := LoadEnvFiles()
	if len(loaded) != 1 {
		t.Fatalf("expected 1 loaded file (ENV_FILE), got %v", loaded)
	}
	if got := os.Getenv("EXPLICIT"); got != "from-explicit" {
		t.Errorf("EXPLICIT = %q, want from-explicit (dari ENV_FILE)", got)
	}
}

func TestLoadConfigFromDotenv(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	restore := withEnv(map[string]string{
		EnvFileVar:  "",
		adatrackEnvVar: "",
		"TCP_PORT":  "",
	})
	defer restore()

	writeTestFile(t, filepath.Join(tmp, DefaultEnvFile), "TCP_PORT=9500\n")

	c := LoadConfig()
	if c.TCP.Port != "9500" {
		t.Errorf("LoadConfig TCP.Port = %q, want 9500 (dari .env)", c.TCP.Port)
	}
}

func TestSanitizeEnvName(t *testing.T) {
	cases := map[string]string{
		"staging":   "staging",
		"PROD":      "PROD",
		"dev.local": "dev.local",
		"../secret": ".._secret", // '/' diganti '_'
		"a b":       "a_b",       // spasi diganti '_'
	}
	for in, want := range cases {
		if got := sanitizeEnvName(in); got != want {
			t.Errorf("sanitizeEnvName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeEnvNameDangerous(t *testing.T) {
	// Path traversal tidak boleh lolos sebagai nama file env.
	for _, in := range []string{"../x", "a/b", "a\\b", "a b", ".."} {
		got := sanitizeEnvName(in)
		if got == in {
			t.Errorf("sanitizeEnvName(%q) harus diubah, got unchanged", in)
		}
		if strings.ContainsAny(got, "/\\") {
			t.Errorf("sanitizeEnvName(%q) = %q masih mengandung path separator", in, got)
		}
		if got == "." || got == ".." {
			t.Errorf("sanitizeEnvName(%q) = %q menghasilkan dot path", in, got)
		}
	}
}

func TestLoadDotEnvCRLF(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	restore := withEnv(map[string]string{
		EnvFileVar:    "",
		adatrackEnvVar:   "",
		"WINDOWS_VAR": "",
	})
	defer restore()

	// Simulasikan file .env hasil Windows (CRLF).
	raw := "WINDOWS_VAR=windows-value\r\n"
	writeTestFile(t, filepath.Join(tmp, DefaultEnvFile), raw)
	LoadEnvFiles()
	if got := os.Getenv("WINDOWS_VAR"); got != "windows-value" {
		t.Errorf("WINDOWS_VAR = %q, want windows-value", got)
	}
}

func TestProjectEnvPathWalksUpToBackendRoot(t *testing.T) {
	// Bangun struktur sementara yang meniru akar backend (internal/ + services/).
	tmp := t.TempDir()
	root := filepath.Join(tmp, "backend")
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "services", "worker-live"), 0o755); err != nil {
		t.Fatal(err)
	}
	// backend/.env harus ada agar projectEnvPath mengembalikannya.
	writeTestFile(t, filepath.Join(root, DefaultEnvFile), "ROOT_ONLY=1\n")
	// CWD di dalam services/worker-live → naik sampai backend/.
	t.Chdir(filepath.Join(root, "services", "worker-live"))

	p := projectEnvPath()
	want := filepath.Join(root, DefaultEnvFile)
	if p != want {
		t.Errorf("projectEnvPath() = %q, want %q", p, want)
	}
}

func TestLoadProjectEnvFromServiceDir(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "backend")
	for _, d := range []string{"internal", "services/worker-live"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(filepath.Join(root, "services", "worker-live"))

	restore := withEnv(map[string]string{
		EnvFileVar:    "",
		adatrackEnvVar:   "",
		"PROJ_ONLY":   "",
		"PROJ_OS_WIN": "",
		"REDIS_ADDR":  "",
	})
	defer restore()

	writeTestFile(t, filepath.Join(root, DefaultEnvFile), "PROJ_ONLY=single\nPROJ_OS_WIN=from-file\nREDIS_ADDR=127.0.0.1:6390\n")

	// OS env menang.
	os.Setenv("PROJ_OS_WIN", "from-os")
	defer os.Unsetenv("PROJ_OS_WIN")

	loaded := LoadProjectEnv()
	if len(loaded) != 1 || loaded[0] != filepath.Join(root, DefaultEnvFile) {
		t.Fatalf("LoadProjectEnv() = %v, want [%s]", loaded, filepath.Join(root, DefaultEnvFile))
	}
	if got := os.Getenv("PROJ_ONLY"); got != "single" {
		t.Errorf("PROJ_ONLY = %q, want 'single' (dari backend/.env)", got)
	}
	if got := os.Getenv("PROJ_OS_WIN"); got != "from-os" {
		t.Errorf("PROJ_OS_WIN = %q, want 'from-os' (OS env menang)", got)
	}
	if got := os.Getenv("REDIS_ADDR"); got != "127.0.0.1:6390" {
		t.Errorf("REDIS_ADDR = %q, want 127.0.0.1:6390", got)
	}
}

func TestEnvOr(t *testing.T) {
	restore := withEnv(map[string]string{"MY_UNIQUE_KEY": ""})
	defer restore()

	if got := EnvOr("MY_UNIQUE_KEY", ":1111"); got != ":1111" {
		t.Errorf("EnvOr empty = %q, want :1111", got)
	}
	os.Setenv("MY_UNIQUE_KEY", ":2222")
	defer os.Unsetenv("MY_UNIQUE_KEY")
	if got := EnvOr("MY_UNIQUE_KEY", ":1111"); got != ":2222" {
		t.Errorf("EnvOr set = %q, want :2222", got)
	}
}

func TestConfigureLoggingUnknownLevel(t *testing.T) {
	restore := withEnv(map[string]string{"LOG_LEVEL": "super-duper"})
	defer restore()
	ConfigureLogging()
}
