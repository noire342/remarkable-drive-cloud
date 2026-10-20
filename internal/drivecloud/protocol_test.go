// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/ddvk/rmfakecloud/internal/app"
	"github.com/ddvk/rmfakecloud/internal/common"
	"github.com/ddvk/rmfakecloud/internal/config"
	"github.com/ddvk/rmfakecloud/internal/storage/models"
	"github.com/golang-jwt/jwt/v4"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNativeHTTPRequiresDurableDriveCommit(t *testing.T) {
	e, remote := setupEngine(t)
	doc := addFixture(t, e)
	key := []byte(strings.Repeat("a", 32))
	cfg := &config.Config{DataDir: t.TempDir(), TabletOnly: true, HashSchemaVersion: "4", JWTSecretKey: key, CloudHost: "local.appspot.com"}
	bridge := app.NewAppWithBackend(cfg, e)
	claims := &app.UserClaims{Profile: app.Auth0profile{UserID: "tablet"}, DeviceID: "test", Scopes: "sync:tortoise", Version: 10, StandardClaims: jwt.StandardClaims{ExpiresAt: time.Now().Add(time.Hour).Unix()}}
	token, err := common.SignClaims(claims, key)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, body []byte, authorized bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		if authorized {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		bridge.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/sync/v4/root", nil, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := request("GET", "/sync/v4/root", nil, true)
	if w.Code != 200 {
		t.Fatalf("root status %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get(common.GCPHashHeader) != crc(w.Body.Bytes()) {
		t.Fatal("root CRC32C missing or incorrect")
	}
	var root struct {
		Schema     int   `json:"schemaVersion"`
		Generation int64 `json:"generation"`
	}
	json.Unmarshal(w.Body.Bytes(), &root)
	if root.Schema != 4 || root.Generation != 1 {
		t.Fatal("wrong native schema or generation")
	}
	for _, path := range []string{"/sync/v3/check-files", "/sync/v3/missing"} {
		method, body := "GET", []byte(nil)
		if path == "/sync/v3/check-files" {
			method, body = "POST", []byte(`{"files":[]}`)
		}
		response := request(method, path, body, true)
		if response.Code != 200 || response.Header().Get(common.GCPHashHeader) != crc(response.Body.Bytes()) {
			t.Fatalf("native integrity response invalid for %s: %d %s", path, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "null") {
			t.Fatalf("native integrity response has null instead of an empty array: %s", response.Body.String())
		}
	}
	if err = e.updateMetadata(doc, map[string]any{"visibleName": "Pending"}); err != nil {
		t.Fatal(err)
	}
	entries := []*models.HashEntry{{Hash: doc.Hash, Type: "0", EntryName: doc.ID, Subfiles: len(doc.Entries)}}
	for _, part := range doc.Entries {
		entries[0].Size += part.Size
	}
	data := indexBytes(entries, "4", true)
	hash := digest(data)
	if err = e.putObject(hash, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	remote.failRole = "snapshot"
	body, _ := json.Marshal(map[string]any{"hash": hash, "generation": 1})
	if w = request("PUT", "/sync/v3/root", body, true); w.Code != 500 {
		t.Fatalf("failed remote commit acknowledged: %d", w.Code)
	}
	if e.Status().Generation != 1 {
		t.Fatal("generation advanced before remote durability")
	}
	remote.failRole = ""
	if w = request("PUT", "/sync/v3/root", body, true); w.Code != http.StatusOK {
		t.Fatalf("retry failed: %d %s", w.Code, w.Body.String())
	}
	if _, err = e.VerifyRemote(context.Background()); err != nil {
		t.Fatal(err)
	}
	body, _ = json.Marshal(map[string]any{"hash": strings.Repeat("b", 64), "generation": 0})
	if w = request("PUT", "/sync/v3/root", body, true); w.Code != 412 {
		t.Fatalf("concurrent generation accepted: %d", w.Code)
	}
	if w = request("GET", "/ui/api/documents", nil, true); w.Code != 404 {
		t.Fatalf("legacy web UI exposed: %d", w.Code)
	}
}
