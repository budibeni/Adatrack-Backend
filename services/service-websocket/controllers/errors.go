package controllers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"ajb_gps/service-websocket/models"
)

// Error codes (PRD §8.1 / §3.1 / §9.1). Every failure the API can produce has a
// stable machine-readable code so the frontend can map it to a locale string.
const (
	CodeValidationError       = "VALIDATION_ERROR"
	CodeUnauthorized          = "UNAUTHORIZED"
	CodeInvalidCredentials    = "INVALID_CREDENTIALS"
	CodeTokenExpired          = "TOKEN_EXPIRED"
	CodeTokenRevoked          = "TOKEN_REVOKED"
	CodeTokenInvalid          = "TOKEN_INVALID"
	CodeForbidden             = "FORBIDDEN"
	CodePlatformOnly          = "PLATFORM_ONLY"
	CodePlatformScope         = "PLATFORM_SCOPE"
	CodePlatformRoleReserved  = "PLATFORM_ROLE_RESERVED"
	CodePasswordChangeNeeded  = "PASSWORD_CHANGE_REQUIRED"
	CodeAccountLocked         = "ACCOUNT_LOCKED"
	CodeAccountInactive       = "ACCOUNT_INACTIVE"
	CodeRateLimited           = "RATE_LIMITED"
	CodeVehicleNotFound       = "VEHICLE_NOT_FOUND"
	CodeUnauthorizedVehicle   = "UNAUTHORIZED_VEHICLE"
	CodeCompanyNotFound       = "COMPANY_NOT_FOUND"
	CodeCompanyExists         = "COMPANY_EXISTS"
	CodeUserNotFound          = "USER_NOT_FOUND"
	CodeUserExists            = "USER_EXISTS"
	CodeRouteNotFound         = "ROUTE_NOT_FOUND"
	CodeMethodNotAllowed      = "METHOD_NOT_ALLOWED"
	CodeInternalError         = "INTERNAL_ERROR"
	CodeServiceUnavailable    = "SERVICE_UNAVAILABLE"
	CodeWebSocketUnauthorized = "UNAUTHORIZED_WEBSOCKET"
)

// APIError is a failure that carries an HTTP status + PRD error_code.
type APIError struct {
	Status  int
	Code    string
	Message string
	// Fields holds per-field validation failures (PRD §8.5 rule 1).
	Fields map[string]string
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// NewAPIError builds an APIError.
func NewAPIError(status int, code, message string) *APIError {
	return &APIError{Status: status, Code: code, Message: message}
}

// invalidCredentials is the single failure answer for every bad login so an
// attacker cannot distinguish an unknown account from a wrong password
// (PRD §8.1 error_code catalogue, §9.1).
func invalidCredentials() *APIError {
	return NewAPIError(http.StatusUnauthorized, CodeInvalidCredentials, "invalid email or password")
}

// errUnauthorized builds a generic 401 (missing/invalid token).
func errUnauthorized(msg string) *APIError {
	return NewAPIError(http.StatusUnauthorized, CodeUnauthorized, msg)
}
func errForbidden(code, msg string) *APIError {
	return NewAPIError(http.StatusForbidden, code, msg)
}
func errNotFound(code, msg string) *APIError {
	return NewAPIError(http.StatusNotFound, code, msg)
}
func errConflict(code, msg string) *APIError {
	return NewAPIError(http.StatusConflict, code, msg)
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
