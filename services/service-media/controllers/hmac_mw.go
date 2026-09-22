package controllers

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ctxIngest carries the verified HMAC ingest context into the handlers.
const ctxIngest = "media_ingest"

// ingestContext is the result of a successful FR-8.1 handshake.
type ingestContext struct {
	CompanyCode string
	Config      MediaConfig
	Secret      string
	// Body is the buffered raw request body (handlers re-read it instead of the
	// consumed stream, so the signature can be verified against the exact bytes).
	Body []byte
	// Multipart tells the handler that the signature covers the FILE bytes
	// (bound to the metadata) rather than the whole body.
	Multipart bool
}

// requireHMAC implements the FR-8.1 ingest handshake:
//
//	X-Company-Code : tenant code (selects the HMAC secret)
//	X-Timestamp    : RFC3339, must be within MEDIA_HMAC_MAX_SKEW_SEC (anti-replay)
//	X-Signature    : hex HMAC-SHA256
//	  - JSON body      → HMAC(secret, raw body bytes)
//	  - multipart/form → HMAC(secret, imei + "\n" + event_type + "\n" + file bytes)
//
// A missing/invalid signature is 401 MEDIA_SIGNATURE_INVALID (never a 403: the
// request is not authenticated at all).
func (s *Service) requireHMAC() gin.HandlerFunc {
	return func(c *gin.Context) {
		company := strings.ToUpper(strings.TrimSpace(c.GetHeader("X-Company-Code")))
		signature := c.GetHeader("X-Signature")
		if company == "" || strings.TrimSpace(signature) == "" {
			s.denyIngest(c, "missing_signature")
			return
		}
		if !TimestampFresh(c.GetHeader("X-Timestamp"), s.settings.HMACMaxSkew, time.Now().UTC()) {
			s.denyIngest(c, "stale_timestamp")
			return
		}

		cfg, err := s.mediaConfig(c.Request.Context(), company)
		if err != nil {
			respondError(c, errUnavailable("media configuration unavailable"))
			c.Abort()
			return
		}
		secret := cfg.HMACSecret
		if secret == "" {
			secret = s.settings.HMACSecret
		}
		if secret == "" {
			// Fail-closed: no secret configured for this tenant means the ingest
			// path cannot authenticate anything (PRD §8.5 rule 7).
			respondError(c, errUnavailable("ingest is not configured for this company"))
			c.Abort()
			return
		}

		body, rerr := io.ReadAll(c.Request.Body)
		if rerr != nil {
			respondError(c, errValidation("could not read the request body",
				map[string]string{"body": "unreadable"}))
			c.Abort()
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))

		multipart := strings.HasPrefix(strings.ToLower(c.GetHeader("Content-Type")), "multipart/form-data")
		if !multipart && !VerifyHMAC(secret, body, signature) {
			s.denyIngest(c, "bad_signature")
			return
		}

		c.Set(ctxIngest, &ingestContext{
			CompanyCode: company,
			Config:      cfg,
			Secret:      secret,
			Body:        body,
			Multipart:   multipart,
		})
		c.Next()
	}
}

// ingestOf returns the verified ingest context.
func ingestOf(c *gin.Context) (*ingestContext, bool) {
	v, ok := c.Get(ctxIngest)
	if !ok {
		return nil, false
	}
	ingest, ok := v.(*ingestContext)
	return ingest, ok
}

// denyIngest answers a failed handshake (401, counted + audited).
func (s *Service) denyIngest(c *gin.Context, reason string) {
	s.countHTTPError(http.StatusUnauthorized, CodeMediaSignatureInvalid)
	rbacDenied.WithLabelValues("media_" + reason).Inc()
	if s.auditor != nil {
		_ = s.auditor.Write(c.Request.Context(), AuditRow{
			Action:         ActionAccessDenied,
			Outcome:        OutcomeDenied,
			CompanyCode:    strings.ToUpper(strings.TrimSpace(c.GetHeader("X-Company-Code"))),
			EntityType:     "media_event",
			Reason:         reason,
			ActorIP:        c.ClientIP(),
			ActorUserAgent: c.GetHeader("User-Agent"),
			RequestID:      requestID(c),
		})
	}
	respondError(c, NewAPIError(http.StatusUnauthorized, CodeMediaSignatureInvalid,
		"invalid or missing HMAC signature"))
	c.Abort()
}
