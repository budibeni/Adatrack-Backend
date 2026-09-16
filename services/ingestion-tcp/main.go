package main

import (
	"fmt"
	"log"
	"net"
)

func main() {
	fmt.Println("Starting ingestion-tcp service on :5001")
	ln, err := net.Listen("tcp", ":5001")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept error: %v", err)
			continue
		}
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	// TCP handler logic here
}
