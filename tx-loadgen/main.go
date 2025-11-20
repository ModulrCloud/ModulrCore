package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

func main() {
	configPath := flag.String("config", "config.json", "Path to load generator config")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	client := &http.Client{Timeout: cfg.requestTimeout()}
	nonceTracker := newNonceTracker(cfg.Senders)

	if err := nonceTracker.Prime(context.Background(), client, cfg.NodeURL); err != nil {
		log.Fatalf("failed to initialize nonces: %v", err)
	}

	if cfg.Faucet.Enabled {
		if err := cfg.startFaucetServer(client, nonceTracker); err != nil {
			log.Fatalf("failed to start faucet server: %v", err)
		}
	}

	ticker := time.NewTicker(cfg.tick())
	defer ticker.Stop()

	log.Printf("starting tx load generator: %d txs per sender every %s against %s", cfg.TransactionsPerSender, cfg.tick(), cfg.NodeURL)

	for range ticker.C {
		for _, sender := range cfg.Senders {
			for i := 0; i < cfg.TransactionsPerSender; i++ {
				nonce, release, ok := nonceTracker.Reserve(sender.PublicKey)
				if !ok {
					log.Printf("skip tx: nonce not initialized for %s", sender.PublicKey)
					continue
				}

				tx, err := cfg.newTransaction(sender, nonce)
				if err != nil {
					release(false)
					log.Printf("skip tx for %s: %v", sender.PublicKey, err)
					continue
				}

				if err := submitTransaction(context.Background(), client, cfg.NodeURL, tx); err != nil {
					release(false)
					log.Printf("tx submit failed (%s -> %s nonce %d): %v", tx.From, tx.To, tx.Nonce, err)
					continue
				}

				release(true)
				log.Printf("submitted tx: from=%s to=%s amount=%d fee=%d nonce=%d", tx.From, tx.To, tx.Amount, tx.Fee, tx.Nonce)
			}
		}
	}
}

func submitTransaction(ctx context.Context, client *http.Client, nodeURL string, tx Transaction) error {
	txJSON, err := json.Marshal(tx)
	if err != nil {
		return fmt.Errorf("serialize tx: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(nodeURL, "/")+"/transaction", strings.NewReader(string(txJSON)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

func (cfg *Config) newTransaction(sender Sender, nonce uint64) (Transaction, error) {
	recipient, err := sender.pickRecipient()
	if err != nil {
		return Transaction{}, err
	}

	payload := cfg.DefaultPayload
	if len(sender.Payload) > 0 {
		payload = sender.Payload
	}

	return buildTransaction(sender, nonce, sender.Amount, recipient, payload)
}

func (cfg *Config) tick() time.Duration {
	if cfg.TickMS == 0 {
		return time.Second
	}
	return time.Duration(cfg.TickMS) * time.Millisecond
}

func (cfg *Config) requestTimeout() time.Duration {
	if cfg.RequestTimeoutMS == 0 {
		return 10 * time.Second
	}
	return time.Duration(cfg.RequestTimeoutMS) * time.Millisecond
}

func (s *Sender) version() uint {
	if s.Version == 0 {
		return 1
	}
	return s.Version
}

func (s *Sender) pickRecipient() (string, error) {
	if len(s.Recipients) == 0 {
		return "", errors.New("no recipients configured")
	}
	if len(s.Recipients) == 1 {
		return s.Recipients[0], nil
	}
	return s.Recipients[rand.Intn(len(s.Recipients))], nil
}

type faucetRequest struct {
	Address string         `json:"address"`
	Amount  uint64         `json:"amount"`
	Payload map[string]any `json:"payload"`
}

type faucetResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func (cfg *Config) startFaucetServer(client *http.Client, nonces *nonceTracker) error {
	sender, err := cfg.faucetSender()
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/faucet", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(faucetResponse{Status: "error", Error: "POST required"})
			return
		}

		var req faucetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(faucetResponse{Status: "error", Error: "invalid JSON"})
			return
		}

		if req.Address == "" || req.Amount == 0 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(faucetResponse{Status: "error", Error: "address and positive amount required"})
			return
		}

		nonce, release, ok := nonces.Reserve(sender.PublicKey)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(faucetResponse{Status: "error", Error: "nonce unavailable"})
			return
		}

		payload := cfg.DefaultPayload
		if len(cfg.Faucet.Payload) > 0 {
			payload = cfg.Faucet.Payload
		}
		if len(req.Payload) > 0 {
			payload = req.Payload
		}

		tx, err := buildTransaction(*sender, nonce, req.Amount, req.Address, payload)
		if err != nil {
			release(false)
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(faucetResponse{Status: "error", Error: "failed to build tx"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), cfg.requestTimeout())
		defer cancel()

		if err := submitTransaction(ctx, client, cfg.NodeURL, tx); err != nil {
			release(false)
			log.Printf("faucet tx submit failed (%s -> %s nonce %d): %v", tx.From, tx.To, tx.Nonce, err)
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(faucetResponse{Status: "error", Error: "submission failed"})
			return
		}

		release(true)
		log.Printf("faucet submitted tx: from=%s to=%s amount=%d fee=%d nonce=%d", tx.From, tx.To, tx.Amount, tx.Fee, tx.Nonce)
		_ = json.NewEncoder(w).Encode(faucetResponse{Status: "ok"})
	})

	srv := &http.Server{Addr: cfg.faucetListenAddr(), Handler: mux}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("faucet server error: %v", err)
		}
	}()

	log.Printf("faucet server listening on %s using sender %s", cfg.faucetListenAddr(), sender.Name)

	return nil
}
