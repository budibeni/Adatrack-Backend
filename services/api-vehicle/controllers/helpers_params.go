package controllers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// parseIDParam reads the numeric :id param; writes 400 on failure.
func parseIDParam(c *gin.Context) (uint64, bool) {
	id, err := fmtParseUint(c.Param("id"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "id must be a number")
		return 0, false
	}
	return id, true
}

// fmtParseUint parses base-10 uint64.
func fmtParseUint(s string) (uint64, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	var n uint64
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("not numeric: %s", s)
		}
		n = n*10 + uint64(s[i]-'0')
	}
	return n, nil
}

// isDuplicateErr reports MySQL duplicate-key errors (1062).
func isDuplicateErr(err error) bool {
	return err != nil && contains(err.Error(), "Duplicate entry")
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// timeFmt renders a NullTime as RFC3339 or "" when NULL.
func timeFmt(t sql.NullTime) string {
	if !t.Valid {
		return ""
	}
	return t.Time.Format(timeLayoutRFC3339)
}

const timeLayoutRFC3339 = "2006-01-02T15:04:05Z07:00"
