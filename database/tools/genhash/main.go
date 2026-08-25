// Command genhash generates a bcrypt hash for a given plaintext password.
//
// Purpose: produce consistent hashes for seed/migration SQL files (e.g. the
// fixed dev seeds in database/seed and the platform tenant migration 012).
// Usage (from backend/):
//
//	go run ./database/tools/genhash -password 'Platform@123'
package main

import (
	"flag"
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	password := flag.String("password", "", "plaintext password to hash")
	cost := flag.Int("cost", 10, "bcrypt cost (prod target 12)")
	flag.Parse()

	if *password == "" {
		fmt.Fprintln(os.Stderr, "error: -password is required")
		os.Exit(1)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(*password), *cost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(hash))
}
