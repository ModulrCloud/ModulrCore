package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
)

func startAlfpBlockingProxy(targetRawURL string, blockGenesisEpoch bool) (string, func(), error) {
	targetURL, err := url.Parse(targetRawURL)
	if err != nil {
		return "", nil, err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/accept_aggregated_leader_finalization_proof" {
				if shouldBlockAlfpPost(r, blockGenesisEpoch) {
					http.Error(w, "ALFP POST intentionally blocked by alfp_pull_smoke", http.StatusServiceUnavailable)
					return
				}
			}
			proxy.ServeHTTP(w, r)
		}),
	}

	go func() {
		_ = server.Serve(listener)
	}()

	closeFn := func() {
		_ = server.Close()
	}
	return "http://" + listener.Addr().String(), closeFn, nil
}

func shouldBlockAlfpPost(r *http.Request, blockGenesisEpoch bool) bool {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return true
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))

	var payload struct {
		LeaderFinalizations []struct {
			EpochIndex int `json:"epochIndex"`
		} `json:"leaderFinalizations"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return true
	}
	for _, proof := range payload.LeaderFinalizations {
		if blockGenesisEpoch && proof.EpochIndex >= 0 {
			return true
		}
		if proof.EpochIndex > 0 {
			return true
		}
	}
	return false
}

func rewriteCoreAnchorsHTTPURL(runDir string, proxyURL string) error {
	path := filepath.Join(runDir, "network", "core-1", "anchors.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var anchors []map[string]any
	if err := json.Unmarshal(raw, &anchors); err != nil {
		return err
	}
	if len(anchors) == 0 {
		return errors.New("core anchors.json has no anchors")
	}
	anchors[0]["anchorURL"] = proxyURL
	return writeJSON(path, anchors)
}

func rewriteCoreAnchorHTTPURLForAll(runDir string, fromURL string, toURL string) error {
	for coreIndex := 1; ; coreIndex++ {
		path := filepath.Join(runDir, "network", fmt.Sprintf("core-%d", coreIndex), "anchors.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) && coreIndex > 1 {
				return nil
			}
			return err
		}
		var anchors []map[string]any
		if err := json.Unmarshal(raw, &anchors); err != nil {
			return err
		}
		updated := false
		for _, anchor := range anchors {
			if anchor["anchorURL"] == fromURL {
				anchor["anchorURL"] = toURL
				updated = true
			}
		}
		if !updated {
			return fmt.Errorf("no anchorURL %q found in %s", fromURL, path)
		}
		if err := writeJSON(path, anchors); err != nil {
			return err
		}
	}
}
