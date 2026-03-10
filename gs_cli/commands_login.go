package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/niczy/gitslice/internal/auth"
)

func handleLogin(currentAuth cliAuth, args []string) {
	if len(args) == 0 {
		if strings.TrimSpace(currentAuth.Authorization) == "" {
			fmt.Println("Not logged in. Usage: gs login <username>")
			return
		}
		if strings.TrimSpace(currentAuth.Username) != "" {
			fmt.Printf("Logged in as: %s\n", strings.TrimSpace(currentAuth.Username))
			return
		}
		fmt.Printf("Authenticated via %s\n", strings.TrimSpace(currentAuth.Source))
		return
	}

	username := strings.TrimSpace(args[0])
	if !auth.ValidateUsername(username) {
		log.Fatalf("Invalid username %q (expected 3..32 chars; alnum/_/-; must start with alnum)", username)
	}
	if err := writeUsernameConfig(username); err != nil {
		log.Fatalf("Failed to save login: %v", err)
	}
	fmt.Printf("Logged in as: %s\n", username)
}
