package controllers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
)

// rbacDenied counts access denials per reason (PRD §10.1).
var rbacDenied = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "api_vehicle_rbac_denied_total",
	Help: "Requests denied by the api-vehicle auth/RBAC layer, per reason",
}, []string{"reason"})

// httpErrors feeds `http_errors_total{status,error_code}` (PRD §10.1).
var httpErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "http_errors_total",
	Help: "HTTP error responses, per status and error_code",
}, []string{"status", "error_code"})

// liveStateErrors counts Redis read failures while enriching REST list/detail
// responses with the live state (PRD §10.1). Read failures degrade gracefully
// (the fleet list is still returned) but are never silent — same contract and
// counter name as service-websocket `livestate.go`.
var liveStateErrors = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "live_state_read_errors_total",
	Help: "Redis live-state read failures (graceful degradation: response served without live data)",
})

// RegisterMetrics registers the api-vehicle collectors.
func RegisterMetrics(reg prometheus.Registerer) {
	if reg == nil {
		return
	}
	reg.MustRegister(rbacDenied, httpErrors, liveStateErrors)
}

// apiRateLimitMiddleware enforces PRD §8.4 (100 requests / minute / user).
func (s *Service) apiRateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.settings.APIRateLimit <= 0 || s.settings.APIRateWindow <= 0 {
			c.Next()
			return
		}
		key := "adatrack_gps:auth:api:" + c.ClientIP()
		if claims, ok := currentClaims(c); ok {
			key = "adatrack_gps:auth:api:user:" + strconv.FormatInt(claims.UserID, 10)
		}
		n, err := s.kv.Incr(c.Request.Context(), key)
		if err != nil {
			respondError(c, errUnavailable("rate limiter unavailable"))
			c.Abort()
			return
		}
		if n == 1 {
			if err := s.kv.Expire(c.Request.Context(), key, s.settings.APIRateWindow); err != nil {
				respondError(c, errUnavailable("rate limiter unavailable"))
				c.Abort()
				return
			}
		}
		if n > int64(s.settings.APIRateLimit) {
			s.countHTTPError(http.StatusTooManyRequests, CodeRateLimited)
			respondError(c, errRateLimited("rate limit exceeded"))
			c.Abort()
			return
		}
		c.Next()
	}
}

// denyRequest answers an auth/RBAC failure with the PRD §8.1 envelope.
func (s *Service) denyRequest(c *gin.Context, claims *Claims, err error, reason string) {
	apiErr := asAPIError(err)
	rbacDenied.WithLabelValues(reason).Inc()
	_ = claims
	s.countHTTPError(apiErr.Status, apiErr.Code)
	respondError(c, apiErr)
	c.Abort()
}

// countHTTPError feeds `http_errors_total{status,error_code}`.
func (s *Service) countHTTPError(status int, code string) {
	httpErrors.WithLabelValues(strconv.Itoa(status), code).Inc()
}

// currentIdentity returns the resolved identity of the request.
func currentIdentity(c *gin.Context) (*tenantIdentity, bool) {
	v, ok := c.Get(ctxIdentity)
	if !ok {
		return nil, false
	}
	identity, ok := v.(*tenantIdentity)
	return identity, ok
}

// currentClaims returns the verified JWT claims of the request.
func currentClaims(c *gin.Context) (*Claims, bool) {
	v, ok := c.Get(ctxClaims)
	if !ok {
		return nil, false
	}
	claims, ok := v.(*Claims)
	return claims, ok
}
