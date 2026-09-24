package main
import (
	"os"
	"strings"
)
func main() {
	b, _ := os.ReadFile("services/service-websocket/internal/api/handlers.go")
	s := string(b)
	oldStr := `		if migrationDir != "" {
			files, _ := filepath.Glob(filepath.Join(migrationDir, "*.up.sql"))
			sort.Strings(files)
			for _, file := range files {
				sqlBytes, err := os.ReadFile(file)
				if err == nil {
					execSQL := fmt.Sprintf("SET search_path TO %s, public; %s", schema, string(sqlBytes))
					_, err := tx.Exec(r.Context(), execSQL)
					if err != nil {
						logger.Log.Error("Migration error in company schema", "file", file, "err", err)
					}
				}
			}
		}`
	
	newStr := `		if migrationDir != "" {
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
			logger.Log.Error("Migration directory not found")
			h.writeError(w, http.StatusInternalServerError, "MIGRATION_DIR_NOT_FOUND", "Migration directory not found")
			return
		}`
	
	s = strings.Replace(s, oldStr, newStr, 1)
	os.WriteFile("services/service-websocket/internal/api/handlers.go", []byte(s), 0644)
}
