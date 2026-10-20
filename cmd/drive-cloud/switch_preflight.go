// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"time"

	"github.com/ddvk/rmfakecloud/internal/deviceops"
	"github.com/ddvk/rmfakecloud/internal/drivecloud"
	"github.com/google/uuid"
)

type migrationManifest struct {
	Stable    bool              `json:"source_stable"`
	Files     map[string]string `json:"library_files"`
	Errors    []json.RawMessage `json:"errors"`
	Documents map[string]struct {
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	} `json:"documents"`
}

func privateJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".switch-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(data, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	e := f.Close()
	if err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func checkedBackup(base, backup, evidence string) (migrationManifest, error) {
	var manifest migrationManifest
	if base != "/home/root/drive-cloud" || !filepath.IsAbs(backup) || !filepath.IsAbs(evidence) {
		return manifest, fmt.Errorf("private absolute backup and evidence paths are required")
	}
	for _, path := range []string{backup, evidence} {
		relative, err := filepath.Rel(base, path)
		if err != nil || relative == ".." || len(relative) > 3 && relative[:3] == ".."+string(os.PathSeparator) {
			return manifest, fmt.Errorf("migration input must be inside the private installation")
		}
	}
	if !deviceops.Inspect("/").FirmwareMatch {
		return manifest, fmt.Errorf("unsupported native firmware")
	}
	path := filepath.Join(backup, "manifest.private.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest, err
	}
	if err = json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	if !manifest.Stable || len(manifest.Errors) > 0 || len(manifest.Documents) == 0 || len(manifest.Files) == 0 {
		return manifest, fmt.Errorf("native backup is incomplete or unstable")
	}
	deviceHash, err := deviceops.DeviceIdentityHash("/")
	if err != nil {
		return manifest, err
	}
	digest := sha256.Sum256(data)
	if _, err = deviceops.ReadValidation(evidence, deviceHash, hex.EncodeToString(digest[:]), time.Now().UTC()); err != nil {
		return manifest, err
	}
	validUUID := regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
	for id, entry := range manifest.Documents {
		if !validUUID.MatchString(id) {
			return manifest, fmt.Errorf("invalid backup UUID")
		}
		data, err := os.ReadFile(filepath.Join(backup, id+".rmdoc"))
		if err != nil {
			return manifest, err
		}
		sum := sha256.Sum256(data)
		if int64(len(data)) != entry.Size || hex.EncodeToString(sum[:]) != entry.SHA256 {
			return manifest, fmt.Errorf("native backup checksum mismatch")
		}
	}
	actual, err := deviceops.LibraryFiles("/home/root/.local/share/remarkable/xochitl")
	if err != nil {
		return manifest, err
	}
	if !reflect.DeepEqual(actual, manifest.Files) {
		return manifest, fmt.Errorf("native library changed; create a fresh stable backup")
	}
	return manifest, nil
}
func controlRequest(ctx context.Context, base, method, path string, body, result any) error {
	token, err := drivecloud.ControlToken(filepath.Join(base, "control-token"))
	if err != nil {
		return err
	}
	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://127.0.0.1:12347"+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 135 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("local cloud interface unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("local cloud request failed (HTTP %d)", response.StatusCode)
	}
	if result != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 32<<20)).Decode(result)
	}
	return nil
}
func pairLocal(ctx context.Context, base string) error {
	var status drivecloud.Status
	if err := controlRequest(ctx, base, "GET", "/v1/state", nil, &status); err != nil {
		return err
	}
	if status.Active || status.RecoveryNeeded || !status.Ready || status.Pending != 0 {
		return fmt.Errorf("migration requires idle preparation mode")
	}
	var pair struct {
		Code string `json:"code"`
	}
	if err := controlRequest(ctx, base, "POST", "/v1/pair", map[string]any{}, &pair); err != nil {
		return err
	}
	ca, err := os.ReadFile(filepath.Join(base, "private/pki/ca.crt"))
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return fmt.Errorf("local bridge CA unavailable")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp4", "127.0.0.1:443")
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(path string, body any, bearer string) (string, error) {
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		req, e := http.NewRequestWithContext(ctx, "POST", "https://local.appspot.com"+path, bytes.NewReader(data))
		if e != nil {
			return "", e
		}
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		response, e := client.Do(req)
		if e != nil {
			return "", fmt.Errorf("local pairing TLS connection failed")
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return "", fmt.Errorf("local pairing failed (HTTP %d)", response.StatusCode)
		}
		raw, e := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return string(raw), e
	}
	device, err := request("/token/json/2/device/new", map[string]string{"code": pair.Code, "deviceDesc": "desktop-windows", "deviceID": "drive-cloud-" + uuid.NewString()}, "")
	if err != nil {
		return err
	}
	userToken, err := request("/token/json/2/user/new", nil, device)
	if err != nil {
		return err
	}
	return privateJSON(filepath.Join(base, "private/local-account.json"), deviceops.LocalAccount{DeviceToken: device, UserToken: userToken})
}
