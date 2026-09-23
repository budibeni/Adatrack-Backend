// Command jsadmin — housekeeping JetStream untuk stack acceptance lokal.
//
// Kenapa tool ini ada: ketika sebuah stream menyentuh budget byte-nya
// (`JETSTREAM_MAX_BYTES`, default 4 GiB) dengan konsumer yang tertinggal,
// JetStream mulai membuang pesan tertua (`DiscardOld`) — termasuk pesan yang
// belum di-ack. Setelah itu pipeline bisa macet permanen: konsumer tidak bisa
// mengejar back-log, ingestion melihat pemakaian >90 % dan (sesuai FR-1.5)
// membuang SEMUA telemetri baru — sehingga tidak ada yang bisa memulihkan diri.
// Terjadi pada run endurance 2026-09-22 (docs/B4-VERIFICATION.md §2.14).
//
// Sebelum tool ini tidak ada jalur pemulihan sama sekali (tidak ada CLI `nats`,
// tidak ada kode purge/delete stream) sehingga satu-satunya jalan adalah
// menghapus volume NATS — yang membuang state semua stream.
//
// Usage:
//
//	tools/jsadmin --status                       # pesan/byte/pending per stream + % budget
//	tools/jsadmin --purge telemetry-raw,telemetry-live --yes
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/nats-io/nats.go"
)

// streams adalah stream yang dikelola stack ini (urutan tetap untuk output).
var streams = []string{"telemetry-raw", "telemetry-live", "telemetry-error", "alert", "notify", "media"}

func main() {
	natsURL := flag.String("nats", envOr("NATS_URL", "nats://127.0.0.1:4222"), "NATS URL")
	showStatus := flag.Bool("status", false, "print per-stream status (messages/bytes/pending + budget usage)")
	purge := flag.String("purge", "", "comma-separated stream names to purge (empties all messages)")
	deleteStreams := flag.String("delete", "", "comma-separated stream names to DELETE (frees budget; recreated by services at boot)")
	confirm := flag.Bool("yes", false, "required together with --purge/--delete")
	assertBelow := flag.Float64("assert-usage-below", 0,
		"exit 1 if any stream's byte usage is at/above this percentage (pre/post-run guard)")
	flag.Parse()

	if !*showStatus && *purge == "" && *deleteStreams == "" && *assertBelow == 0 {
		flag.Usage()
		os.Exit(2)
	}

	nc, err := nats.Connect(*natsURL, nats.Name("adatrack-jsadmin"))
	if err != nil {
		fail("connect %s: %v", *natsURL, err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		fail("jetstream context: %v", err)
	}

	if *showStatus {
		for _, name := range streams {
			info, err := js.StreamInfo(name)
			if err != nil {
				fmt.Printf("%-16s (tidak ada: %v)\n", name, err)
				continue
			}
			limit := info.Config.MaxBytes
			usage := "-"
			if limit > 0 {
				usage = fmt.Sprintf("%.1f%% dari %s", float64(info.State.Bytes)/float64(limit)*100, humanBytes(uint64(limit)))
			}
			fmt.Printf("%-16s msgs=%d bytes=%s consumers=%d usage=%s\n",
				name, info.State.Msgs, humanBytes(info.State.Bytes), info.State.Consumers, usage)
			if info.State.Consumers > 0 {
				for c := range js.ConsumersInfo(name) {
					if c == nil {
						fail("consumer info stream %s: channel ditutup tanpa data", name)
					}
					fmt.Printf("    - %-12s pending=%d ack_pending=%d redelivered=%d\n",
						c.Name, c.NumPending, c.NumAckPending, c.NumRedelivered)
				}
			}
		}
	}

	if *assertBelow > 0 {
		var offenders []string
		for _, name := range streams {
			info, err := js.StreamInfo(name)
			if err != nil {
				continue
			}
			limit := info.Config.MaxBytes
			if limit <= 0 {
				continue
			}
			pct := float64(info.State.Bytes) / float64(limit) * 100
			if pct >= *assertBelow {
				offenders = append(offenders, fmt.Sprintf("%s %.1f%% (%s/%s)",
					name, pct, humanBytes(info.State.Bytes), humanBytes(uint64(limit))))
			}
		}
		if len(offenders) > 0 {
			fmt.Fprintf(os.Stderr, "jsadmin: stream mendekati budget, ingestion akan mulai membuang telemetri (FR-1.5 drop >90%%):\n  %s\n",
				strings.Join(offenders, "\n  "))
			fmt.Fprintln(os.Stderr, "jsadmin: pulihkan dengan `make js-purge` lalu restart service pipeline")
			os.Exit(1)
		}
		fmt.Printf("jsadmin: semua stream < %.0f%% dari budget — sehat\n", *assertBelow)
	}

	if *purge != "" {
		if !*confirm {
			fail("purge bersifat destruktif: ulangi dengan --yes")
		}
		for _, raw := range strings.Split(*purge, ",") {
			name := strings.TrimSpace(raw)
			if name == "" {
				continue
			}
			before, err := js.StreamInfo(name)
			if err != nil {
				fail("stream %s: %v", name, err)
			}
			if err := js.PurgeStream(name); err != nil {
				fail("purge %s: %v", name, err)
			}
			after, err := js.StreamInfo(name)
			if err != nil {
				fail("stream %s setelah purge: %v", name, err)
			}
			fmt.Printf("purged %-16s msgs %d -> %d, bytes %s -> %s\n",
				name, before.State.Msgs, after.State.Msgs, humanBytes(before.State.Bytes), humanBytes(after.State.Bytes))
		}
		fmt.Println("jsadmin: selesai — restart service pipeline agar konsumer kembali dari awal stream")
	}

	if *deleteStreams != "" {
		if !*confirm {
			fail("delete stream bersifat destruktif: ulangi dengan --yes")
		}
		for _, raw := range strings.Split(*deleteStreams, ",") {
			name := strings.TrimSpace(raw)
			if name == "" {
				continue
			}
			info, err := js.StreamInfo(name)
			if err != nil {
				fail("stream %s: %v", name, err)
			}
			if err := js.DeleteStream(name); err != nil {
				fail("delete %s: %v", name, err)
			}
			fmt.Printf("deleted %-16s (msgs %d, bytes %s) — dibebaskan untuk stream lain\n",
				name, info.State.Msgs, humanBytes(info.State.Bytes))
		}
		fmt.Println("jsadmin: selesai — restart service pipeline agar stream dibuat ulang dari env")
	}
}

// humanBytes renders a byte count in MiB/GiB.
func humanBytes(n uint64) string {
	const (
		mib = 1 << 20
		gib = 1 << 30
	)
	switch {
	case n >= gib:
		return fmt.Sprintf("%.2f GiB", float64(n)/float64(gib))
	case n >= mib:
		return fmt.Sprintf("%.2f MiB", float64(n)/float64(mib))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// envOr reads an env var or returns def.
func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// fail prints to stderr and exits 1.
func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "jsadmin: "+format+"\n", args...)
	os.Exit(1)
}
