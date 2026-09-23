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
    echo 'echo "Waiting for postgres to stabilize..."' >> /app/run.sh && \
    echo 'sleep 5' >> /app/run.sh && \
    echo 'echo "Running schema migrations..."' >> /app/run.sh && \
    echo '/app/migrate' >> /app/run.sh && \
    echo 'MIGRATE_EXIT=$?' >> /app/run.sh && \
    echo 'if [ $MIGRATE_EXIT -ne 0 ]; then echo "MIGRATE BINARY FAILED WITH CODE $MIGRATE_EXIT (continuing to test psql...)"; fi' >> /app/run.sh && \
    echo 'echo "Applying seed data..."' >> /app/run.sh && \
    echo 'psql -f /app/database/init-pg/03_seed_master.sql' >> /app/run.sh && \
    echo 'PSQL_EXIT=$?' >> /app/run.sh && \
    echo 'if [ $PSQL_EXIT -ne 0 ]; then echo "PSQL SEED FAILED WITH CODE $PSQL_EXIT"; exit 1; fi' >> /app/run.sh && \
    echo 'echo "Migration and Seeding complete!"' >> /app/run.sh && \
    chmod +x /app/run.sh

CMD ["/app/run.sh"]
