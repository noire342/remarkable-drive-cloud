// SPDX-License-Identifier: AGPL-3.0-only
package deviceops

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const Firmware = "3.27.3.0"
const XochitlSHA256 = "87b574fc7ac1a15d502e96458a94a5279a189023bd476758e4aaca9bae13575a"

var CloudHosts = []string{"local.appspot.com", "my.remarkable.com", "service-manager-production-dot-remarkable-production.appspot.com", "internal.cloud.remarkable.com", "eu.tectonic.remarkable.com", "eu.internal.tctn.cloud.remarkable.com", "webapp-prod.cloud.remarkable.engineering"}

type Doctor struct {
	Firmware          string `json:"supported_firmware"`
	FirmwareMatch     bool   `json:"firmware_match"`
	Xovi              bool   `json:"xovi"`
	ResourceRebuilder bool   `json:"resource_rebuilder"`
	NativeImport      bool   `json:"native_import_prepared"`
	ActivationAllowed bool   `json:"activation_allowed"`
	Reason            string `json:"reason"`
}

func Inspect(root string) Doctor {
	result := Doctor{Firmware: Firmware, Reason: "Migration requires verified protocol, cloud inventory, backup and restore evidence"}
	path := func(p string) string { return filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(p, "/"))) }
	f, err := os.Open(path("/usr/bin/xochitl"))
	if err == nil {
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		result.FirmwareMatch = err == nil && hex.EncodeToString(h.Sum(nil)) == XochitlSHA256
	}
	exists := func(p string) bool { f, err := os.Stat(path(p)); return err == nil && !f.IsDir() }
	result.Xovi = exists("/home/root/xovi/xovi.so")
	result.ResourceRebuilder = exists("/home/root/xovi/extensions.d/qt-resource-rebuilder.so")
	result.NativeImport = exists("/home/root/quickshare/bin/xochitl-local-import")
	return result
}
func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path+".new", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(path + ".new")
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(path+".new", path)
}
func EnsureTLS(dir string) error {
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")
	if _, err := os.Stat(certPath); err == nil {
		_, err = tls.LoadX509KeyPair(certPath, keyPath)
		return err
	}
	for _, name := range []string{"ca.crt", "ca.key", "server.key"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return fmt.Errorf("partial TLS identity; inspect before repair")
		}
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial := func() *big.Int { n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128)); return n }
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "reMarkable Drive local bridge"}, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	server := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "local.appspot.com"}, DNSNames: CloudHosts, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(2, 0, 0), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, server, ca, &serverKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	caPK, _ := x509.MarshalECPrivateKey(caKey)
	serverPK, _ := x509.MarshalECPrivateKey(serverKey)
	for _, file := range []struct {
		name, kind string
		data       []byte
	}{{"ca.key", "EC PRIVATE KEY", caPK}, {"ca.crt", "CERTIFICATE", caDER}, {"server.key", "EC PRIVATE KEY", serverPK}, {"server.crt", "CERTIFICATE", der}} {
		if err = writePrivate(filepath.Join(dir, file.name), pem.EncodeToMemory(&pem.Block{Type: file.kind, Bytes: file.data})); err != nil {
			return err
		}
	}
	return nil
}
func WriteQMLConfig(path, token, nonce string) error {
	t, _ := json.Marshal(token)
	n, _ := json.Marshal(nonce)
	body := fmt.Sprintf(".pragma library\nvar endpoint = \"http://127.0.0.1:12347\";\nvar token = %s;\nvar nonce = %s;\n", t, n)
	return writePrivate(path, []byte(body))
}

func PrepareUI(base, token string) error {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	value := hex.EncodeToString(nonce)
	if err := writePrivate(filepath.Join(base, "private", "ui-nonce"), []byte(value)); err != nil {
		return err
	}
	return WriteQMLConfig(filepath.Join(base, "ui", "Config.js"), token, value)
}
func UIReady(base, nonce string) error {
	expected, err := os.ReadFile(filepath.Join(base, "private", "ui-nonce"))
	if err != nil || nonce == "" || nonce != string(expected) {
		return fmt.Errorf("unexpected UI candidate")
	}
	return writePrivate(filepath.Join(base, "private", "ui-ready"), []byte(nonce))
}
