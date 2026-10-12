// Command asterlink-nodeagent runs next to a proxy forwarder on a node. It makes the
// node the authority for who may use it and how much they used (handoff_app.md 7.3).
package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"asterlink/nodeagent/agent"
	"asterlink/nodeagent/control"
	"asterlink/nodeagent/forwarder"
)

// version is stamped at build time (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	envFile := flag.String("env", "", "path to the agent env file (see .env.example)")
	printFingerprint := flag.Bool("print-fingerprint", false, "print the SHA-256 fingerprint of the client certificate (to register in the admin console) and exit")
	flag.Parse()
	file, err := parseEnvFile(*envFile)
	if err != nil {
		log.Fatal(err)
	}
	cfg, err := loadConfig(file, os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	cert, err := tls.LoadX509KeyPair(cfg.ClientCertFile, cfg.ClientKeyFile)
	if err != nil {
		log.Fatalf("client certificate: %v", err)
	}
	if *printFingerprint {
		sum := sha256.Sum256(cert.Certificate[0])
		fmt.Println(hex.EncodeToString(sum[:]))
		return
	}
	var roots *x509.CertPool
	if cfg.ServerCAFile != "" {
		pem, readErr := os.ReadFile(cfg.ServerCAFile)
		if readErr != nil {
			log.Fatalf("server CA: %v", readErr)
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			log.Fatal("server CA file contains no certificate")
		}
	}
	api, err := control.New(cfg.APIURL, cert, roots)
	if err != nil {
		log.Fatal(err)
	}
	opts := forwarder.VLESSOptions{Listen: cfg.VLESSListen, AllowPrivateTargets: cfg.AllowPrivateTargets}
	if cfg.VLESSTLSCertFile != "" {
		pair, loadErr := tls.LoadX509KeyPair(cfg.VLESSTLSCertFile, cfg.VLESSTLSKeyFile)
		if loadErr != nil {
			log.Fatalf("VLESS TLS certificate: %v", loadErr)
		}
		opts.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	} else {
		log.Print("WARNING: serving VLESS without TLS (NODEAGENT_ALLOW_PLAINTEXT)")
	}
	if cfg.AllowPrivateTargets {
		log.Print("WARNING: private and loopback destinations are reachable through this node")
	}
	fwd, err := forwarder.NewVLESS(opts)
	if err != nil {
		log.Fatal(err)
	}
	ag, err := agent.New(agent.Options{API: api, Fwd: fwd, StatePath: cfg.StateFile, Version: version, Logf: log.Printf})
	if err != nil {
		_ = fwd.Close()
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("node agent %s epoch %d forwarding vless on %s", version, ag.Epoch(), fwd.Addr())
	ag.Run(ctx, cfg.SyncEvery, cfg.ReportEvery)
	log.Print("node agent stopped")
}
