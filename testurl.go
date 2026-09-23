package main

import (
	"fmt"
	"net/url"
)

func main() {
	importURL := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword("adatrack", "pass@word?"),
		Host:   "postgres:5432",
		Path:   "adatrack",
		RawQuery: "sslmode=disable",
	}
	fmt.Println(importURL.String())
}
