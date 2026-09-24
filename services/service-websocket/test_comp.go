package main
import (
    "context"
    "fmt"
    "backend/internal/config"
    "backend/internal/dbclient"
)
func main() {
    config.LoadConfig()
    dbclient.InitDB(config.AppConfig)
    rows, _ := dbclient.Pool.Query(context.Background(), "SELECT code, business_type FROM adatrack_gps_master.tm_companies")
    for rows.Next() {
        var code, bt string
        rows.Scan(&code, &bt)
        fmt.Println(code, bt)
    }
}
