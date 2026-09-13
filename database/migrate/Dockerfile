FROM postgres:16-alpine

# Copy all migration and init scripts
COPY init-pg/ /scripts/init-pg/
COPY migrations/ /scripts/migrations/
COPY seed/ /scripts/seed/

# Entrypoint script that runs migrations
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
