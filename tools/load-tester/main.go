package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	fmt.Println("Starting Adatrack Multi-Tenant Load Tester...")
	// Dummy load tester for now that actually does HTTP requests
	// In a real scenario, this would send TCP packets and hit WS endpoints.
	var successCount int64
	var errorCount int64

	start := time.Now()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			client := &http.Client{Timeout: 5 * time.Second}
			for j := 0; j < 100; j++ {
				req, _ := http.NewRequest("GET", "http://localhost:8080/healthz", nil)
				resp, err := client.Do(req)
				if err == nil && resp.StatusCode == 200 {
					atomic.AddInt64(&successCount, 1)
					resp.Body.Close()
				} else {
					atomic.AddInt64(&errorCount, 1)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(start)
	fmt.Printf("Load test completed in %v\n", duration)
	fmt.Printf("Success: %d\n", successCount)
	fmt.Printf("Errors: %d\n", errorCount)
}
