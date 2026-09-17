package controllers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"adatrack_gps/service-websocket/models"
)

// Refresh exchanges an opaque refresh token for a NEW access+refresh pair
// (FR-5.7): rotation is mandatory — the presented token is destroyed, so a
// replayed token is rejected.
func (a *AuthService) Refresh(ctx context.Context, raw, ip, userAgent, requestID string) (*models.LoginResponse, error) {
	raw = strings.TrimSpace(raw)
	key := a.cfg.RefreshPrefix + hashToken(raw)

	stored, err := a.kv.Get(ctx, key)
	if err != nil {
		return nil, errUnavailable("token store unavailable")
	}
	if stored == "" {
		_ = a.recordAudit(ctx, AuditRow{
			Action: ActionTokenRefresh, Outcome: OutcomeFailure, ActorIP: ip,
			ActorUserAgent: userAgent, RequestID: requestID, Reason: "unknown or already rotated refresh token",
		})
		return nil, NewAPIError(401, CodeTokenInvalid, "invalid refresh token")
	}

	var record refreshRecord
	if uerr := json.Unmarshal([]byte(stored), &record); uerr != nil {
		return nil, errInternal("corrupt refresh record")
	}

	// Rotation: the presented token can never be used twice (PRD §9.1).
	if derr := a.kv.Del(ctx, key); derr != nil {
		return nil, errUnavailable("token store unavailable")
	}

	user, uerr := a.store.UserByID(ctx, record.UserID)
	if uerr != nil {
		return nil, errUnavailable("authentication backend unavailable")
	}
	if user == nil || !user.IsActive {
		return nil, NewAPIError(401, CodeAccountInactive, "account is inactive")
	}

	identity, ierr := a.resolveIdentity(ctx, user)
	if ierr != nil {
		return nil, ierr
	}
	pair, terr := a.issueTokens(ctx, identity, identity.assigned)
	if terr != nil {
		return nil, terr
	}

	a.recordAudit(ctx, AuditRow{
		Action: ActionTokenRefresh, Outcome: OutcomeSuccess, ActorUserID: user.ID,
		ActorEmail: user.Email, ActorRole: identity.user.Role, CompanyCode: identity.user.CompanyCode,
		ActorIP: ip, ActorUserAgent: userAgent, EntityType: "user", EntityID: itoa(user.ID),
		RequestID: requestID,
	})

	return &models.LoginResponse{TokenPair: pair, User: identity.user}, nil
}

// Logout revokes the access token (jti denylist) and the refresh token
// (FR-5.7). The audit row is written FIRST: if it cannot be persisted the request
// is rejected (fail-closed) and nothing is revoked, so the caller can retry.
func (a *AuthService) Logout(ctx context.Context, claims *Claims, rawRefresh, ip, userAgent, requestID string) error {
	if a.auditor != nil {
		if aerr := a.auditor.RecordSync(ctx, AuditRow{
			Action: ActionLogout, Outcome: OutcomeSuccess, ActorUserID: claims.UserID,
			ActorEmail: claims.Email, ActorRole: claims.Role, CompanyCode: claims.CompanyCode,
			ActorIP: ip, ActorUserAgent: userAgent, EntityType: "user", EntityID: itoa(claims.UserID),
			RequestID: requestID,
		}); aerr != nil {
			return errUnavailable("audit trail unavailable")
		}
	}

	if err := a.DenyToken(ctx, claims); err != nil {
		return errUnavailable("token store unavailable")
	}
	if err := a.RevokeRefresh(ctx, rawRefresh); err != nil {
		return errUnavailable("token store unavailable")
	}

	a.recordAudit(ctx, AuditRow{
		Action: ActionTokenRevoked, Outcome: OutcomeSuccess, ActorUserID: claims.UserID,
		ActorEmail: claims.Email, ActorRole: claims.Role, CompanyCode: claims.CompanyCode,
		ActorIP: ip, EntityID: claims.ID, EntityType: "token", Reason: "logout",
		RequestID: requestID,
	})
	return nil
}

// recordAudit buffers a non-critical audit row (never blocks the response).
func (a *AuthService) recordAudit(ctx context.Context, row AuditRow) error {
	if a.auditor == nil {
		return nil
	}
	row.BeforeState = redactAuditState(row.BeforeState)
	row.AfterState = redactAuditState(row.AfterState)
	a.auditor.Record(row)
	return nil
}

// RevokedTokenError builds the 401 TOKEN_REVOKED failure and audits it
// (used by the auth middleware, PRD §8.1/§9.4).
func (a *AuthService) RevokedTokenError(ctx context.Context, claims *Claims, ip, userAgent, requestID string) error {
	if claims != nil {
		_ = a.recordAudit(ctx, AuditRow{
			Action: ActionTokenRevoked, Outcome: OutcomeDenied, ActorUserID: claims.UserID,
			ActorEmail: claims.Email, ActorRole: claims.Role, CompanyCode: claims.CompanyCode,
			ActorIP: ip, ActorUserAgent: userAgent, EntityType: "token", EntityID: claims.ID,
			Reason: "token is denylisted", RequestID: requestID,
		})
	}
	return NewAPIError(401, CodeTokenRevoked, "token has been revoked")
}

// nowUTC returns the current UTC time (helper for audit/deadline maths).
func nowUTC() time.Time { return time.Now().UTC() }

// logAuthIssue logs a failure that must never be silent but is not fatal.
func logAuthIssue(msg string, err error) {
	if err != nil {
		slog.Warn(msg, "error", err)
	}
}
