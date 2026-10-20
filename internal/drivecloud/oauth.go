// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

const DriveScope = "https://www.googleapis.com/auth/drive"

type Credentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}
type Authorization struct {
	Credentials Credentials   `json:"credentials"`
	Token       *oauth2.Token `json:"token"`
}

func ReadClient(path string) (Credentials, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Credentials{}, err
	}
	var data struct {
		Installed Credentials `json:"installed"`
	}
	if err = json.Unmarshal(b, &data); err != nil {
		return Credentials{}, fmt.Errorf("invalid OAuth client JSON")
	}
	if data.Installed.ClientID == "" || data.Installed.ClientSecret == "" {
		return Credentials{}, fmt.Errorf("select an OAuth Desktop application client")
	}
	return data.Installed, nil
}
func oauthConfig(c Credentials) *oauth2.Config {
	return &oauth2.Config{ClientID: c.ClientID, ClientSecret: c.ClientSecret, Scopes: []string{DriveScope}, Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token"}}
}

// AuthorizeDesktop never asks for a Google password. PKCE and the state bind
// the external browser response to this short-lived loopback listener.
func AuthorizeDesktop(ctx context.Context, c Credentials, show func(string)) (*Authorization, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer listener.Close()
	cfg := oauthConfig(c)
	cfg.RedirectURL = "http://" + listener.Addr().String() + "/callback"
	state := oauth2.GenerateVerifier()
	verifier := oauth2.GenerateVerifier()
	type result struct {
		token *oauth2.Token
		err   error
	}
	done := make(chan result, 1)
	var once sync.Once
	mux := http.NewServeMux()
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	mux.HandleFunc("GET /callback", func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid authorization state", 400)
			return
		}
		once.Do(func() {
			if r.URL.Query().Get("error") != "" {
				done <- result{err: fmt.Errorf("Google authorization denied")}
				http.Error(w, "Authorization denied", 400)
				return
			}
			exchangeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			token, e := cfg.Exchange(exchangeCtx, r.URL.Query().Get("code"), oauth2.VerifierOption(verifier))
			if e != nil || token.RefreshToken == "" {
				done <- result{err: fmt.Errorf("Google did not issue offline access; reconnect and grant consent")}
				http.Error(w, "Authorization could not be completed", 400)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, "Google Drive collegato. Puoi chiudere questa pagina.")
			done <- result{token: token}
		})
	})
	go server.Serve(listener)
	show(cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"), oauth2.S256ChallengeOption(verifier)))
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-done:
		if res.err != nil {
			return nil, res.err
		}
		return &Authorization{Credentials: c, Token: res.token}, nil
	}
}

type persistedTokens struct {
	mu          sync.Mutex
	source      oauth2.TokenSource
	last        string
	path        string
	credentials Credentials
	err         error
}

func (t *persistedTokens) Token() (*oauth2.Token, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	token, err := t.source.Token()
	if err != nil {
		return nil, ErrNotLinked
	}
	b, err := json.Marshal(token)
	if err != nil {
		return nil, err
	}
	if string(b) != t.last {
		if err = saveJSON(t.path, Authorization{Credentials: t.credentials, Token: token}); err != nil {
			return nil, err
		}
		t.last = string(b)
	}
	return token, nil
}
func AuthorizedClient(ctx context.Context, path string) (*http.Client, error) {
	var auth Authorization
	if err := loadJSON(path, &auth); err != nil {
		return nil, ErrNotLinked
	}
	if auth.Token == nil || auth.Token.RefreshToken == "" || auth.Credentials.ClientID == "" {
		return nil, ErrNotLinked
	}
	source := &persistedTokens{source: oauthConfig(auth.Credentials).TokenSource(ctx, auth.Token), path: path, credentials: auth.Credentials}
	client := oauth2.NewClient(ctx, source)
	client.Timeout = 90 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client, nil
}
func SaveAuthorization(path string, auth *Authorization) error { return saveJSON(path, auth) }
