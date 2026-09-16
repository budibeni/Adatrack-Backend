package controllers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"ajb_gps/api-vehicle/models"
)

// Error codes (PRD §8.1 + §3.1; superset of the service-websocket catalogue).
const (
	CodeValidationError      = "VALIDATION_ERROR"
	CodeUnauthorized         = "UNAUTHORIZED"
	CodeTokenExpired         = "TOKEN_EXPIRED"
	CodeTokenRevoked         = "TOKEN_REVOKED"
	CodeTokenInvalid         = "TOKEN_INVALID"
	CodeForbidden            = "FORBIDDEN"
	CodePlatformScope        = "PLATFORM_SCOPE"
	CodePasswordChangeNeeded = "PASSWORD_CHANGE_REQUIRED"
	CodeAccountInactive      = "ACCOUNT_INACTIVE"
	CodeRateLimited          = "RATE_LIMITED"
	CodeVehicleNotFound      = "VEHICLE_NOT_FOUND"
	CodeUnauthorizedVehicle  = "UNAUTHORIZED_VEHICLE"
	CodeGeofenceNotFound     = "GEOFENCE_NOT_FOUND"
	CodeRouteNotFound        = "ROUTE_NOT_FOUND"
	CodeAssignmentNotFound   = "ASSIGNMENT_NOT_FOUND"
	CodeInvalidTransition    = "INVALID_STATUS_TRANSITION"
	CodeSpeedConfigNotFound  = "SPEED_CONFIG_NOT_FOUND"
	CodeAlertNotFound        = "ALERT_NOT_FOUND"
	CodeConflict             = "CONFLICT"
	CodeMethodNotAllowed     = "METHOD_NOT_ALLOWED"
	CodeEndpointNotFound     = "ENDPOINT_NOT_FOUND"
	CodeInternalError        = "INTERNAL_ERROR"
	CodeServiceUnavailable   = "SERVICE_UNAVAILABLE"
)

// APIError is a failure that carries an HTTP status + PRD error_code.
type APIError struct {
	Status  int
	Code    string
	Message string
	Fields  map[string]string
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// NewAPIError builds an APIError.
func NewAPIError(status int, code, message string) *APIError {
	return &APIError{Status: status, Code: code, Message: message}
}

func errUnauthorized(msg string) *APIError {
	return NewAPIError(http.StatusUnauthorized, CodeUnauthorized, msg)
}
func errForbidden(code, msg string) *APIError {
	return NewAPIError(http.StatusForbidden, code, msg)
}
func errNotFound(code, msg string) *APIError {
	return NewAPIError(http.StatusNotFound, code, msg)
}
func errConflict(msg string) *APIError {
	return NewAPIError(http.StatusConflict, CodeConflict, msg)
}
func errValidation(msg string, fields map[string]string) *APIError {
	return &APIError{Status: http.StatusBadRequest, Code: CodeValidationError, Message: msg, Fields: fields}
}
func errBadRequest(code, msg string) *APIError {
	return NewAPIError(http.StatusBadRequest, code, msg)
}
func errRateLimited(msg string) *APIError {
	return NewAPIError(http.StatusTooManyRequests, CodeRateLimited, msg)
}
func errInternal(msg string) *APIError {
	return NewAPIError(http.StatusInternalServerError, CodeInternalError, msg)
}
func errUnavailable(msg string) *APIError {
	return NewAPIError(http.StatusServiceUnavailable, CodeServiceUnavailable, msg)
}

// respondOK writes the PRD §8.1 success envelope.
func respondOK(c *gin.Context, data any, pagination *models.Pagination) {
	c.JSON(http.StatusOK, models.Envelope{Status: "success", Data: data, Pagination: pagination})
}

// respondCreated writes a 201 with the same success envelope.
func respondCreated(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, models.Envelope{Status: "success", Data: data})
}

// respondError writes the PRD §8.1 error envelope. It is the ONLY way a handler
// reports a failure, so the contract can never drift per endpoint.
func respondError(c *gin.Context, err error) {
	apiErr := asAPIError(err)
	c.JSON(apiErr.Status, models.ErrorEnvelope{
		Status:    "error",
		ErrorCode: apiErr.Code,
		Message:   apiErr.Message,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Errors:    apiErr.Fields,
	})
}

// asAPIError normalises any error into an APIError (unknown errors become a
// generic 500 — internals are never leaked to the client).
func asAPIError(err error) *APIError {
	if err == nil {
		return errInternal("unknown error")
	}
	if apiErr, ok := err.(*APIError); ok {
		return apiErr
	}
	return errInternal("internal error")
}
