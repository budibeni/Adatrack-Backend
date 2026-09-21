package main

import (
	"fmt"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	hash := "$2a$12$/9O9AuDO2dREDGhkoPBs9eTmojhyj7t52Lah4VP/ZwWcN1HGt1zrW"
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("Admin@123"))
	fmt.Println("Admin@123:", err == nil)
	err2 := bcrypt.CompareHashAndPassword([]byte(hash), []byte("password"))
	fmt.Println("password:", err2 == nil)
}
