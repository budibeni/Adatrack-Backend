package internal

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// Dot-env file support.
//
// Setiap service memanggil LoadConfig() yang otomatis memuat file .env dari
// working directory sebelum membaca variabel environment. Dengan begitu semua
// konfigurasi yang terdaftar di PRD §7 bisa di-set lewat file .env (dev local)
// dan tetap bisa di-override oleh environment proses (staging/production).
//
// Urutan prioritas (rendah -> tinggi):
//   1. Default aman di kode (getEnv/getEnvInt fallback)
//   2. Nilai dari file dotenv (.env, .env.<adatrack_ENV>, ENV_FILE)
//   3. Variabel env proses (OS env) — SELALU menang, tidak pernah ditimpa
//
// File yang dicoba, dalam urutan:
//   1. ENV_FILE            (path eksplisit, absolut atau relatif ke CWD)
//   2. .env.<adatrack_ENV>    (mis. adatrack_ENV=staging -> .env.staging)
//   3. .env                (base default di working directory)
//
// File yang muncul belakangan boleh menimpa nilai dari file sebelumnya
// (selama env proses belum menset variabel itu).
// ---------------------------------------------------------------------------

const (
	// EnvFileVar memaksa path file env tertentu (mis. untuk staging/prod).
	EnvFileVar = "ENV_FILE"
	// adatrackEnvVar memilih file env spesifik environment: .env.<adatrack_ENV>.
	adatrackEnvVar = "adatrack_ENV"
	// DefaultEnvFile adalah nama file dotenv default di working directory.
	DefaultEnvFile = ".env"
)

// LoadEnvFiles memuat file dotenv ke dalam environment proses.
// Mengembalikan daftar path yang berhasil dimuat (dipakai untuk logging).
// Nilai dari file TIDAK menimpa environment variable yang sudah ter-set di OS.
func LoadEnvFiles() []string {
	// Setiap pemanggilan independen: buang jejak key dari pemanggilan
	// sebelumnya supaya semantik "OS env menang" tetap berlaku.
	resetDotenvTracker()

	loaded := make([]string, 0, 3)
	for _, path := range envFilePaths() {
		if !isRegularFile(path) {
			continue
		}
		if _, err := loadDotEnvFile(path); err != nil {
			// Tidak pernah silent drop: laporkan tapi terus ke file berikutnya.
			fmt.Fprintf(os.Stderr, "envfile: failed to load %s: %v\n", path, err)
			continue
		}
		loaded = append(loaded, path)
	}
	return loaded
}

// envFilePaths menyusun daftar path yang dicoba, sesuai urutan prioritas
// di atas.
func envFilePaths() []string {
	var paths []string
	if p := strings.TrimSpace(os.Getenv(EnvFileVar)); p != "" {
		paths = append(paths, p)
	}
	if env := sanitizeEnvName(os.Getenv(adatrackEnvVar)); env != "" {
		paths = append(paths, DefaultEnvFile+"."+env)
	}
	paths = append(paths, DefaultEnvFile)
	return paths
}

// LoadProjectEnv memuat file .env di akar proyek backend (backend/.env) — file
// .env TUNGGAL yang dipakai bersama oleh semua service. Mencari dari CWD ke atas
// sampai menemukan direktori akar backend (ditandai adanya subdir internal/ dan
// services/). Dipanggil oleh tiap service di main.go SEBELUM LoadConfig.
//
// Nilai dari file TIDAK menimpa env proses yang sudah ter-set (OS env menang),
// konsisten dengan LoadEnvFiles.
func LoadProjectEnv() []string {
	p := projectEnvPath()
	if p == "" {
		return nil
	}
	if n, err := loadDotEnvFile(p); err != nil {
		// Tidak pernah silent drop.
		fmt.Fprintf(os.Stderr, "envfile: failed to load project env %s: %v\n", p, err)
		return nil
	} else if n > 0 {
		return []string{p}
	}
	return []string{p}
}

