module backend/api-vehicle

go 1.21

require (
	backend/internal v0.0.0
	github.com/go-chi/chi/v5 v5.0.12
	github.com/go-chi/cors v1.2.1
)

replace backend/internal => ../../internal
