package main

import (
	"fmt"
	"time"
)

func main() {
	fmt.Println("Starting worker-live service...")
	for {
		time.Sleep(5 * time.Second)
		// Redis consumer logic here
	}
}