// projectEnvPath mencari akar backend dari CWD ke atas (ditandai subdir
// internal/ + services/) dan mengembalikan path backend/.env (kosong bila
// tak ditemukan / bukan file reguler).
func projectEnvPath() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir := wd
	for {
		if isDir(filepath.Join(dir, "internal")) && isDir(filepath.Join(dir, "services")) {
			p := filepath.Join(dir, DefaultEnvFile)
			if isRegularFile(p) {
				return p
			}
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// sanitizeEnvName membatasi adatrack_ENV hanya karakter aman [A-Za-z0-9_.-]
// dan menolak nama dot ("." / "..") untuk menghindari path traversal via
// adatrack_ENV=../secret.
func sanitizeEnvName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// isRegularFile melaporkan apakah path menunjuk ke sebuah file reguler.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// loadDotEnvFile mem-parsing satu file KEY=VALUE dan mengekspor variabelnya
// lewat os.Setenv. Nilai yang sudah di-set oleh OS env (sebelum loader jalan)
// TIDAK ditimpa; nilai dari file loader boleh saling menimpa (file belakangan
// menang). Mengembalikan jumlah variabel yang berhasil di-set.
func loadDotEnvFile(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	set := 0
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		key, val, ok := parseDotEnvLine(line)
		if !ok {
			continue
		}
		if !canOverride(key) {
			// OS env menang; jangan timpa.
			continue
		}
		if err := os.Setenv(key, val); err != nil {
			return set, fmt.Errorf("setenv %s: %w", key, err)
		}
		markDotenvSet(key)
		set++
	}
	if err := scanner.Err(); err != nil {
		return set, fmt.Errorf("scan %s: %w", path, err)
	}
	return set, nil
}

var (
	dotenvMu      sync.Mutex
	dotenvSetKeys = map[string]bool{}
)

// resetDotenvTracker mengosongkan catatan key yang di-set oleh loader
// (dipanggil sekali di awal tiap LoadEnvFiles).
func resetDotenvTracker() {
	dotenvMu.Lock()
	defer dotenvMu.Unlock()
	dotenvSetKeys = map[string]bool{}
}

// canOverride melaporkan apakah loader boleh mengeset key: boleh jika key
// belum pernah di-set sama sekali ATAU sudah pernah di-set oleh loader
// sebelumnya (file belakangan menimpa file lebih dulu). Key dari OS env
// (sebelum loader jalan) tidak pernah boleh ditimpa.
func canOverride(key string) bool {
	dotenvMu.Lock()
	defer dotenvMu.Unlock()
	if _, fromOS := os.LookupEnv(key); !fromOS {
		return true
	}
	return dotenvSetKeys[key]
}

// markDotenvSet mencatat bahwa sebuah key ditetapkan oleh loader.
func markDotenvSet(key string) {
	dotenvMu.Lock()
	defer dotenvMu.Unlock()
	dotenvSetKeys[key] = true
}

// parseDotEnvLine mengekstrak KEY=VALUE dari satu baris file dotenv.
// Mendukung: komentar (# /* awalan baris dan inline untuk nilai tak dalam
// kutip), prefix "export ", dan nilai dalam kutip '...' / "...".
func parseDotEnvLine(line string) (key, value string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
		return "", "", false
	}

	body := strings.TrimPrefix(trimmed, "export ")
	body = strings.TrimLeft(body, " \t")

	eq := strings.Index(body, "=")
	if eq <= 0 {
		return "", "", false
	}

	keyCand := strings.TrimSpace(body[:eq])
	if !isValidEnvKey(keyCand) {
		return "", "", false
	}
	return keyCand, parseEnvValue(strings.TrimSpace(body[eq+1:])), true
}

// isValidEnvKey memvalidasi nama variabel: diawali huruf/underscore,
// hanya berisi alnum/underscore.
func isValidEnvKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_'
		if !valid || (i == 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// parseEnvValue menormalkan nilai: membuang kutip, memakai komentar inline
// pada nilai tanpa kutip, dan trim whitespace.
func parseEnvValue(val string) string {
	val = strings.TrimSpace(val)
	if len(val) >= 2 {
		if (val[0] == '"' && val[len(val)-1] == '"') ||
			(val[0] == '\'' && val[len(val)-1] == '\'') {
			return val[1 : len(val)-1]
		}
	}
	if len(val) >= 1 && (val[0] == '"' || val[0] == '\'') {
		// Kutip pembuka tanpa penutup: buang kutipnya saja.
		val = val[1:]
	}
	// Nilai tanpa kutip boleh punya komentar inline (diawali spasi + #).
	if i := strings.Index(val, " #"); i >= 0 {
		val = val[:i]
	} else if i := strings.Index(val, "\t#"); i >= 0 {
		val = val[:i]
	}
	return strings.TrimSpace(val)
}
