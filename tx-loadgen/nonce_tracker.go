package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

type accountState struct {
	Balance uint64 `json:"Balance"`
	Nonce   uint64 `json:"Nonce"`
}

type nonceTracker struct {
	mu      sync.RWMutex
	nonces  map[string]uint64
	senders []Sender
}

func newNonceTracker(senders []Sender) *nonceTracker {
	return &nonceTracker{nonces: make(map[string]uint64), senders: senders}
}

func (nt *nonceTracker) Prime(ctx context.Context, client *http.Client, nodeURL string) error {
	for _, sender := range nt.senders {
		nonce, err := fetchNonce(ctx, client, nodeURL, sender.PublicKey)
		if err != nil {
			return fmt.Errorf("fetch nonce for %s: %w", sender.PublicKey, err)
		}
		nt.mu.Lock()
		nt.nonces[sender.PublicKey] = nonce + 1
		nt.mu.Unlock()
	}
	return nil
}

func (nt *nonceTracker) Next(account string) (uint64, bool) {
	nt.mu.RLock()
	defer nt.mu.RUnlock()
	nonce, ok := nt.nonces[account]
	return nonce, ok
}

func (nt *nonceTracker) Increment(account string) {
	nt.mu.Lock()
	defer nt.mu.Unlock()
	nt.nonces[account]++
}

// Reserve returns the next nonce for the given account while reserving it to
// avoid concurrent reuse. The returned release function should be invoked with
// success=true once the transaction using this nonce is submitted successfully;
// otherwise pass success=false to release the reservation and retry with the
// same nonce later.
func (nt *nonceTracker) Reserve(account string) (nonce uint64, release func(success bool), ok bool) {
	nt.mu.Lock()
	defer nt.mu.Unlock()

	nonce, ok = nt.nonces[account]
	if !ok {
		return 0, func(bool) {}, false
	}

	nt.nonces[account]++

	return nonce, func(success bool) {
		if success {
			return
		}

		nt.mu.Lock()
		nt.nonces[account]--
		nt.mu.Unlock()
	}, true
}

func fetchNonce(ctx context.Context, client *http.Client, nodeURL, account string) (uint64, error) {
	url := strings.TrimRight(nodeURL, "/") + "/account/" + account
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	var state accountState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}

	return state.Nonce, nil
}
