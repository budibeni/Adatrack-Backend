// Package validate holds the shared input-validation primitives of the B10
// hardening pass (PRD §8.5 "Input Validation (WAJIB — seluruh input)" and §9.6
// "Aplikasi Aman dari Serangan").
//
// The rules are deliberately small, pure and reusable so an HTTP handler, a
// NATS consumer and a device decoder can apply EXACTLY the same policy:
//
//   - a closed whitelist instead of a blocklist,
//   - length bounds on every free-text field,
//   - no control characters (log/terminal injection),
//   - numeric ranges on identifiers and pagination,
//   - identifiers that are safe to build subjects/keys with.
//
// Every function returns a descriptive error and never panics; callers map the
// error onto the PRD §8.1 VALIDATION_ERROR envelope.
package validate

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Errors returned by this package (wrapped with field context).
var (
	ErrRequired     = errors.New("value is required")
	ErrTooShort     = errors.New("value is too short")
	ErrTooLong      = errors.New("value is too long")
	ErrBadFormat    = errors.New("value has an invalid format")
	ErrControlChars = errors.New("value contains control characters")
	ErrOutOfRange   = errors.New("value is out of range")
	ErrNotAllowed   = errors.New("value is not in the allowed set")
)

// Text validates a free-text field: required/optional, bounded length, no control
// characters (except the newline/tab of multi-line notes when allowBreaks).
func Text(field, value string, min, max int, required, allowBreaks bool) error {
	v := strings.TrimSpace(value)
	if v == "" {
		if required {
			return fmt.Errorf("%s: %w", field, ErrRequired)
		}
		return nil
	}
	if len(v) < min {
		return fmt.Errorf("%s: %w (min %d)", field, ErrTooShort, min)
	}
	if len(v) > max {
		return fmt.Errorf("%s: %w (max %d)", field, ErrTooLong, max)
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			if allowBreaks && (r == '\n' || r == '\t' || r == '\r') {
				continue
			}
			return fmt.Errorf("%s: %w", field, ErrControlChars)
		}
	}
	return nil
}

// IMEI validates a device IMEI: exactly 15 decimal digits (the only form the
// anti-spoofing allowlist `master.tm_vehicle_imei_map` can resolve, FR-1.4).
func IMEI(value string) error {
	v := strings.TrimSpace(value)
	if v == "" {
		return fmt.Errorf("imei: %w", ErrRequired)
	}
	if len(v) != 15 {
		return fmt.Errorf("imei: %w (15 digits)", ErrBadFormat)
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return fmt.Errorf("imei: %w (digits only)", ErrBadFormat)
		}
	}
	return nil
}

// Coordinates validates a WGS-84 pair. Zero/zero is accepted (a device without a
// fix), which is why the range check is inclusive.
func Coordinates(lat, lon float64) error {
	if lat < -90 || lat > 90 {
		return fmt.Errorf("lat: %w (-90..90)", ErrOutOfRange)
	}
	if lon < -180 || lon > 180 {
		return fmt.Errorf("lon: %w (-180..180)", ErrOutOfRange)
	}
	return nil
}

// IntRange validates a numeric bound (ids, intervals, thresholds).
func IntRange(field string, value, min, max int) error {
	if value < min || value > max {
		return fmt.Errorf("%s: %w (%d..%d)", field, ErrOutOfRange, min, max)
	}
	return nil
}

// OneOf enforces a closed whitelist (PRD §8.5: never a blocklist).
func OneOf(field, value string, allowed ...string) error {
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return fmt.Errorf("%s: %w (%s)", field, ErrNotAllowed, strings.Join(allowed, ", "))
}

// Pagination validates page/limit after the service has defaulted them.
func Pagination(page, limit, maxLimit int) error {
	if err := IntRange("page", page, 1, 1_000_000); err != nil {
		return err
	}
	return IntRange("limit", limit, 1, maxLimit)
}

// SubjectToken validates a value that is embedded in a NATS subject
// (`command.request.<company>`, `alert.<category>.<company>`): upper-case letters,
// digits and underscore only, so a crafted tenant code can never inject a wildcard
// or split the subject.
func SubjectToken(field, value string) error {
	v := strings.TrimSpace(value)
	if v == "" {
		return fmt.Errorf("%s: %w", field, ErrRequired)
	}
	if len(v) > 20 {
		return fmt.Errorf("%s: %w (max 20)", field, ErrTooLong)
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
		default:
			return fmt.Errorf("%s: %w (A-Z, 0-9, _)", field, ErrBadFormat)
		}
	}
	return nil
}

// SearchTerm validates a free-text search box: bounded length + no control
// characters. Wildcards (`%`, `_`) are NOT rejected here — the storage layer
// escapes them; the guard exists to keep the value small and printable.
func SearchTerm(value string, max int) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return Text("search", value, 0, max, false, false)
}
