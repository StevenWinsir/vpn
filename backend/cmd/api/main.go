package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"vpn/backend/internal/api"
	"vpn/backend/internal/config"
	"vpn/backend/internal/store"
)

func main() {
	migrate := flag.Bool("migrate", false, "run additive database migration and exit")
	flag.Parse()
	c, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	db, err := store.Open(c)
	if err != nil {
		log.Fatal("database connection failed; check backend .env, connectivity and SSL settings (credentials suppressed)")
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	if *migrate || c.AutoMigrate {
		if err = store.Migrate(db, c); err != nil {
			log.Fatal("database migration failed (details suppressed); inspect database permissions/schema")
		}
	}
	if *migrate {
		log.Print("database migration completed")
		return
	}
	if c.SSLMode == "disable" {
		log.Print("WARNING: development database connection is not encrypted; configure verified TLS before production")
	}
	if c.TestPurchase {
		log.Print("WARNING: TEST PURCHASE ENABLED; no real payment or proxy service is provided")
	}
	handler := api.New(db, c)
	srv := &http.Server{Addr: c.Addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: c.RequestTimeout + 5*time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("API listening on http://%s", c.Addr)
	if err = srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
