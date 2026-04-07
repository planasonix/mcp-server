package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/planasonix/mcp-server/auth"
	"github.com/planasonix/mcp-server/middleware"
	"github.com/planasonix/mcp-server/server"
	"github.com/planasonix/mcp-server/tools"
)

func main() {
	cfg := loadConfig()

	// ── Key store ───────────────────────────────────────────────────────
	var keyStore auth.KeyStore

	if cfg.DatabaseURL != "" {
		dbStore, err := auth.NewDBKeyStore(cfg.DatabaseURL)
		if err != nil {
			log.Fatalf("[planasonix-mcp] Failed to connect to database: %v", err)
		}
		defer dbStore.Close()
		keyStore = dbStore
		log.Println("[planasonix-mcp] Using DB-backed key store (user_api_keys)")
	} else {
		log.Println("[planasonix-mcp] WARNING: DATABASE_URL not set — falling back to in-memory key store (dev only)")
		mem := auth.NewInMemoryKeyStore()
		mem.Register(
			"plx_live_examplekey123",
			"org_acme",
			"Acme Corp",
			[]string{"pipelines:read", "pipelines:write", "connectors:read"},
		)
		keyStore = mem
	}

	// ── Backend API client ──────────────────────────────────────────────
	client := tools.NewHTTPClient(cfg.PlanasonixAPI)

	// ── Transport selection ─────────────────────────────────────────────
	if cfg.Transport == "stdio" {
		runStdio(keyStore, client)
		return
	}

	runHTTP(cfg, keyStore, client)
}

// runStdio starts the stdio transport. The API key is read from
// PLANASONIX_API_KEY and validated once at startup.
func runStdio(keyStore auth.KeyStore, client tools.PlanasonixClient) {
	apiKey := os.Getenv("PLANASONIX_API_KEY")
	if apiKey == "" {
		log.Fatal("[planasonix-mcp] PLANASONIX_API_KEY is required in stdio mode")
	}

	orgCtx, err := keyStore.Validate(apiKey)
	if err != nil {
		log.Fatalf("[planasonix-mcp] Invalid API key: %v", err)
	}

	log.SetOutput(os.Stderr)
	log.Printf("[planasonix-mcp] stdio mode — org=%s (%s)", orgCtx.OrgName, orgCtx.OrgID)

	srv := server.NewStdio(*orgCtx, client)
	if err := srv.Run(); err != nil {
		log.Fatalf("[planasonix-mcp] stdio error: %v", err)
	}
}

// runHTTP starts the HTTP+SSE transport (default).
func runHTTP(cfg appConfig, keyStore auth.KeyStore, client tools.PlanasonixClient) {
	mcpServer := server.New(server.Config{
		Port:         cfg.Port,
		PlanasonixAPI: cfg.PlanasonixAPI,
		RateLimitRPM: cfg.RateLimitRPM,
	}, keyStore, client)

	handler := middleware.Chain(
		mcpServer,
		middleware.RequestID,
		middleware.Logger,
		middleware.Recover,
	)

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("[planasonix-mcp] HTTP+SSE mode — listening on :%s  →  backend %s", cfg.Port, cfg.PlanasonixAPI)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[planasonix-mcp] Shutting down gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("[planasonix-mcp] Shutdown error: %v", err)
	}
	log.Println("[planasonix-mcp] Stopped.")
}

type appConfig struct {
	Port          string
	PlanasonixAPI string
	DatabaseURL   string
	RateLimitRPM  int
	Transport     string // "http" (default) or "stdio"
}

func loadConfig() appConfig {
	transport := os.Getenv("MCP_TRANSPORT")
	if transport == "" {
		// Check for --stdio CLI flag
		for _, arg := range os.Args[1:] {
			if arg == "--stdio" {
				transport = "stdio"
				break
			}
		}
	}
	if transport == "" {
		transport = "http"
	}

	return appConfig{
		Port:          getEnv("PORT", "8080"),
		PlanasonixAPI: getEnv("PLANASONIX_API_URL", "http://localhost:9000"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		RateLimitRPM:  60,
		Transport:     transport,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
