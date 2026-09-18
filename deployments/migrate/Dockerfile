FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.work go.work.sum ./
COPY tools/migrate/ ./tools/migrate/
COPY database/ ./database/

RUN cd tools/migrate && go mod download && go build -o /migrate main.go

FROM alpine:latest
WORKDIR /app
COPY --from=builder /migrate /app/migrate
COPY --from=builder /app/database /app/database

CMD ["/app/migrate"]
