package controllers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"ajb_gps/api-vehicle/models"
)

// parsePagination validates `page`/`limit` (PRD §8.5 rule 4).
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
				map[string]string{"limit": "must be <= " + strconv.Itoa(max)})
		}
		limit = parsed
	}
	return page, limit, nil
}

// parseIncludeDeleted validates the `include_deleted` flag (PRD §6.0.1; the
// Admin-only guard is applied by requireAdmin on the restore endpoints and by
// the read handlers' role check).
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

// pathID parses the `:id` path parameter.
func pathID(c *gin.Context) (int64, *APIError) {
	id, err := parsePositiveInt(c.Param("id"))
	if err != nil {
		return 0, errValidation("path id must be a positive integer",
			map[string]string{"id": "invalid"})
	}
	return id, nil
}

// parsePositiveInt parses a strictly positive base-10 integer.
func parsePositiveInt(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("not a positive integer")
	}
	return id, nil
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
	case "gte":
		return "must be greater than or equal to " + fe.Param()
	case "lte":
		return "must be less than or equal to " + fe.Param()
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
