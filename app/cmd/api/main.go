package main

import (
	"log"
	"net/http"

	"github.com/gorilla/mux"

	"clearoutspaces/internal/config"
	"clearoutspaces/internal/database"
	"clearoutspaces/internal/handlers"
	"clearoutspaces/internal/llm"
)

func main() {
	// 1. Load and validate all environment variables — fail fast if any are missing.
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// 2. Load and compile the YAML system prompt.
	llm.LoadPrompt("templates/system_prompt.yaml")

	// 3. Initialise the SQLite database and run migrations.
	db := database.Init(cfg.DBPath)

	// 4. Set up the router.
	r := mux.NewRouter()

	r.HandleFunc("/health", handlers.HealthCheck).Methods(http.MethodGet)

	// Meta / WhatsApp routes.
	r.HandleFunc("/whatsapp/webhook", handlers.VerifyWebhook(cfg)).Methods(http.MethodGet)
	r.HandleFunc("/whatsapp/webhook", handlers.HandleWhatsAppMessage(db, cfg)).Methods(http.MethodPost)

	// Slack interactive route.
	r.HandleFunc("/slack/interactive", handlers.HandleSlackInteractive(db, cfg)).Methods(http.MethodPost)

	// 5. Start the server.
	addr := ":8080"
	log.Printf("server: listening on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("server: %v", err)
	}
}
