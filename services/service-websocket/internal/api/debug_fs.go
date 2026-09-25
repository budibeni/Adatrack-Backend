package api

import (
	"net/http"
	"os"
	"path/filepath"
)

func (h *Handler) DebugFS(w http.ResponseWriter, r *http.Request) {
	candidates := []string{
		"../../database/migrations/company_pg",
		"database/migrations/company_pg",
		"/app/database/migrations/company_pg",
	}
	
	results := make(map[string]interface{})
	for _, c := range candidates {
		info, err := os.Stat(c)
		if err != nil {
			results[c] = err.Error()
			continue
		}
		
		files, _ := filepath.Glob(filepath.Join(c, "*.up.sql"))
		results[c] = map[string]interface{}{
			"is_dir": info.IsDir(),
			"count": len(files),
			"files": files,
		}
	}
	
	h.writeJSON(w, http.StatusOK, results)
}
