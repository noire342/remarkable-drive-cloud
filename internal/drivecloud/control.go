// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func ControlToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		token := strings.TrimSpace(string(b))
		if len(token) != 43 {
			return "", fmt.Errorf("invalid control token; refusing replacement")
		}
		return token, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	err = atomicWrite(path, 0600, func(w io.Writer) error { _, err := fmt.Fprintln(w, token); return err })
	return token, err
}

type Control struct {
	Engine  *Engine
	Token   string
	Code    func() (string, error)
	UIReady func(string) error
}

func (c Control) Handler() http.Handler {
	mux := http.NewServeMux()
	output := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /v1/source", func(w http.ResponseWriter, r *http.Request) {
		output(w, 200, map[string]string{"license": "AGPL-3.0-only", "source": "https://github.com/noire342/remarkable-drive-cloud"})
	})
	mux.HandleFunc("POST /v1/ui-ready", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Nonce string `json:"nonce"`
		}
		if c.UIReady == nil || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body) != nil || c.UIReady(body.Nonce) != nil {
			http.Error(w, "UI candidate not recognized", 400)
			return
		}
		output(w, 200, map[string]bool{"ready": true})
	})
	mux.HandleFunc("GET /v1/state", func(w http.ResponseWriter, r *http.Request) { output(w, 200, c.Engine.Status()) })
	mux.HandleFunc("POST /v1/verify", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		count, err := c.Engine.VerifyRemote(ctx)
		if err != nil {
			output(w, 503, map[string]string{"error": err.Error()})
			return
		}
		output(w, 200, map[string]any{"verified_remote_documents": count, "status": c.Engine.Status()})
	})
	mux.HandleFunc("POST /v1/sync", func(w http.ResponseWriter, r *http.Request) {
		c.Engine.Wake()
		output(w, 202, map[string]bool{"queued": true})
	})
	mux.HandleFunc("POST /v1/pause", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Paused bool `json:"paused"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
			http.Error(w, "Invalid request", 400)
			return
		}
		if err := c.Engine.Pause(body.Paused); err != nil {
			output(w, 503, map[string]string{"error": err.Error()})
			return
		}
		if !body.Paused {
			c.Engine.Wake()
		}
		output(w, 200, c.Engine.Status())
	})
	mux.HandleFunc("GET /v1/history", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		entries, err := c.Engine.History(ctx)
		if err != nil {
			output(w, 503, map[string]string{"error": err.Error()})
			return
		}
		output(w, 200, entries)
	})
	mux.HandleFunc("POST /v1/restore", func(w http.ResponseWriter, r *http.Request) {
		if c.Engine.Status().RecoveryNeeded {
			output(w, 503, map[string]string{"error": "Complete the interrupted migration recovery first"})
			return
		}
		var body struct {
			Snapshot string `json:"snapshot_id"`
			Document string `json:"document_id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Snapshot == "" || !validDocumentID(body.Document) {
			http.Error(w, "Invalid restore request", 400)
			return
		}
		id, err := c.Engine.Restore(r.Context(), body.Snapshot, body.Document)
		if err != nil {
			output(w, 503, map[string]string{"error": err.Error()})
			return
		}
		output(w, 200, map[string]string{"document_id": id})
	})
	mux.HandleFunc("POST /v1/pair", func(w http.ResponseWriter, r *http.Request) {
		if c.Code == nil {
			http.Error(w, "Pairing unavailable", 503)
			return
		}
		code, err := c.Code()
		if err != nil {
			http.Error(w, "Pairing unavailable", 503)
			return
		}
		output(w, 200, map[string]string{"code": code})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		address, _, err := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(address)
		if err != nil || ip == nil || !ip.IsLoopback() {
			http.Error(w, "Loopback only", 403)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "null" && origin != "file://" {
			http.Error(w, "Cross-origin requests forbidden", 403)
			return
		}
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(c.Token) != 43 || subtle.ConstantTimeCompare([]byte(provided), []byte(c.Token)) != 1 {
			http.Error(w, "Unauthorized", 401)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
