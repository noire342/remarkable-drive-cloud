// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResumableUploadSurvivesConnectionFailure(t *testing.T) {
	var meta File
	var received []byte
	failed := false
	starts := 0
	queries := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/drive/v3/files/fixed-id":
			if len(received) < 6<<20 {
				http.NotFound(w, r)
				return
			}
			sum := md5.Sum(received)
			meta.MD5 = hex.EncodeToString(sum[:])
			json.NewEncoder(w).Encode(meta)
		case r.URL.Path == "/upload/drive/v3/files":
			starts++
			if err := json.NewDecoder(r.Body).Decode(&meta); err != nil {
				t.Error(err)
			}
			w.Header().Set("Location", server.URL+"/upload/session")
			w.WriteHeader(200)
		case r.URL.Path == "/upload/session":
			cr := r.Header.Get("Content-Range")
			if strings.HasPrefix(cr, "bytes */") {
				queries++
				if len(received) == 6<<20 {
					sum := md5.Sum(received)
					meta.MD5 = hex.EncodeToString(sum[:])
					json.NewEncoder(w).Encode(meta)
					return
				}
				if len(received) > 0 {
					w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", len(received)-1))
				}
				w.WriteHeader(308)
				return
			}
			if r.ContentLength <= 0 {
				t.Error("resumable request used chunked transfer instead of fixed Content-Length")
			}
			var start, end, total int64
			if _, err := fmt.Sscanf(cr, "bytes %d-%d/%d", &start, &end, &total); err != nil {
				t.Error(err)
			}
			if start != int64(len(received)) {
				t.Errorf("offset %d instead of %d", start, len(received))
			}
			data, _ := io.ReadAll(r.Body)
			received = append(received, data...)
			if !failed {
				failed = true
				w.WriteHeader(503)
				return
			}
			if int64(len(received)) == total {
				sum := md5.Sum(received)
				meta.MD5 = hex.EncodeToString(sum[:])
				json.NewEncoder(w).Encode(meta)
				return
			}
			w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", len(received)-1))
			w.WriteHeader(308)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "large.pdf")
	data := strings.Repeat("z", 6<<20)
	os.WriteFile(path, []byte(data), 0600)
	drive := &Drive{HTTP: server.Client(), Base: server.URL + "/drive/v3", UploadBase: server.URL + "/upload/drive/v3", Sessions: filepath.Join(dir, "sessions")}
	file := File{ID: "fixed-id", Name: "payload", MIME: "application/pdf", Properties: map[string]string{"role": "object"}}
	if _, err := drive.Put(context.Background(), file, path); err == nil {
		t.Fatal("expected interrupted chunk")
	}
	restarted := &Drive{HTTP: server.Client(), Base: drive.Base, UploadBase: drive.UploadBase, Sessions: drive.Sessions}
	if _, err := restarted.Put(context.Background(), file, path); err != nil {
		t.Fatal(err)
	}
	if string(received) != data || starts != 1 || queries != 2 {
		t.Fatalf("resume did not reuse stored session: starts=%d queries=%d bytes=%d", starts, queries, len(received))
	}
}

