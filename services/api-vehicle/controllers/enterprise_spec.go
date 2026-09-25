package controllers

import (
	"fmt"
	"strings"
	"time"
)

// fieldKind is the storage kind of one enterprise field. It drives request
// coercion + validation AND the SQL parameter type, so a client can never send a
// string where a BIGINT is expected.
type fieldKind int

const (
	kindString fieldKind = iota
	kindInt
	kindFloat
	kindBool
	kindTime
)

// fieldSpec describes one writable column of an enterprise resource. Column names
// are compile-time constants (never user input), which is what makes the
// table-driven CRUD safe: the SQL identifier is taken from this whitelist only.
type fieldSpec struct {
	Column   string
	Kind     fieldKind
	Required bool
	MaxLen   int
	Enum     []string
}

// resourceSpec is the contract of one enterprise resource (PRD §5.10 B12).
type resourceSpec struct {
	Name      string // URL + audit resource segment
	Table     string // tenant table
	Entity    string // audit entity label
	Fields    []fieldSpec
	Search    []string // columns matched by ?search (ILIKE)
	OrderBy   string
	Immutable bool // append-only: no update/delete/restore (e.g. access log)
}

// lookupResource resolves a resource spec by URL segment.
func lookupResource(name string) (resourceSpec, bool) {
	spec, ok := enterpriseResources[strings.ToLower(strings.TrimSpace(name))]
	return spec, ok
}

// selectColumns is the projection (declared columns + housekeeping).
func (spec resourceSpec) selectColumns() []string {
	cols := []string{"id"}
	for _, f := range spec.Fields {
		cols = append(cols, f.Column)
	}
	return append(cols, "created_at", "updated_at")
}

// coerceField validates one value against its field spec.
func coerceField(field fieldSpec, raw any) (any, *APIError) {
	switch field.Kind {
	case kindString:
		text, ok := raw.(string)
		if !ok {
			return nil, fieldErr(field, "must be a string")
		}
		text = strings.TrimSpace(text)
		if field.MaxLen > 0 && len([]rune(text)) > field.MaxLen {
			return nil, fieldErr(field, fmt.Sprintf("must be at most %d characters", field.MaxLen))
		}
		if len(field.Enum) > 0 && !containsString(field.Enum, text) {
			return nil, fieldErr(field, "must be one of: "+strings.Join(field.Enum, " "))
		}
		if text == "" {
			return nil, nil
		}
		return text, nil
	case kindInt:
		number, ok := raw.(float64)
		if !ok || number != float64(int64(number)) {
			return nil, fieldErr(field, "must be an integer")
		}
		return int64(number), nil
	case kindFloat:
		number, ok := raw.(float64)
		if !ok {
			return nil, fieldErr(field, "must be a number")
		}
		return number, nil
	case kindBool:
		value, ok := raw.(bool)
		if !ok {
			return nil, fieldErr(field, "must be a boolean")
		}
		return value, nil
	case kindTime:
		text, ok := raw.(string)
		if !ok {
			return nil, fieldErr(field, "must be an RFC3339 timestamp or YYYY-MM-DD date")
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, nil
		}
		for _, layout := range []string{time.RFC3339, "2006-01-02"} {
			if parsed, err := time.Parse(layout, text); err == nil {
				return parsed, nil
			}
		}
		return nil, fieldErr(field, "must be an RFC3339 timestamp or YYYY-MM-DD date")
	default:
		return nil, fieldErr(field, "unsupported field type")
	}
}

// fieldErr builds a per-field VALIDATION_ERROR.
func fieldErr(field fieldSpec, message string) *APIError {
	return errValidation("request validation failed", map[string]string{field.Column: message})
}

// containsString is a tiny enum membership test.
func containsString(list []string, needle string) bool {
	for _, item := range list {
		if item == needle {
			return true
		}
	}
	return false
}

// normalize validates + coerces one JSON body into column→value pairs. Unknown
// keys are rejected (typo safety + no injection surface) and every value is
// checked against its field kind, length and enum.
//
// `partial` is true for PATCH: only the provided keys are returned and required
// fields may be omitted.
func (spec resourceSpec) normalize(payload map[string]any, partial bool) (map[string]any, *APIError) {
	byColumn := make(map[string]fieldSpec, len(spec.Fields))
	for _, f := range spec.Fields {
		byColumn[f.Column] = f
	}
	out := make(map[string]any, len(payload))
	for key, raw := range payload {
		field, ok := byColumn[key]
		if !ok {
			return nil, errValidation("unknown field for "+spec.Name,
				map[string]string{key: "not a writable field"})
		}
		if raw == nil {
			if field.Required && !partial {
				return nil, fieldErr(field, "is required")
			}
			out[key] = nil
			continue
		}
		value, apiErr := coerceField(field, raw)
		if apiErr != nil {
			return nil, apiErr
		}
		if value == nil && field.Required && !partial {
			return nil, fieldErr(field, "is required")
		}
		out[key] = value
	}
	if !partial {
		for _, f := range spec.Fields {
			if !f.Required {
				continue
			}
			if _, provided := out[f.Column]; !provided {
				return nil, fieldErr(f, "is required")
			}
		}
	}
	if len(out) == 0 {
		return nil, errValidation("empty request body", map[string]string{"body": "no writable field provided"})
	}
	return out, nil
}
