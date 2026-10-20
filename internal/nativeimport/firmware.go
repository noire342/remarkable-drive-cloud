// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Lorenzo Garrone (noire342)
// Local-only USB interface adaptation based on rM-self-serve/webinterface-onboot
// (MIT, Copyright (c) 2023 rM-self-serve). No Xochitl binary is redistributed.
package nativeimport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const originalXochitlSHA = "87b574fc7ac1a15d502e96458a94a5279a189023bd476758e4aaca9bae13575a"
const preparedXochitlSHA = "72deb4483772142ab12d08d12f1d25dbd4e7b1f449a9ead63df103f0ac612fe4"

func shaBytes(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func prepareImage(original []byte, expectedOriginal, expectedPrepared string) ([]byte, error) {
	if shaBytes(original) != expectedOriginal {
		return nil, fmt.Errorf("unsupported Xochitl image: SHA256 %s; only the validated reMarkable 2 OS 3.27.3.0 image is supported", shaBytes(original))
	}
	if bytes.Count(original, []byte("usb0")) != 1 || bytes.Count(original, []byte("usb1")) != 2 {
		return nil, fmt.Errorf("unexpected USB interface strings; original left untouched")
	}
	result := bytes.ReplaceAll(original, []byte("usb0"), []byte("usbF"))
	result = bytes.ReplaceAll(result, []byte("usb1"), []byte("usb0"))
	if shaBytes(result) != expectedPrepared {
		return nil, fmt.Errorf("prepared image hash mismatch; original left untouched")
	}
	return result, nil
}

func Prepare(source, destination string) error {
	if destination == "" {
		return fmt.Errorf("-xochitl-output is required")
	}
	sourceAbs, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	destAbs, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if sourceAbs == destAbs {
		return fmt.Errorf("source and output must differ; the original is never overwritten")
	}
	original, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	prepared, err := prepareImage(original, originalXochitlSHA, preparedXochitlSHA)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destAbs), 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(destAbs); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symlink output")
	}
	temp, err := os.CreateTemp(filepath.Dir(destAbs), ".xochitl-prepare-")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(prepared); err == nil {
		err = temp.Chmod(0755)
	}
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(temp.Name(), destAbs); err != nil {
		return err
	}
	fmt.Printf("Prepared local Xochitl copy (OS 3.27.3.0), SHA256 %s\n", preparedXochitlSHA)
	return nil
}

func checkImportAPI(base string) error {
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
	response, err := client.Get(base + "/documents/")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	io.Copy(io.Discard, io.LimitReader(response.Body, 4<<20))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("native import API HTTP %d", response.StatusCode)
	}
	return nil
}
