// pgxdriver.go — pembungkus database/sql di atas driver pgx v5 stdlib yang
// men-transpile placeholder '?' gaya MySQL menjadi '$N' gaya PostgreSQL.
//
// MENGAPA INI ADA: proyek sengaja memakai SATU code path SQL untuk kedua
// engine (PRD §7.1.1) dengan placeholder posisi '?'. Asumsi lama yang
// terdokumentasi di paket ini ("pgx menulis ulang ? menjadi $N otomatis")
// TIDAK BENAR untuk driver stdlib pgx v5 — query ber-'?' menghasilkan
// SQLSTATE 42601 "syntax error at end of input" (diverifikasi empiris
// terhadap PostgreSQL live, sesi B4 2026-08-25).
//
// Alih-alih menyentuh ratusan call site di enam service, SEMUA koneksi
// postgres yang dibuka via Dialect.DriverName() kini melewati driver
// pembungkus bernama PgxadatrackDriverName ("pgxadatrack"). Transpilasi bersifat
// leksikal murni (lihat RewritePlaceholders) dan ter-unit-test.

package dialect

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/stdlib" // driver dasar "pgx" + GetDefaultDriver
)

// PgxadatrackDriverName adalah nama driver database/sql hasil pembungkus
// (wrapper) di atas pgx v5 stdlib.
const PgxadatrackDriverName = "pgxadatrack"

var registerOnce sync.Once

// RegisterPgxPlaceholderDriver mendaftarkan driver PgxadatrackDriverName secara
// idempoten. Dipanggil otomatis oleh Dialect.DriverName() untuk Postgres.
func RegisterPgxPlaceholderDriver() {
	registerOnce.Do(func() {
		sql.Register(PgxadatrackDriverName, &pgxadatrackDriver{})
	})
}

type pgxadatrackDriver struct{}

// OpenConnector meneruskan ke OpenConnector milik stdlib pgx agar parsing DSN
// URL tetap dilakukan pgx, lalu membungkus tiap koneksi yang dihasilkan.
func (d *pgxadatrackDriver) OpenConnector(name string) (driver.Connector, error) {
	type openConnectorer interface {
		OpenConnector(string) (driver.Connector, error)
	}
	base, ok := stdlib.GetDefaultDriver().(openConnectorer)
	if !ok {
		return nil, fmt.Errorf("dialect: pgx stdlib driver tidak mendukung OpenConnector")
	}
	inner, err := base.OpenConnector(name)
	if err != nil {
		return nil, err
	}
	return &pgxadatrackConnector{inner: inner}, nil
}

func (d *pgxadatrackDriver) Open(name string) (driver.Conn, error) {
	raw, err := stdlib.GetDefaultDriver().Open(name)
	if err != nil {
		return nil, err
	}
	return &pgxadatrackConn{raw: raw}, nil
}

type pgxadatrackConnector struct {
	inner driver.Connector
}

func (c *pgxadatrackConnector) Connect(ctx context.Context) (driver.Conn, error) {
	raw, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &pgxadatrackConn{raw: raw}, nil
}

func (c *pgxadatrackConnector) Driver() driver.Driver { return &pgxadatrackDriver{} }

// pgxadatrackConn membungkus driver.Conn pgx stdlib dan men-transpile query pada
// semua jalur eksekusi (Exec/Query/Prepare, varian Context maupun tidak).
type pgxadatrackConn struct {
	raw driver.Conn
}

func (c *pgxadatrackConn) Prepare(q string) (driver.Stmt, error) {
	return c.raw.Prepare(RewritePlaceholders(q))
}

func (c *pgxadatrackConn) Close() error                { return c.raw.Close() }
func (c *pgxadatrackConn) Begin() (driver.Tx, error)   { return c.raw.Begin() }

func (c *pgxadatrackConn) BeginTx(ctx context.Context, opts sql.TxOptions) (driver.Tx, error) {
	if bt, ok := c.raw.(driver.ConnBeginTx); ok {
		return bt.BeginTx(ctx, driver.TxOptions{
			Isolation: driver.IsolationLevel(opts.Isolation),
			ReadOnly:  opts.ReadOnly,
		})
	}
	return c.Begin()
}

func (c *pgxadatrackConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	if pc, ok := c.raw.(driver.ConnPrepareContext); ok {
		return pc.PrepareContext(ctx, RewritePlaceholders(q))
	}
	return nil, driver.ErrSkip
}

func (c *pgxadatrackConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if ec, ok := c.raw.(driver.ExecerContext); ok {
		return ec.ExecContext(ctx, RewritePlaceholders(q), args)
	}
	return nil, driver.ErrSkip
}

func (c *pgxadatrackConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if qc, ok := c.raw.(driver.QueryerContext); ok {
		return qc.QueryContext(ctx, RewritePlaceholders(q), args)
	}
	return nil, driver.ErrSkip
}

// CheckNamedValue diteruskan ke checker pgx (konversi time.Time dsb.) bila ada.
func (c *pgxadatrackConn) CheckNamedValue(nv *driver.NamedValue) error {
	if cn, ok := c.raw.(driver.NamedValueChecker); ok {
		return cn.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

// ---------------------------------------------------------------------------
// Placeholder rewriting
// ---------------------------------------------------------------------------

// RewritePlaceholders mengubah placeholder posisi '?' menjadi '$N' (PostgreSQL)
// berurutan sesuai kemunculan. Karakter '?' DI DALAM string literal ('…'),
// identifier berkutip ganda ("…"), maupun komentar (-- … / * … */) TIDAK
// diubah. Query yang sudah memakai '$n' lolos apa adanya.
//
// Limitasi (dokumentasi jujur): dollar-quoted string PostgreSQL ($$…$$ /
// $tag$…$tag$) tidak ditangani scanner ini — kode proyek tidak memakainya;
// hindari '?' literal di luar konstruksi di atas (pakai parameter binding).
func RewritePlaceholders(query string) string {
	if !strings.Contains(query, "?") {
		return query // fast path: query tanpa parameter / sudah $n
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	i, L := 0, len(query)
	for i < L {
		ch := query[i]
		switch {
		case ch == '\'': // string literal; '' = escape kutip
			b.WriteByte(ch)
			i++
			for i < L {
				if query[i] == '\'' {
					if i+1 < L && query[i+1] == '\'' {
						b.WriteString("''")
						i += 2
						continue
					}
					break
				}
				b.WriteByte(query[i])
				i++
			}
			if i < L {
				b.WriteByte('\'')
				i++
			}
		case ch == '"': // identifier berkutip ganda; "" = escape
			b.WriteByte(ch)
			i++
			for i < L {
				if query[i] == '"' {
					if i+1 < L && query[i+1] == '"' {
						b.WriteString("\"\"")
						i += 2
						continue
					}
					break
				}
				b.WriteByte(query[i])
				i++
			}
			if i < L {
				b.WriteByte('"')
				i++
			}
		case ch == '-' && i+1 < L && query[i+1] == '-': // komentar baris
			for i < L && query[i] != '\n' {
				b.WriteByte(query[i])
				i++
			}
		case ch == '/' && i+1 < L && query[i+1] == '*': // komentar blok
			b.WriteString("/*")
			i += 2
			for i < L {
				if query[i] == '*' && i+1 < L && query[i+1] == '/' {
					b.WriteString("*/")
					i += 2
					break
				}
				b.WriteByte(query[i])
				i++
			}
		case ch == '?':
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			i++
		default:
			b.WriteByte(ch)
			i++
		}
	}
	return b.String()
}