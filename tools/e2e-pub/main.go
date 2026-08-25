// e2epub — utility test e2e F3 (murni stdlib, tanpa dependency eksternal).
// Mem-publish pesan telemetry JSON ke NATS memakai protokol text NATS
// (CONNECT/PUB) sehingga bisa men-simulasi perangkat tanpa TCP GT06.
//
// Cara pakai:
//   go build -o e2epub .
//   ./e2epub -cases cases.json            # daftar {subject,payload}[], jeda -delay
//   ./e2epub -subject <s> < payload.json  # publish satu pesan dari stdin
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type pubCase struct {
	Subject string `json:"subject"`
	Payload string `json:"payload"`
}

func main() {
	host := flag.String("host", "127.0.0.1:4222", "NATS server address")
	casesPath := flag.String("cases", "", "path ke file JSON daftar kasus yang akan di-publish berurutan")
	subject := flag.String("subject", "", "subject target (mode publish tunggal)")
	delay := flag.Duration("delay", 400*time.Millisecond, "jeda antar publish")
	genhash := flag.String("genhash", "", "buat bcrypt hash untuk password ini lalu keluar (untuk seed users)")
	flag.Parse()

	if *genhash != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(*genhash), bcrypt.DefaultCost)
		if err != nil {
			fatalf("genhash: %v", err)
		}
		fmt.Println(string(hash))
		return
	}

	var cases []pubCase
	switch {
	case *casesPath != "":
		raw, err := os.ReadFile(*casesPath)
		if err != nil {
			fatalf("read cases: %v", err)
		}
		if err := json.Unmarshal(raw, &cases); err != nil {
			fatalf("parse cases: %v", err)
		}
		if len(cases) == 0 {
			fatalf("cases kosong")
		}
	case *subject != "":
		payload, err := io.ReadAll(os.Stdin)
		if err != nil {
			fatalf("read payload: %v", err)
		}
		cases = []pubCase{{Subject: *subject, Payload: string(payload)}}
	default:
		fatalf("gunakan -cases <file.json> atau -subject <s> dengan payload via stdin")
	}

	conn, err := net.Dial("tcp", *host)
	if err != nil {
		fatalf("dial nats: %v", err)
	}
	defer conn.Close()

	// Handshake NATS (verbose off), lalu tunggu PONG.
	if _, err := fmt.Fprintf(conn, "CONNECT {\"verbose\":false,\"pedantic\":false}\r\nPING\r\n"); err != nil {
		fatalf("handshake: %v", err)
	}
	if line, err := bufio.NewReader(conn).ReadString('\n'); err != nil {
		fatalf("baca respons handshake: %v", err)
	} else {
		fmt.Printf("[info] server: %s", line)
	}

	for i, c := range cases {
		if err := publish(conn, c.Subject, c.Payload); err != nil {
			fatalf("publish #%d gagal: %v", i+1, err)
		}
		fmt.Printf("[ok] %d/%d  %s  (%d bytes)\n", i+1, len(cases), c.Subject, len(c.Payload))
		if i < len(cases)-1 && *delay > 0 {
			time.Sleep(*delay)
		}
	}
	fmt.Println("[done]")
}

func publish(conn net.Conn, subject, payload string) error {
	_, err := fmt.Fprintf(conn, "PUB %s %d\r\n%s\r\n", subject, len(payload), payload)
	return err
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "e2epub: "+format+"\n", args...)
	os.Exit(1)
}