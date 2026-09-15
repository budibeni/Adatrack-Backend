package internal

import (
	"bufio"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// projectRoot is the detected backend root (the directory holding database/ and
// the variant env file); used to resolve relative migration paths when a service
// is started from its own subdirectory.
var projectRoot string

// ProjectRoot returns the detected backend root ("" when unknown).
func ProjectRoot() string { return projectRoot }

// ResolvePath resolves a relative path against the project root so commands such
// as `go run ./services/<svc>` from inside services/<svc>/ still find the
// database/ artifacts (migrations + seeds).
func ResolvePath(p string) string {
	if p == "" || filepath.IsAbs(p) || projectRoot == "" {
		return p
	}
	return filepath.Join(projectRoot, p)
}

// LoadProjectEnv loads the variant env file so a service can be started directly
// on a dev host (`go run .` / ./bin/<service> from services/<name>/) without
// exporting every variable.
//
// Search: the current directory and up to 3 parents are scanned (so a service
// started from its own folder still finds backend/.env.local), preferring
// .env.<COMPOSE_VARIANT> over .env. The directory containing database/ is
// remembered as the project root for relative path resolution.
//
// Already-set process variables ALWAYS win (the file never overrides the
// environment), keeping the "no mixed config" rule (PRD §7/§14.1): running under
// docker compose or Coolify must not be affected by a developer's local file.
func LoadProjectEnv() {
	variant := os.Getenv("COMPOSE_VARIANT")
	names := make([]string, 0, 2)
	if variant != "" {
		names = append(names, ".env."+variant)
	}
	names = append(names, ".env")

	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for depth := 0; depth < 4; depth++ {
		if projectRoot == "" && isProjectRoot(dir) {
			projectRoot = dir
		}
		for _, name := range names {
			path := filepath.Join(dir, name)
			f, err := os.Open(filepath.Clean(path))
			if err != nil {
				continue
			}
			loaded := applyEnvFile(f)
			_ = f.Close()
			if projectRoot == "" {
				projectRoot = dir
			}
			slog.Debug("env file loaded", "file", path, "vars_applied", loaded,
				"project_root", projectRoot)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

// isProjectRoot reports whether dir looks like the backend root.
func isProjectRoot(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "database", "migrations")); err == nil {
		return true
	}
	return false
}

// applyEnvFile parses KEY=VALUE lines into the process environment.
func applyEnvFile(f *os.File) int {
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	applied := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue // environment wins over the file
		}
		if err := os.Setenv(key, value); err == nil {
			applied++
		}
	}
	return applied
}
