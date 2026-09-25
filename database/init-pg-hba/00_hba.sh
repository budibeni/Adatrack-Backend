#!/bin/sh
echo "host replication all all md5" >> "$PGDATA/pg_hba.conf"
