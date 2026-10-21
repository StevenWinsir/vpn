package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gorm.io/gorm"
	"vpn/backend/internal/api"
	"vpn/backend/internal/authmail"
	"vpn/backend/internal/config"
	"vpn/backend/internal/maintenance"
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
	if c.AgentEnabled() {
		agentServer, agentErr := newAgentServer(db, c)
		if agentErr != nil {
			log.Fatal(agentErr)
		}
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = agentServer.Shutdown(shutdown)
		}()
		go func() {
			log.Printf("node-agent API (mTLS) listening on %s", c.NodeAgentAddr)
			if e := agentServer.ListenAndServeTLS("", ""); e != nil && e != http.ErrServerClosed {
				log.Fatal(e)
			}
		}()
		go reconcileLoop(ctx, db, c)
	}
	// Presence and usage-history housekeeping; one instance at a time via an advisory lock.
	maintenanceDone := make(chan struct{})
	go func() {
		defer close(maintenanceDone)
		maintenance.New(db, c.Schema).Run(ctx)
	}()
	defer func() {
		select {
		case <-maintenanceDone:
		case <-time.After(10 * time.Second):
		}
	}()
	mailDone := startAuthMail(ctx, db, c)
	log.Printf("API listening on http://%s", c.Addr)
	if err = srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	// The worker cancels in-flight SMTP calls with ctx; never wait on a stuck server.
	select {
	case <-mailDone:
	case <-time.After(authmail.SendTimeout + 5*time.Second):
		log.Print("auth mail worker did not stop in time; unfinished jobs resume after their lease")
	}
}

// startAuthMail runs the mail worker and the auth-data cleanup with the API lifecycle.
// Without complete mail configuration nothing runs and /meta reports mail as unavailable.
func startAuthMail(ctx context.Context, db *gorm.DB, c config.Config) <-chan struct{} {
	done := make(chan struct{})
	if !c.MailEnabled() {
		close(done)
		if c.Env == "production" {
			log.Print("WARNING: SMTP is not configured; registration is closed and password reset mail is unavailable")
		}
		return done
	}
	service := authmail.NewService(db, c)
	worker := authmail.NewWorker(service, authmail.NewSMTPMailer(c))
	go func() {
		defer close(done)
		worker.Run(ctx)
	}()
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			if _, err := authmail.Cleanup(ctx, db, c.Schema, time.Now()); err != nil && ctx.Err() == nil {
				log.Print("auth mail cleanup failed (will retry)")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	log.Printf("auth mail worker started (registration open: %t)", c.RegistrationOpen())
	return done
}

// newAgentServer serves the node-agent API. mTLS is mandatory: a connection without a
// client certificate chained to NODE_AGENT_CLIENT_CA_FILE never reaches a handler.
func newAgentServer(db *gorm.DB, c config.Config) (*http.Server, error) {
	pem, err := os.ReadFile(c.NodeAgentClientCAFile)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("NODE_AGENT_CLIENT_CA_FILE contains no certificate")
	}
	cert, err := tls.LoadX509KeyPair(c.NodeAgentCertFile, c.NodeAgentKeyFile)
	if err != nil {
		return nil, err
	}
	return &http.Server{
		Addr: c.NodeAgentAddr, Handler: api.NewAgent(db, c),
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool},
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: c.RequestTimeout + 5*time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
	}, nil
}

func reconcileLoop(ctx context.Context, db *gorm.DB, c config.Config) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			released, err := api.ReconcileGrants(ctx, db, c)
			if err != nil {
				log.Print("grant reconciliation failed (will retry)")
			} else if released > 0 {
				log.Printf("grant reconciliation released %d lapsed grants", released)
			}
		}
	}
}
