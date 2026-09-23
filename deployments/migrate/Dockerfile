FROM golang:alpine AS builder

WORKDIR /app
COPY . .

RUN cd tools/migrate && go mod download && go build -o /migrate main.go

FROM alpine:latest
RUN apk --no-cache add postgresql-client
WORKDIR /app
COPY --from=builder /migrate /app/migrate
COPY --from=builder /app/database /app/database

# Create a small runner script for Coolify
RUN echo '#!/bin/sh' > /app/run.sh && \
    echo 'set -e' >> /app/run.sh && \
    echo 'echo "Running schema migrations..."' >> /app/run.sh && \
    echo '/app/migrate' >> /app/run.sh && \
    echo 'echo "Applying seed data..."' >> /app/run.sh && \
    echo 'psql -f /app/database/init-pg/03_seed_master.sql' >> /app/run.sh && \
    echo 'echo "Migration and Seeding complete!"' >> /app/run.sh && \
    chmod +x /app/run.sh

CMD ["/app/run.sh"]
