package auth

import (
	"context"
	"net/http"
	"strings"

	"backend/internal/config"
	"backend/internal/logger"
	"backend/internal/redclient"
)

type contextKey string

const (
	ClaimsKey contextKey = "claims"
)

func AuthMiddleware(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
			claims, err := ValidateToken(cfg, tokenStr)
			if err != nil {
				if logger.Log != nil {
					logger.Log.Warn("Invalid token", "err", err)
				}
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Check revocation (denylist) if redis is configured
			if redclient.Client != nil && claims.ID != "" {
				isRevoked, err := redclient.Client.Exists(r.Context(), "denylist:"+claims.ID).Result()
				if err == nil && isRevoked > 0 {
					http.Error(w, "Token Revoked", http.StatusUnauthorized)
					return
				}
			}

			ctx := context.WithValue(r.Context(), ClaimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func EnsurePlatformAdminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := r.Context().Value(ClaimsKey).(*Claims)
		if !ok || claims.CompanyCode != "DEFAULT" || (claims.Role != "SUPER_ADMIN" && claims.Role != "ADMIN") {
			http.Error(w, "PLATFORM_SCOPE required", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func RequireRoleMiddleware(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := r.Context().Value(ClaimsKey).(*Claims)
			if !ok {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			for _, role := range roles {
				if strings.EqualFold(claims.Role, role) {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, "Forbidden", http.StatusForbidden)
		})
	}
}
