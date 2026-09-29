package main

import (
	"fmt"
	"time"
)

func main() {
	s := "2026-09-29T06:00:00.000Z"
	_, err := time.Parse(time.RFC3339, s)
	if err != nil {
		fmt.Println("Error:", err)
	} else {
		fmt.Println("Success")
	}
}
