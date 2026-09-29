package controllers

// handlers_reports_export.go — CSV export of the Analysis → Reports views
// (PRD §5.10 §1.6). Gap C3 in docs/GAP-REGISTER.md: the on-demand summaries
// existed, the export did not.
//
// Design notes:
//   - The export reuses the SAME store calls as the JSON endpoints, so a report
//     can never disagree between formats.
//   - Values are written with encoding/csv (correct quoting for any future text
//     column) and prefixed with a UTF-8 BOM so Excel renders Indonesian
//     characters correctly.
//   - Errors are reported through the standard envelope; the error code list in
//     errors.go is unchanged (503/422 reuse the existing codes).

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// reportCSVFilename builds an attachment name, e.g. trips_20260901_20260929.csv.
func reportCSVFilename(kind string, from, to time.Time) string {
	return fmt.Sprintf("%s_%s_%s.csv", kind,
		from.UTC().Format("20060102"), to.UTC().Format("20060102"))
}

// writeCSV streams a CSV attachment. `rows` are already stringified so the
// renderer stays independent of the report models.
func writeCSV(c *gin.Context, filename string, header []string, rows [][]string) {
	var b strings.Builder
	b.WriteString("\ufeff") // BOM: Excel opens a UTF-8 CSV correctly by default.
	w := csv.NewWriter(&b)
	if err := w.Write(header); err != nil {
		respondError(c, errInternal("could not render the CSV export"))
		return
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			respondError(c, errInternal("could not render the CSV export"))
			return
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		respondError(c, errInternal("could not render the CSV export"))
		return
	}
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", []byte(b.String()))
}

// csvFloat renders a float without a trailing ".000000" for whole numbers.
func csvFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// handleTripReportExport implements GET /api/v1/reports/trips/export (CSV).
func (s *Service) handleTripReportExport(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	from, to, werr := analyticsWindow(c)
	if werr != nil {
		respondError(c, werr)
		return
	}
	report, err := s.enterprise.TripReport(c.Request.Context(), identity.companyCode, from, to)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if report == nil {
		respondError(c, errNotFound("REPORT_NOT_FOUND", "trip report not found"))
		return
	}
	rows := [][]string{{
		report.From, report.To,
		strconv.FormatInt(report.TripCount, 10),
		csvFloat(report.DistanceKm),
		csvFloat(report.AvgSpeedKmh),
		csvFloat(report.MaxSpeedKmh),
		strconv.FormatInt(report.StopCount, 10),
	}}
	writeCSV(c, reportCSVFilename("trips", from, to),
		[]string{"from", "to", "trip_count", "distance_km", "avg_speed_kmh", "max_speed_kmh", "stop_count"},
		rows)
}

// handleViolationReportExport implements GET /api/v1/reports/violations/export (CSV).
func (s *Service) handleViolationReportExport(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	from, to, werr := analyticsWindow(c)
	if werr != nil {
		respondError(c, werr)
		return
	}
	items, err := s.enterprise.ViolationReport(c.Request.Context(), identity.companyCode, from, to)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{item.EventType, item.Severity, strconv.FormatInt(item.Count, 10)})
	}
	writeCSV(c, reportCSVFilename("violations", from, to),
		[]string{"event_type", "severity", "count"}, rows)
}
