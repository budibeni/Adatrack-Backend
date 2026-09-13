# PostgreSQL Custom Image for Adatrack Backend

Docker image PostgreSQL 16 yang sudah include:
- Init scripts (`init-pg/`) - otomatis jalan saat first start
- Migration files (`migrations/`) - untuk manual migration
- Seed data (`seed/`) - data wilayah dan default data

## Build Image

```bash
cd database/postgres
docker build -t arfian107/postgres-adatrack:latest .
```

## Push ke Docker Hub

```bash
docker login
docker push arfian107/postgres-adatrack:latest
```

## Gunakan di docker-compose

```yaml
services:
  postgres:
    image: arfian107/postgres-adatrack:latest
    # ... config lainnya
    volumes:
      - postgres_data:/var/lib/postgresql/data
```

## Struktur Folder

```
database/postgres/
├── Dockerfile           # Dockerfile custom
├── init-pg/             # Init scripts (auto-run on first start)
│   ├── 01_schemas.sql
│   ├── 02_master_setup.sql
│   ├── 02b-seed-reference.sh
│   └── 03_company_setup.sql
├── migrations/          # Migration files
│   ├── master/          # MySQL master migrations
│   ├── master_pg/       # PostgreSQL master migrations
│   ├── company/         # MySQL company migrations
│   └── company_pg/      # PostgreSQL company migrations
└── seed/                # Seed data
    ├── reference/       # Reference data (wilayah)
    ├── master_seed.sql
    └── company_seed.sql
```
