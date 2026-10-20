// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestBundleRejectsTraversalLinksAndUnexpectedFiles(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind byte
		ok   bool
	}{
		{"tablet-ui/Page.qml", tar.TypeReg, true},
		{"licenses/dependency/LICENSE", tar.TypeReg, true},
		{"../escape", tar.TypeReg, false}, {"/etc/hosts", tar.TypeReg, false},
		{"device/link", tar.TypeSymlink, false}, {"credentials.json", tar.TypeReg, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "bundle.tar.gz")
			f, err := os.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			z := gzip.NewWriter(f)
			w := tar.NewWriter(z)
			for _, entry := range []string{"PAYLOAD.sha256", "bin/drive-cloud", "device/install-device.sh"} {
				w.WriteHeader(&tar.Header{Name: entry, Mode: 0600, Typeflag: tar.TypeReg, Size: 1})
				w.Write([]byte("x"))
			}
			header := &tar.Header{Name: tc.name, Mode: 0600, Typeflag: tc.kind, Linkname: "/etc/shadow"}
			if tc.kind == tar.TypeReg {
				header.Size = 1
			}
			w.WriteHeader(header)
			if header.Size > 0 {
				w.Write([]byte("x"))
			}
			w.Close()
			z.Close()
			f.Close()
			data, _ := os.ReadFile(name)
			h := sha256.Sum256(data)
			sum := hex.EncodeToString(h[:])
			err = validateBundle(name, sum)
			if (err == nil) != tc.ok {
				t.Fatalf("valid=%v err=%v", tc.ok, err)
			}
			if validateBundle(name, "00") == nil {
				t.Fatal("unpinned bundle accepted")
			}
		})
	}
}

func TestSSHAddressCannotInjectCommands(t *testing.T) {
	for _, host := range []string{"", "-oProxyCommand=bad", "1.2.3.4;bad", "$(bad)", "host name", "host\ncommand"} {
		if _, err := target(host); err == nil {
			t.Fatalf("unsafe SSH target accepted %q", host)
		}
	}
	for _, host := range []string{"192.168.1.10", "remarkable.local", "2001:db8::1"} {
		if _, err := target(host); err != nil {
			t.Fatal(err)
		}
	}
}
