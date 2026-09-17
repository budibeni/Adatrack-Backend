package controllers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"adatrack_gps/service-websocket/models"
)

// errNATS is reported by the readiness probe when the NATS client is disconnected.
var errNATS = errors.New("nats: disconnected")

// parsePagination validates `page`/`limit` (PRD §8.5 rule 4: limit is capped at
// API_MAX_PAGE_SIZE and page must be >= 1).
func (s *Service) parsePagination(c *gin.Context) (int, int, *APIError) {
	page := 1
	limit := s.settings.DefaultPageSize
	if limit <= 0 {
		limit = 100
	}

	if raw := strings.TrimSpace(c.Query("page")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return 0, 0, errValidation("page must be an integer >= 1", map[string]string{"page": "invalid"})
		}
		page = parsed
	}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return 0, 0, errValidation("limit must be an integer >= 1", map[string]string{"limit": "invalid"})
		}
		if max := s.settings.MaxPageSize; max > 0 && parsed > max {
			return 0, 0, errValidation("limit exceeds the maximum page size",
				map[string]string{"limit": "must be <= " + itoa(int64(s.settings.MaxPageSize))})
		}
		limit = parsed
	}
	return page, limit, nil
}

// parseIncludeDeleted validates the `include_deleted` flag (PRD §6.0.1: only
// SuperAdmin/Admin may see soft-deleted rows, and doing so is audited).
func parseIncludeDeleted(c *gin.Context) (bool, *APIError) {
	raw := strings.TrimSpace(c.Query("include_deleted"))
	if raw == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, errValidation("include_deleted must be a boolean",
			map[string]string{"include_deleted": "invalid"})
	}
	return value, nil
}

// parseTimeRange validates `from`/`to` (RFC3339 or date), enforces from <= to and
// the maximum history window (PRD §8.5 rules 2 and 4).
func (s *Service) parseTimeRange(c *gin.Context, defaultWindow time.Duration) (time.Time, time.Time, *APIError) {
	now := time.Now().UTC()
	to := now
	from := now.Add(-defaultWindow)

	if raw := strings.TrimSpace(c.Query("to")); raw != "" {
		parsed, err := parseTimestamp(raw)
		if err != nil {
			return time.Time{}, time.Time{}, errValidation("to must be RFC3339 or YYYY-MM-DD",
				map[string]string{"to": "invalid"})
		}
		to = parsed
	}
	if raw := strings.TrimSpace(c.Query("from")); raw != "" {
		parsed, err := parseTimestamp(raw)
		if err != nil {
			return time.Time{}, time.Time{}, errValidation("from must be RFC3339 or YYYY-MM-DD",
				map[string]string{"from": "invalid"})
		}
		from = parsed
	}
	if from.After(to) {
		return time.Time{}, time.Time{}, errValidation("from must be <= to",
			map[string]string{"from": "must be before to"})
	}
	if days := s.settings.HistoryMaxRangeDays; days > 0 && to.Sub(from) > time.Duration(days)*24*time.Hour {
		return time.Time{}, time.Time{}, errValidation("requested range exceeds HISTORY_MAX_RANGE_DAYS",
			map[string]string{"from": "range must be <= " + itoa(int64(days)) + " days"})
	}
	return from, to, nil
}

// parseTimestamp accepts RFC3339 or a plain date (UTC midnight).
func parseTimestamp(raw string) (time.Time, error) {
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts.UTC(), nil
	}
	return time.ParseInLocation("2006-01-02", raw, time.UTC)
}

// bindJSON binds and validates a request body, converting binding failures into
// the PRD §8.1/§8.5 VALIDATION_ERROR with a per-field error map.
func bindJSON(c *gin.Context, dst any) *APIError {
	if err := c.ShouldBindJSON(dst); err != nil {
		fields := map[string]string{}
		var verrs validator.ValidationErrors
		if errors.As(err, &verrs) {
			for _, fe := range verrs {
				fields[strings.ToLower(fe.Field())] = validationMessage(fe)
			}
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return errValidation("request body too large", map[string]string{"body": "too large"})
		}
		if len(fields) == 0 {
			fields["body"] = "malformed JSON body"
		}
		return errValidation("request validation failed", fields)
	}
	return nil
}

// validationMessage renders one validator failure for the client.
func validationMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "email":
		return "must be a valid email"
	case "min":
		return "must be at least " + fe.Param()
	case "max":
		return "must be at most " + fe.Param()
	case "oneof":
		return "must be one of: " + fe.Param()
	case "len":
		return "must be exactly " + fe.Param() + " characters"
	case "alpha":
		return "must contain letters only"
	case "gt":
		return "must be greater than " + fe.Param()
	case "dive":
		return "contains an invalid element"
	default:
		return "is invalid"
	}
}

// pagination builds the PRD §8.1 pagination block.
func pagination(page, limit int, total int64) *models.Pagination {
	return &models.Pagination{Page: page, Limit: limit, Total: total}
}
