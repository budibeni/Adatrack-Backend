package main
import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v4/pgxpool"
	"golang.org/x/crypto/bcrypt"
)
func main() {
	ctx := context.Background()
	connStr := "postgres://adatrack_gps_user:adatrack_gps_password@localhost:5432/adatrack_gps_master"
	pool, err := pgxpool.Connect(ctx, connStr)
	if err != nil { panic(err) }
	defer pool.Close()

	hash, _ := bcrypt.GenerateFromPassword([]byte("admin@123"), 12)
	_, err = pool.Exec(ctx, "UPDATE adatrack_gps_master.tm_users SET password_hash = $1 WHERE email = 'admin@tesst001.local'", string(hash))
	fmt.Println("Err:", err)
}
