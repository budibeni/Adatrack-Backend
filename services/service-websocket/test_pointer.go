package main

import (
	"encoding/json"
	"fmt"
)

type UserInfo struct {
	ID      int      `json:"id"`
	Tenants []string `json:"tenants"`
}

func main() {
	users := []UserInfo{
		{ID: 1, Tenants: []string{}},
	}

	userMap := make(map[int]*UserInfo)
	for i := range users {
		userMap[users[i].ID] = &users[i]
	}

	user, ok := userMap[1]
	if ok {
		user.Tenants = append(user.Tenants, "COMP1")
		user.Tenants = append(user.Tenants, "COMP2")
	}

	b, _ := json.Marshal(users)
	fmt.Println(string(b))
}
