module backend/service-websocket

go 1.21

require (
	backend/internal v0.0.0
	github.com/go-chi/chi/v5 v5.0.12
	github.com/go-chi/cors v1.2.1
	github.com/golang-jwt/jwt/v5 v5.2.1
	github.com/gorilla/websocket v1.5.1
	golang.org/x/crypto v0.23.0
)

replace backend/internal => ../../internal
