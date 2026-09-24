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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// streams adalah stream yang dikelola stack ini (urutan tetap untuk output).
var streams = []string{"telemetry-raw", "telemetry-live", "telemetry-error", "alert", "notify", "media", "command"}

func main() {
	natsURL := flag.String("nats", envOr("NATS_URL", "nats://127.0.0.1:4222"), "NATS URL")
	showStatus := flag.Bool("status", false, "print per-stream status (messages/bytes/pending + budget usage)")
	purge := flag.String("purge", "", "comma-separated stream names to purge (empties all messages)")
	deleteStreams := flag.String("delete", "", "comma-separated stream names to DELETE (frees budget; recreated by services at boot)")
	confirm := flag.Bool("yes", false, "required together with --purge/--delete")
	assertBelow := flag.Float64("assert-usage-below", 0,
		"exit 1 if any stream's byte usage is at/above this percentage (pre/post-run guard)")

	// --- B8 downlink inspection -------------------------------------------
	publishCmd := flag.String("publish-command", "",
		"publish a downlink command request (engine_cut|engine_restore|set_interval|reboot|locate)")
	company := flag.String("company", envOr("E2E_COMPANY", "DEV001"), "tenant code (with --publish-command)")
	imei := flag.String("imei", "", "device IMEI (with --publish-command)")
	vehicleID := flag.Int64("vehicle-id", 0, "vehicle id (with --publish-command)")
	interval := flag.Int("interval-seconds", 20, "interval for set_interval (5..86400)")

	flag.Parse()

	if !*showStatus && *purge == "" && *deleteStreams == "" && *assertBelow == 0 && *publishCmd == "" {
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

	if *publishCmd != "" {
		reqID, err := publishCommand(js, *company, *imei, *vehicleID, *publishCmd, *interval)
		if err != nil {
			fail("publish command: %v", err)
		}
		fmt.Printf("published command.request.%s: request_id=%s kind=%s imei=%s\n",
			strings.ToUpper(*company), reqID, *publishCmd, *imei)
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

// commandRequest mirrors services/ingestion-tcp/models.DeviceCommand — the payload
// the downlink dispatcher consumes. Keeping the JSON field names identical is what
// lets this tool exercise the real command path (B8).
type commandRequest struct {
	ID          int64  `json:"id"`
	RequestID   string `json:"request_id"`
	CompanyCode string `json:"company_code"`
	VehicleID   int64  `json:"vehicle_id"`
	IMEI        string `json:"imei"`
	Command     string `json:"command"`
	Interval    int    `json:"interval_seconds,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// publishCommand publishes one downlink request on `command.request.<COMPANY>` and
// returns the generated request id (the same value the dispatcher writes into
// td_device_commands, so the operator can follow it to the device ACK).
func publishCommand(js nats.JetStreamContext, company, imei string, vehicleID int64, kind string, interval int) (string, error) {
	if imei == "" {
		return "", fmt.Errorf("--imei is required")
	}
	valid := map[string]bool{
		"engine_cut": true, "engine_restore": true, "set_interval": true,
		"reboot": true, "locate": true,
	}
	if !valid[kind] {
		return "", fmt.Errorf("unknown command %q (engine_cut|engine_restore|set_interval|reboot|locate)", kind)
	}
	reqID, err := randomRequestID()
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(commandRequest{
		RequestID:   reqID,
		CompanyCode: strings.ToUpper(company),
		VehicleID:   vehicleID,
		IMEI:        imei,
		Command:     kind,
		Interval:    interval,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return "", err
	}
	subject := "command.request." + strings.ToUpper(company)
	if _, err := js.Publish(subject, payload); err != nil {
		return "", fmt.Errorf("publish %s: %w", subject, err)
	}
	return reqID, nil
}

// randomRequestID builds a 128-bit hex request id (mirrors the API generator).
func randomRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// fail prints to stderr and exits 1.
func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "jsadmin: "+format+"\n", args...)
	os.Exit(1)
}