func TestDriveCreateOmitsReadOnlyFieldsAndFinalizesEmptyFiles(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/drive/v3/files/empty":
			http.NotFound(w, r)
		case "/upload/drive/v3/files":
			var fields map[string]any
			json.NewDecoder(r.Body).Decode(&fields)
			if _, ok := fields["trashed"]; ok {
				http.Error(w, `{"error":{"errors":[{"reason":"fieldNotWritable"}]}}`, 403)
				return
			}
			w.Header().Set("Location", server.URL+"/upload/empty-session")
		case "/upload/empty-session":
			if r.Header.Get("Content-Range") != "" || r.ContentLength != 0 {
				t.Error("empty file must use a final zero-byte request")
			}
			json.NewEncoder(w).Encode(File{ID: "empty", MD5: "d41d8cd98f00b204e9800998ecf8427e"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	file := filepath.Join(dir, "empty")
	os.WriteFile(file, nil, 0600)
	d := &Drive{HTTP: server.Client(), Base: server.URL + "/drive/v3", UploadBase: server.URL + "/upload/drive/v3", Sessions: filepath.Join(dir, "sessions")}
	if _, err := d.Put(context.Background(), File{ID: "empty", Name: "empty", MIME: "application/octet-stream"}, file); err != nil {
		t.Fatal(err)
	}
}
func TestUploadSessionCannotExfiltrateCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/drive/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Location", "https://attacker.invalid/upload/session")
		w.WriteHeader(200)
	}))
	defer server.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "data")
	os.WriteFile(path, []byte("private content"), 0600)
	drive := &Drive{HTTP: server.Client(), Base: server.URL + "/drive/v3", UploadBase: server.URL + "/upload/drive/v3", Sessions: filepath.Join(dir, "sessions")}
	if _, err := drive.Put(context.Background(), File{ID: "fixed", Name: "private", MIME: "application/pdf"}, path); err == nil || !strings.Contains(err.Error(), "untrusted") {
		t.Fatalf("accepted external upload URL: %v", err)
	}
}
func TestControlRejectsNetworkAndBrowserRequests(t *testing.T) {
	e, _ := setupEngine(t)
	token := strings.Repeat("a", 43)
	handler := (Control{Engine: e, Token: token}).Handler()
	for _, test := range []struct {
		name, remote, origin, auth string
		status                     int
	}{{"remote", "192.168.1.2:1234", "", "Bearer " + token, 403}, {"origin", "127.0.0.1:1234", "https://other.invalid", "Bearer " + token, 403}, {"unauthorized", "127.0.0.1:1234", "", "", 401}, {"authorized", "127.0.0.1:1234", "", "Bearer " + token, 200}} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://127.0.0.1/v1/state", nil)
			r.RemoteAddr = test.remote
			r.Header.Set("Origin", test.origin)
			r.Header.Set("Authorization", test.auth)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != test.status {
				t.Fatalf("HTTP %d", w.Code)
			}
		})
	}
}
func TestOAuthRequiresDesktopClient(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	os.WriteFile(path, []byte(`{"web":{"client_id":"wrong"}}`), 0600)
	if _, err := ReadClient(path); err == nil {
		t.Fatal("web OAuth client accepted")
	}
}
func TestProjectionLostAcknowledgementDoesNotCreateImport(t *testing.T) {
	e, r := setupEngine(t)
	addFixture(t, e)
	r.failRole = "document"
	r.failAfterStore = true
	if e.Cycle(context.Background()) == nil {
		t.Fatal("expected lost export acknowledgement")
	}
	r.failRole = ""
	if err := e.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	g, _ := e.currentGraph()
	if len(g.Documents) != 1 {
		t.Fatalf("own PDF upload treated as external edit: %d documents", len(g.Documents))
	}
}
func TestStatusDoesNotWaitForUploadMutex(t *testing.T) {
	e, _ := setupEngine(t)
	e.mu.Lock()
	done := make(chan bool, 1)
	go func() { e.Status(); done <- true }()
	select {
	case <-done:
	case <-time.After(time.Second):
		e.mu.Unlock()
		t.Fatal("settings blocked by remote upload")
	}
	e.mu.Unlock()
}
func TestSecondProcessCannotOpenLiveState(t *testing.T) {
	e, r := setupEngine(t)
	other, err := NewEngine(e.dir, e.objects, "tablet", r)
	if err == nil {
		other.Close()
		t.Fatal("second writer accepted")
	}
}
func TestBootstrapCannotRecreateMissingInitializedVault(t *testing.T) {
	e, r := setupEngine(t)
	delete(r.files, e.state.Layout.Root)
	if e.Bootstrap(context.Background()) == nil {
		t.Fatal("deleted existing vault recreated")
	}
}
