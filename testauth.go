package main

import (
	"database/sql"
	"fmt"
	"net/url"
	"time"

	_ "github.com/lib/pq"
)

func main() {
	dbUser := "adatrack_gps_user"
	dbPass := "adatrack_gps_password!"
	dbName := "adatrack_gps_master"
	dbHost := "localhost"

	importURL := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(dbUser, dbPass),
		Host:   dbHost + ":5432",
		Path:   dbName,
		RawQuery: "sslmode=disable",
	}
	dbURL := importURL.String()
	fmt.Println("URL:", dbURL)

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		panic(err)
	}
	
	for i := 0; i < 10; i++ {
		err = db.Ping()
		if err == nil {
			fmt.Println("Connected successfully!")
			return
		}
		fmt.Println("Ping failed:", err)
		time.Sleep(1 * time.Second)
	}
}
