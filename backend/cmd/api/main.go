package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"vpn/backend/internal/api"
	"vpn/backend/internal/config"
	"vpn/backend/internal/operations"
	"vpn/backend/internal/store"
)

func main() {
	migrate := flag.Bool("migrate", false, "run additive database migration and exit")
	adminEmail := flag.String("grant-admin", "", "explicitly grant admin to one existing active account and exit")
	createAdmin := flag.String("create-admin", "", "create a separate administrator; password JSON is read from stdin, never arguments")
	flag.Parse()
	if (*migrate && (*adminEmail != "" || *createAdmin != "")) || (*adminEmail != "" && *createAdmin != "") {
		log.Fatal("choose exactly one console operation")
	}
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
	if *adminEmail != "" || *createAdmin != "" {
		ctx, cancel := context.WithTimeout(context.Background(), c.RequestTimeout)
		defer cancel()
		if *createAdmin != "" {
			var input struct {
				Password string `json:"password"`
			}
			decoder := json.NewDecoder(io.LimitReader(os.Stdin, 1024))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
				log.Fatal("expected one password JSON object on stdin")
			}
			err = operations.CreateAdmin(ctx, db, *createAdmin, input.Password, c.BcryptCost)
		} else {
			err = operations.GrantAdmin(ctx, db, *adminEmail)
		}
		if err != nil {
			log.Fatal(err)
		}
		log.Print("administrator operation completed and audited; existing passwords unchanged")
		return
	}
	if c.SSLMode == "disable" {
		log.Print("WARNING: development database connection is not encrypted; configure verified TLS before production")
	}
	if c.TestPurchase {
		log.Print("WARNING: TEST PURCHASE ENABLED; no real payment is processed; any enabled proxy access is development-only and client-reported")
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
