# 00 — STRICT AI WORKING PROTOCOL (PLANNING MODE)

**MANDATORY RULE:** AI DILARANG KERAS langsung menulis atau memodifikasi file kode sumber (source code) ketika menerima instruksi pengerjaan fase (misal: "Kerjakan Phase B1"). AI WAJIB mematuhi alur kerja berikut tanpa terkecuali:

## TAHAP 1: RESEARCH & PLANNING (Dilarang Coding)
1. Baca dan telusuri dokumen `PRD.md` dan `.agent/*.md` secara menyeluruh yang berkaitan dengan tugas yang diminta.
2. Buat/Update sebuah artifact bernama `implementation_plan.md`.
3. Di dalam `implementation_plan.md`, rincikan:
   - **Komponen PRD:** Referensi spesifik (Nomor/Bagian) dari PRD yang menjadi acuan.
   - **Arsitektur:** Struktur file yang akan dibuat.
   - **Logic Detail:** Algoritma utama, integrasi, dan batasan (limit/TTL/retry).
   - **Zero Placeholders:** Penjelasan bahwa kode akan ditulis siap-produksi (production-ready).
4. **BERHENTI BERPIKIR.** Tanyakan kepada pengguna (User) apakah plan tersebut disetujui. JANGAN LANJUTKAN ke tahap eksekusi.

## TAHAP 2: EKSEKUSI (Setelah Disetujui User)
1. Setelah User membalas "Setuju" atau "Lanjut", barulah AI boleh memodifikasi/membuat file kode.
2. Tulis kode berskala Enterprise:
   - Implementasikan *Graceful Shutdown*.
   - Implementasikan *Exponential Backoff* untuk semua layanan eksternal.
   - Jangan gunakan `fmt.Println`, gunakan struktur *Logger* enterprise.
   - Dilarang keras menggunakan komentar `// TODO: in real scenario...`. Tulis kodenya secara utuh!

## TAHAP 3: VERIFIKASI MANDIRI
1. Setelah kode ditulis, bandingkan hasil kerja dengan PRD.
2. Jika ada yang melenceng (misal: nama tabel salah, constraint kurang), perbaiki langsung sebelum melapor ke User.
3. Laporkan bahwa tugas selesai dan buat ringkasan pekerjaan.
