package main

import (
	"flag"
	"log"
	"math/rand"
	"net"
	"time"
)

func main() {
	target := flag.String("target", "localhost:5000", "TCP server address")
	rate := flag.Int("rate", 100, "Messages per second")
	duration := flag.Int("duration", 60, "Duration in seconds")
	connections := flag.Int("conns", 100, "Number of concurrent connections")
	flag.Parse()

	log.Printf("Starting Load Test to %s with %d conns, %d msg/s for %ds", *target, *connections, *rate, *duration)

	connsList := make([]net.Conn, *connections)
	for i := 0; i < *connections; i++ {
		conn, err := net.Dial("tcp", *target)
		if err != nil {
			log.Fatalf("Failed to connect: %v", err)
		}
		connsList[i] = conn
		// Send login packet
		loginPkt := []byte{0x78, 0x78, 0x0D, 0x01, 0x08, 0x60, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x01, 0x0D, 0x0A}
		conn.Write(loginPkt)
	}

	time.Sleep(1 * time.Second) // wait for logins

	msgInterval := time.Duration(1000000 / *rate) * time.Microsecond
	ticker := time.NewTicker(msgInterval)
	end := time.After(time.Duration(*duration) * time.Second)

	sent := 0
	
	go func() {
		for {
			select {
			case <-ticker.C:
				conn := connsList[rand.Intn(len(connsList))]
				loc := []byte{0x78, 0x78, 0x1F, 0x12, 0x0B, 0x0A, 0x17, 0x0D, 0x2F, 0x09, 0xCC, 0x02, 0x7A, 0xC7, 0xEB, 0x0C, 0x46, 0x58, 0x49, 0x00, 0x14, 0x8F, 0x01, 0xCC, 0x00, 0x28, 0x7D, 0x00, 0x1F, 0x71, 0x00, 0x00, 0x0D, 0x0A}
				conn.Write(loc)
				sent++
			case <-end:
				return
			}
		}
	}()

	<-end
	ticker.Stop()
	log.Printf("Load test completed. Sent %d messages in %ds. Actual rate: %.2f msg/s", sent, *duration, float64(sent)/float64(*duration))
	
	for _, c := range connsList {
		c.Close()
	}
}
