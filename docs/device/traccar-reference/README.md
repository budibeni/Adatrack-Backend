# Traccar Protocol Reference

> Referensi teknis untuk implementasi protokol GPS tracker di `ingestion-tcp`, berdasarkan analisis kode sumber Traccar (200+ protokol).

## File Index

| File | Deskripsi |
|------|-----------|
| [01-overview.md](01-overview.md) | Overview Traccar, gap analysis, arsitektur decoder |
| [02-priority-high.md](02-priority-high.md) | Protokol prioritas tinggi (Meiligao, Xexun, Suntech, H02) |
| [03-priority-medium.md](03-priority-medium.md) | Protokol prioritas sedang (Totem, GT02, Navigil, Castel) |
| [04-priority-low.md](04-priority-low.md) | Protokol prioritas rendah (CalAmp, Cellocator, Ruptela) |
| [05-implementation-guide.md](05-implementation-guide.md) | Pattern implementasi di Go, checklist, helper functions |
| [06-full-protocol-list.md](06-full-protocol-list.md) | Daftar lengkap 200+ protokol Traccar |
| [07-appendix.md](07-appendix.md) | CRC algorithms, NMEA parsing, BCD, port config |

## Quick Summary

### Sudah Diimplementasikan
- **GT06 (Concox)** - Lengkap dengan fuel sensor
- **Teltonika (Codec 8/8E/7/6)** - Lengkap dengan IO elements
- **TK103 (GT-clone)** - Provisional (alarm belum lengkap)

### Gap Perlu Ditutup (berdasarkan market share Indonesia)

| Prioritas | Protokol | Alasan |
|-----------|----------|--------|
| Tinggi | Meiligao | Populer di Asia, banyak tracker China |
| Tinggi | Xexun | NMEA-based, sangat banyak di Indonesia |
| Tinggi | Suntech | Premium segment, armada logistik |
| Tinggi | H02/H08 | Text protocol populer, banyak clone |
| Sedang | Totem | Text+binary hybrid |
| Sedang | GT02 | Concox family binary |
| Sedang | Navigil | Binary dengan ACK |
| Sedang | Castel | OBD + GPS komersial |
| Rendah | CalAmp | Enterprise fleet |
| Rendah | Cellocator | Industrial |
| Rendah | Ruptela | European market |

## Sumber

- **GitHub:** [github.com/traccar/traccar](https://github.com/traccar/traccar)
- **Lisensi:** Apache License 2.0
- **Website:** [traccar.org](https://www.traccar.org)
- **Protocol Docs:** [traccar.org/protocols](https://www.traccar.org/protocols/)

---

> **Terakhir diupdate:** 2026-09-03