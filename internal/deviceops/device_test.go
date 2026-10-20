// SPDX-License-Identifier: AGPL-3.0-only
package deviceops

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalTLSIdentityAndQMLNonce(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "private", "pki")
	if err := EnsureTLS(dir); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "server.crt"))
	if err := EnsureTLS(dir); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "server.crt"))
	if string(before) != string(after) {
		t.Fatal("TLS identity replaced on update")
	}
	pair, err := tls.LoadX509KeyPair(filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key"))
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(pair.Certificate[0])
	caPEM, _ := os.ReadFile(filepath.Join(dir, "ca.crt"))
	block, _ := pem.Decode(caPEM)
	ca, _ := x509.ParseCertificate(block.Bytes)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	for _, host := range CloudHosts {
		if _, err = cert.Verify(x509.VerifyOptions{DNSName: host, Roots: pool}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = cert.Verify(x509.VerifyOptions{DNSName: "example.com", Roots: pool}); err == nil {
		t.Fatal("certificate covers unrelated hosts")
	}
	if err = PrepareUI(base, strings.Repeat("a", 43)); err != nil {
		t.Fatal(err)
	}
	nonce, _ := os.ReadFile(filepath.Join(base, "private", "ui-nonce"))
	if err = UIReady(base, "incorrect"); err == nil {
		t.Fatal("wrong candidate accepted")
	}
	if err = UIReady(base, string(nonce)); err != nil {
		t.Fatal(err)
	}
	if err = PrepareUI(base, strings.Repeat("a", 43)); err != nil {
		t.Fatal(err)
	}
	if err = UIReady(base, string(nonce)); err == nil {
		t.Fatal("previous candidate accepted after update")
	}
}

func TestMigrationEvidenceFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "validation.json")
	now := time.Now().UTC()
	dh := strings.Repeat("a", 64)
	bh := strings.Repeat("b", 64)
	if _, err := ReadValidation(path, dh, bh, now); err == nil {
		t.Fatal("missing evidence accepted")
	}
	v := Validation{Firmware: Firmware, XochitlHash: XochitlSHA256, DeviceHash: dh, BackupHash: bh, ProtocolPhysical: true, RestorePhysical: true, CloudInventory: true, BackupStable: true, DriveVerified: true, UIPhysical: true, Checked: now}
	save := func() { b, _ := json.Marshal(v); os.WriteFile(path, b, 0600) }
	save()
	if _, err := ReadValidation(path, dh, bh, now); err != nil {
		t.Fatal(err)
	}
	v.RestorePhysical = false
	save()
	if _, err := ReadValidation(path, dh, bh, now); err == nil {
		t.Fatal("simulation accepted as physical restore")
	}
	v.RestorePhysical = true
	v.Checked = now.Add(-25 * time.Hour)
	save()
	if _, err := ReadValidation(path, dh, bh, now); err == nil {
		t.Fatal("stale source backup accepted")
	}
	if _, err := ReadValidation(path, strings.Repeat("c", 64), bh, now); err == nil {
		t.Fatal("other tablet accepted")
	}
}
