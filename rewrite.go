package main

import (
	"os"
	"strings"
)

func main() {
	b, _ := os.ReadFile("services/service-websocket/internal/api/handlers.go")
	lines := strings.Split(string(b), "\n")
	
	out := []string{}
	skip := false
	for _, line := range lines {
		if strings.Contains(line, `if migrationDir != "" {`) {
			skip = true
			out = append(out, `		if migrationDir != "" {
			files, _ := filepath.Glob(filepath.Join(migrationDir, "*.up.sql"))
			if len(files) == 0 {
				logger.Log.Error("No migration files found in directory", "dir", migrationDir)
				h.writeError(w, http.StatusInternalServerError, "MIGRATION_NOT_FOUND", "No migration files found to provision tenant")
				return
			}
			sort.Strings(files)
			for _, file := range files {
				sqlBytes, err := os.ReadFile(file)
				if err == nil {
					execSQL := fmt.Sprintf("SET search_path TO %s, public; %s", schema, string(sqlBytes))
					_, err := tx.Exec(r.Context(), execSQL)
					if err != nil {
						logger.Log.Error("Migration error in company schema", "file", file, "err", err)
						h.writeError(w, http.StatusInternalServerError, "MIGRATION_EXEC_FAILED", "Failed to execute migration: "+filepath.Base(file)+" error: "+err.Error())
						return
					}
				}
			}
		} else {
			h.writeError(w, http.StatusInternalServerError, "MIGRATION_DIR_NOT_FOUND", "Migration directory not found")
			return
		}`)
		}
		if skip && strings.Contains(line, `// 5. Grant admin access`) {
			skip = false
		}
		if !skip {
			out = append(out, line)
		}
	}
	
	os.WriteFile("services/service-websocket/internal/api/handlers.go", []byte(strings.Join(out, "\n")), 0644)
}
