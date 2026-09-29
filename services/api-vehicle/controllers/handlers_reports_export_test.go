package controllers

// handlers_reports_export_test.go — hermetic coverage for the CSV export helpers
// (gap C3). The contract that matters: a download attachment with the right
// headers, a UTF-8 BOM for Excel, and correct CSV quoting.

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestReportCSVFilenameFormat(t *testing.T) {
	from := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 29, 18, 30, 0, 0, time.UTC)

	if got, want := reportCSVFilename("trips", from, to), "trips_20260901_20260929.csv"; got != want {
		t.Fatalf("reportCSVFilename = %q, want %q", got, want)
	}
}

func TestCsvFloatOmitsTrailingZeros(t *testing.T) {
	cases := map[float64]string{
		12:       "12",
		12.5:     "12.5",
		4.440000: "4.44",
		0:        "0",
	}
	for in, want := range cases {
		if got := csvFloat(in); got != want {
			t.Fatalf("csvFloat(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestWriteCSVProducesDownloadableAttachment(t *testing.T) {
	c, rec := testContext(http.MethodGet, "/api/v1/reports/trips/export", "", adminIdentity())

	writeCSV(c, "trips_20260901_20260929.csv",
		[]string{"from", "to", "trip_count"},
		[][]string{
			{"2026-09-01T00:00:00Z", "2026-09-29T00:00:00Z", "12"},
			{"with,comma", "quote\"inside", "3"},
		})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("Content-Type = %q, want text/csv…", ct)
	}
	disposition := rec.Header().Get("Content-Disposition")
	if !strings.Contains(disposition, "attachment") || !strings.Contains(disposition, "trips_20260901_20260929.csv") {
		t.Fatalf("Content-Disposition = %q, want an attachment with the file name", disposition)
	}

	body := rec.Body.String()
	if !strings.HasPrefix(body, "\ufeff") {
		t.Fatal("CSV is missing the UTF-8 BOM (Excel would mis-render Indonesian text)")
	}
	if !strings.Contains(body, "from,to,trip_count") {
		t.Fatalf("CSV header missing; body=%q", body)
	}
	// encoding/csv must quote a value containing a comma / quote.
	if !strings.Contains(body, `"with,comma"`) {
		t.Fatalf("comma inside a value was not quoted; body=%q", body)
	}
	if !strings.Contains(body, `"quote""inside"`) {
		t.Fatalf("embedded quote was not escaped by doubling; body=%q", body)
	}
}
