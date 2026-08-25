package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const batchN = 1000

// batchInsert membangun statement(s) INSERT batch dengan ON DUPLICATE KEY UPDATE.
func batchInsert(table string, cols []string, rows [][]string, onDup string) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(rows); i += batchN {
		end := i + batchN
		if end > len(rows) {
			end = len(rows)
		}
		fmt.Fprintf(&b, "\nINSERT INTO %s (%s) VALUES\n", table, strings.Join(cols, ", "))
		for j := i; j < end; j++ {
			if j > i {
				b.WriteString(",\n")
			}
			b.WriteString("  (")
			b.WriteString(strings.Join(rows[j], ", "))
			b.WriteString(")")
		}
		if onDup != "" {
			fmt.Fprintf(&b, "\nON DUPLICATE KEY UPDATE %s;\n", onDup)
		} else {
			b.WriteString(";\n")
		}
	}
	return b.String()
}

func writeFile(path, header, body string) error {
	return os.WriteFile(path, []byte(header+body), 0644)
}

func headerNotice(title string, src string) string {
	return "-- ============================================================================\n" +
		"-- " + title + "\n" +
		"-- AUTO-GENERATED oleh backend/database/tools/genregions (jangan edit manual).\n" +
		"-- Sumber: " + src + "\n" +
		"-- Idempotent: INSERT ... ON DUPLICATE KEY UPDATE.\n" +
		"-- ============================================================================\n\n"
}

func writeOut(outDir, file, title, src, body string) string {
	p := filepath.Join(outDir, file)
	must(writeFile(p, headerNotice(title, src), body))
	return p
}
