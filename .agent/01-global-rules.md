# 01 — Global Rules & Conventions

> Panduan utama untuk semua kerja di project `adatrack`. Selalu dibaca sebelum mulai implementasi.

## 1. Project Context

Platform **Real-Time GPS Tracking & adatrack Management**.
- 5000+ device GPS, telemetry setiap 20 detik.
- Latensi end-to-end < 800 ms, throughput 2000 msg/sec peak, uptime target 99.9%.
- Referensi akurat: `PRD.md` (konsolidasi v1.7.0 — v1.3.0 + GAPS + FEATURE; struktur aplikasi frontend/acuan penerapan backend: `docs/FRONTEND.md`).
- 📘 **Penanggulangan insiden (CPU/Memory/RDS/service stuck/dll):** `docs/INCIDENT_RUNBOOK.md`.

## 2. Tech Stack

| Layer | Teknologi |
|---|---|
| Ingestion (TCP + parse protokol) | Golang |
| Message Broker / Queue | NATS (JetStream `-js`) |
| Live State / Cache | Redis |
| Persistent DB | PostgreSQL (schema per-tenant, partitioned) |
| Real-Time Streaming | Golang WebSocket (gorilla/websocket) |
| REST API | Golang, Gin (`api-vehicle`) |
| Frontend | Next.js (React), TailwindCSS, Mapbox GL / Leaflet |

## 3. Repository Layout (Backend-First)

```
├── backend/
├   ── .agent/                          # Aturan & roadmap kerja
├   ── PRD.md                           # PRD konsolidated v1.7.0  
│   ├── docker-compose.yml           # Infra dev: PostgreSQL, Redis, NATS
│   ├── deployments/docker-compose.yml
│   └── services/
│       ├── api-vehicle/             # REST API (Gin, PostgreSQL, JWT)
│       ├── ingestion-tcp/           # TCP listener + parse protokol → NATS
│       ├── service-websocket/       # REST + WebSocket + RBAC
│       ├── service-media/           # Dashcam event media MinIO/S3 (v1.3.0, fase B5b)
│       ├── worker-alert/            # Deteksi alert & geofence
│       └── worker-persistence/      # Batch insert ke PostgreSQL
│   └── worker-live/                 # Live state ke Redis
└── frontend/
    └── (Next.js app, FYI kosong)
```

## 4. Working Conventions

1. **Urutan kerja: BACKEND dulu, FRONTEND belakangan.** Jangan memulai frontend sebelum backend selesai & teruji.
2. **Backend-first** = setiap modul selesai (kode + test + ter-integrasi + jalan) baru lanjut.
3. **Jangan hapus/ubah** file dokumentasi PRD kecuali disetujui user.
4. **Satu layanan satu module Go.**
   - Module pattern: `adatrack/<service-name>` per service (`adatrack/ingestion-tcp`, `adatrack/worker-persistence`, `adatrack/worker-live`, `adatrack/service-websocket`, `adatrack/api-vehicle`, dst.). Ikuti pola ini untuk semua service (clean slate — pola ditetapkan sejak awal pengerjaan).
   - File utama: `main.go` (di root service), pakai build `go build -o <name> .` untuk konsisten dengan Dockerfile.
5. **Selalu baca go.mod** service terkait sebelum menambah dependency; gunakan dependency yang sudah ada bila cukup.
6. **Config via environment variable** — ikuti daftar env di `PRD.md §7`. Taruh default aman untuk dev lokal di kode (fallback), tetap bisa di-override env.
7. **Logging:** structured log (JSON) dengan level `debug|info|warn|error`. Jangan `fmt.Println` untuk log.
8. **Error handling:** jangan pernah *silent drop*; log setiap error. Gunakan retry + exponential backoff untuk operasi eksternal (NATS/PostgreSQL/Redis).
9. **Health & metrics:** tiap service expose endpoint `/healthz` (readiness) dan `/metrics` (Prometheus).
10. **Konvensi NATS subjects** (dari PRD §…/GAPS):
    - `telemetry.raw.<IMEI>` : raw telemetry dari device
    - `telemetry.live.<IMEI>` : update live ke WebSocket
    - `telemetry.error.<IMEI>` : parse error
    - `alert.geofence.*`, `alert.speed.*`, `alert.offline.*`, `alert.battery.*`, `alert.sos.*`
    - `alert.fuel.<company>` : alert FUEL_DROP / REFUEL (PRD v1.3.0, fase B5a)
    - `media.event.<company>`, `media.capture.request.<company>` : dashcam event media (PRD v1.3.0, fase B5b)
    - Queue groups: `persistence`, `live`, `websocket`, `alert` (+ `media` untuk service-media)
11. **Commit atomic & jelas**, describe perubahan sesuai fase.