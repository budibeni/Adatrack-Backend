package main

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dbUrl := "postgres://postgres:postgres@localhost:5432/adatrack?sslmode=disable"
	pool, err := pgxpool.New(context.Background(), dbUrl)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	rows, err := pool.Query(context.Background(), "SELECT id, plate_number, current_lat, current_lon FROM adatrack_gps_binago.tm_vehicles WHERE current_lat = 0 OR current_lon = 0 OR current_lat IS NULL")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var plate string
		var lat, lon *float64
		rows.Scan(&id, &plate, &lat, &lon)
		fmt.Printf("ID: %d, Plate: %s, Lat: %v, Lon: %v\n", id, plate, lat, lon)
	}
}
