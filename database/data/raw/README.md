# Raw Reference Data (sumber data real)

File-file di folder ini adalah **sumber mentah** yang dipakai
`backend/database/tools/genregions` untuk membangkitkan
`backend/database/seed/reference/*.sql`. Jangan diedit manual — regenerasi
dengan menjalankan ulang tool tersebut.

| File | Sumber | Lisensi | Dipakai untuk |
|---|---|---|---|
| `countries.json` | [mledoze/countries](https://github.com/mledoze/countries) `master` (diunduh 2026-08-23) | ODbL / MIT | Tabel `countries` (ISO 3166-1: iso2, iso3, nama, kode telepon, mata uang) |
| `provinces.csv` | [fityannugroho/idn-area-data](https://github.com/fityannugroho/idn-area-data) `main/data` (2026-08-23) | MIT (data Kemendagri) | Tabel `provinces` (38 provinsi, kode BPS 2-digit) |
| `regencies.csv` | idem | idem | Tabel `cities` (514 kabupaten/kota, kode BPS 4-digit) |
| `districts.csv` | idem | idem | Tabel `districts` (kecamatan, kode BPS 6-digit) |
| `villages.csv` | idem | idem | Tabel `subdistricts` (desa/kelurahan, kode BPS 8-digit) |
| `pos_data.csv` | [cahyadsn/wilayah_kodepos](https://github.com/cahyadsn/wilayah_kodepos) `master/src` (2026-08-23) | MIT | `subdistricts.postal_code` (join berbasis nama, best-effort) |
| `geonames_id_coords.tsv` | Diekstrak dari [GeoNames ID dump](https://download.geonames.org/export/dump/ID.zip) (CC-BY 4.0) pada 2026-08-23; hanya baris ADM1+ADM2 | CC-BY 4.0 | Koordinat provinsi (centroid kota) & kota |

## Catatan jujur (limitasi data)

- Koordinat **kota** berasal dari GeoNames ADM2 (join berdasarkan nama
  ternormalisasi; hanya dipakai bila kandidat unik). Provinsi = centroid dari
  koordinat kota-kotanya. Kecamatan/desa **tidak** diberi koordinat (NULL).
- `postal_code` desa/kelurahan hanya terisi ±4.500 baris yang cocok persis
  dengan dataset kodepos (perbedaan ejaan menyebabkan sisanya NULL — kolom
  memang nullable dan bisa dilengkapi berkala).
- Nama & kode mengikuti data resmi Kemendagri termasuk pemekaran Papua 2022
  (provinsi 91–96).
