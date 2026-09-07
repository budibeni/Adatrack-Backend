// Command nats-purge — tool operasional B4: purge SEMUA pesan pada SEMUA
// stream JetStream NATS (telemetry-raw, alert-*, dll), mempertahankan
// konfigurasi stream itu sendiri.
//
// Dipakai oleh scripts/cleanup-b4-endurance-data.sh untuk menghapus data test
// B4 yang masih tertahan di JetStream (retensi 48h akan otomatis membersihkan,
// tapi dibersihkan segera di sini supaya storage kembali lega).
//
// Pemakaian:
//   NATS_URL=127.0.0.1:4222 nats-purge
//
// Exit code 0 bila semua stream berhasil di-purge; non-zero bila ada kegagalan
// (per-stream tetap dicoba, kegagalan dilaporkan tanpa silent drop).
package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/nats-io/nats.go"
)

func main() {
	url := "nats://127.0.0.1:4222"
	if v := os.Getenv("NATS_URL"); v != "" {
		url = "nats://" + v
	}

	nc, err := nats.Connect(url)
	if err != nil {
		log.Fatalf("nats-purge: connect %s gagal: %v", url, err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		log.Fatalf("nats-purge: jetstream context gagal: %v", err)
	}

	// Kumpulkan semua nama stream (termasuk stream kosong).
	names := []string{}
	for st := range js.StreamsInfo() {
		name := st.Config.Name
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		fmt.Println("nats-purge: tidak ada stream JetStream — tidak perlu di-purge.")
		return
	}

	failed := 0
	for _, name := range names {
		st, err := js.StreamInfo(name, nats.MaxWait(60*time.Second))
		if err != nil {
			fmt.Printf("nats-purge: SKIP %s (info gagal: %v)\n", name, err)
			failed++
			continue
		}
		beforeMsgs := st.State.Msgs
		beforeBytes := st.State.Bytes

		if err := js.PurgeStream(name, nats.MaxWait(120*time.Second)); err != nil {
			fmt.Printf("nats-purge: GAGAL purge %s (%v)\n", name, err)
			failed++
			continue
		}
		fmt.Printf("nats-purge: OK  %-20s sebelum=%d msgs / %d bytes -> kosong\n",
			name, beforeMsgs, beforeBytes)
	}

	if failed > 0 {
		fmt.Printf("nats-purge: SELESAI dengan %d kegagalan (lihat di atas).\n", failed)
		os.Exit(1)
	}
	fmt.Println("nats-purge: SELESAI — semua stream JetStream di-purge.")
}