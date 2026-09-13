# Chunked Endurance — B4 Endurance 24h yang Tahan WSL Crash

## Masalah

WSL/Docker Desktop restart membunuh semua proses dan container, sehingga endurance
24 jam kontinu mustahil selesai dalam satu run. Sebelumnya, endurance ~5.2 jam
(7.45 juta pesan) terputus karena WSL mati — data utuh tapi run tidak selesai.

## Solusi: Chunked Cumulative Endurance

Target 24 jam dibagi menjadi chunk yang lebih pendek (default 4 jam). Setiap chunk:
- Berjalan independen
- Mencatat baseline & delta MySQL
- Mengakumulasi metrik ke state file di `$HOME/b4_chunked_endurance/`
- Bisa di-resume setelah WSL crash dengan menjalankan ulang skrip yang sama

**Validitas SLA**: Data loss = `cumulative_sent - cumulative_delta_mysql`. Jika 0
setelah 24 jam kumulatif, hasilnya setara dengan 24 jam kontinu karena discrepancy
akan muncul di chunk manapun.

## Cara Pakai

### 1. Start/Resume Endurance

```bash
cd backend/scripts
./run-endurance-chunked.sh
```

Jalankan saja — jika sudah ada state, otomatis melanjutkan dari chunk terakhir.

### 2. Cek Progress

```bash
./run-endurance-chunked.sh --status
```

### 3. Reset & Mulai dari Awal

```bash
./run-endurance-chunked.sh --reset
./run-endurance-chunked.sh
```

### 4. Auto-Resume via Watchdog (opsional)

Untuk auto-restart setelah WSL crash:

```bash
./endurance-watchdog.sh
```

Atau register di Windows Task Scheduler:
- **Trigger**: At logon / At startup
- **Action**: `wsl.exe -d <distro> -e bash -c "/home/user/projects/ajb_gps/backend/scripts/endurance-watchdog.sh"`

## Konfigurasi (env opsional)

| Env | Default | Deskripsi |
|-----|---------|-----------|
| `CHUNK_HOURS` | 4 | Durasi per chunk (jam) |
| `TARGET_HOURS` | 24 | Total target kumulatif (jam) |
| `DEVICES` | 20 | Jumlah device simulasi |
| `RATE` | 20 | msg/s per device (400 total) |
| `TCP_PORT` | 9003 | Port ingestion-tcp |
| `TENANT_DB` | dev001 | Tenant database target |

Contoh custom:
```bash
CHUNK_HOURS=6 TARGET_HOURS=24 ./run-endurance-chunked.sh
```

## State & Logs

| Path | Deskripsi |
|------|-----------|
| `~/b4_chunked_endurance/state.txt` | State kumulatif (survive WSL crash) |
| `~/b4_chunked_endurance/logs/chunked_endurance.log` | Log utama |
| `~/b4_chunked_endurance/logs/chunks/chunk_NNN.log` | Log per chunk |
| `~/b4_chunked_endurance/logs/checkpoint.log` | Checkpoint tiap 5 menit |

## Target Acceptance

- **Durasi kumulatif**: 24 jam (6 chunk × 4 jam)
- **Pesan terkirim**: ~34.560.000 (24h × 400 msg/s)
- **Data loss**: 0 (diverifikasi via delta MySQL kumulatif)
- **Status**: PASS jika `cumulative_sent - cumulative_delta = 0`

## Alur Kerja

```
START → baca state
       ├─ idle → init baseline → loop chunk
       └─ running → loop chunk (resume)
       
LOOP:  while cumulative_duration < target
         ├─ run_chunk(N)
         │   ├─ catat baseline chunk
         │   ├─ jalankan loadtest (4h)
         │   ├─ catat delta chunk
         │   └─ update kumulatif → write_state
         └─ re-read state

END:   final_verify → show_status
       └─ PASS jika loss = 0
```

Jika WSL crash di tengah chunk:
1. Docker mati, loadtest mati, script mati
2. State file di `$HOME` tetap utuh (last successful chunk)
3. Setelah WSL hidup: jalankan ulang → otomatis resume dari chunk berikutnya
