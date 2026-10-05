package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"time"

	"vpn/backend/internal/config"
	"vpn/backend/internal/operations"
	"vpn/backend/internal/store"
)

func main() {
	email := flag.String("email", "", "inspect this account without modifying it")
	flag.Parse()
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	db, err := store.Open(cfg)
	if err != nil {
		log.Fatal("database connection failed; connection details suppressed")
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	report, err := operations.Inspect(ctx, db, cfg, *email)
	if err != nil {
		log.Fatal(err)
	}
	output := json.NewEncoder(os.Stdout)
	output.SetIndent("", "  ")
	if err := output.Encode(report); err != nil {
		log.Fatal("readiness output failed")
	}
}
