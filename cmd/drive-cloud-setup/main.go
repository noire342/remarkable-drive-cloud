// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/ddvk/rmfakecloud/internal/drivecloud"
)

func target(host string) (string, error) {
	if net.ParseIP(host) != nil {
		return "root@" + host, nil
	}
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`).MatchString(host) {
		return "", fmt.Errorf("invalid tablet address")
	}
	return "root@" + host, nil
}
func ssh(ctx context.Context, host, command string, input io.Reader) error {
	address, err := target(host)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "ssh", "-o", "StrictHostKeyChecking=ask", "-o", "ConnectTimeout=10", address, command)
	cmd.Stdin = input
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
func validateBundle(name, expected string) error {
	if !regexp.MustCompile(`^[0-9a-fA-F]{64}$`).MatchString(expected) {
		return fmt.Errorf("a pinned SHA-256 is required")
	}
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expected) {
		return fmt.Errorf("bundle checksum mismatch")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return err
	}
	z, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer z.Close()
	reader := tar.NewReader(z)
	total := int64(0)
	seen := map[string]bool{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name == "" || path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") || seen[name] {
			return fmt.Errorf("unsafe archive entry")
		}
		seen[name] = true
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return fmt.Errorf("archive links and special files forbidden")
		}
		if !(name == "PAYLOAD.sha256" || name == "LICENSE" || name == "THIRD_PARTY_NOTICES.md" || name == "bin/drive-cloud" || strings.HasPrefix(name, "device/") || strings.HasPrefix(name, "tablet-ui/") || strings.HasPrefix(name, "dependencies/") || strings.HasPrefix(name, "licenses/")) {
			return fmt.Errorf("unexpected bundle entry: %s", name)
		}
		if header.Size < 0 || header.Size > 128<<20 {
			return fmt.Errorf("archive member too large")
		}
		total += header.Size
		if total > 256<<20 {
			return fmt.Errorf("archive too large")
		}
	}
	if !seen["device/install-device.sh"] || !seen["bin/drive-cloud"] || !seen["PAYLOAD.sha256"] {
		return fmt.Errorf("incomplete tablet bundle")
	}
	return nil
}
func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: drive-cloud-setup authorize|install|uninstall [options]")
	}
	cmd := os.Args[1]
	flags := flag.NewFlagSet(cmd, flag.ContinueOnError)
	host := flags.String("host", "", "tablet address")
	clientPath := flags.String("client", "", "Google Desktop client JSON")
	output := flags.String("output", "", "optional private local authorization file")
	bundle := flags.String("bundle", "", "verified tablet tar.gz")
	sum := flags.String("sha256", "", "pinned bundle SHA-256")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	switch cmd {
	case "authorize":
		if *host == "" && *output == "" {
			return fmt.Errorf("provide --host or --output")
		}
		if *host != "" {
			if _, err := target(*host); err != nil {
				return err
			}
		}
		credentials, err := drivecloud.ReadClient(*clientPath)
		if err != nil {
			return err
		}
		auth, err := drivecloud.AuthorizeDesktop(ctx, credentials, func(u string) { fmt.Println("Apri questo collegamento nel browser:"); fmt.Println(u) })
		if err != nil {
			return err
		}
		if *output != "" {
			if err = drivecloud.SaveAuthorization(*output, auth); err != nil {
				return err
			}
		}
		if *host != "" {
			data, err := json.Marshal(auth)
			if err != nil {
				return err
			}
			if err = ssh(ctx, *host, "test -d /home/root/drive-cloud/private && umask 077 && cat > /home/root/drive-cloud/credentials.json.new && mv /home/root/drive-cloud/credentials.json.new /home/root/drive-cloud/credentials.json && systemctl restart remarkable-drive-cloud.service", bytes.NewReader(data)); err != nil {
				return err
			}
		}
		fmt.Println("Accesso Google configurato. La migrazione del vecchio cloud resta separata.")
		return nil
	case "install":
		if _, err := target(*host); err != nil {
			return err
		}
		if err := validateBundle(*bundle, *sum); err != nil {
			return err
		}
		f, err := os.Open(*bundle)
		if err != nil {
			return err
		}
		defer f.Close()
		script := "set -eu; umask 077; stage=$(mktemp -d /home/root/drive-cloud-stage.XXXXXX); trap 'rm -f \"$stage/payload.tar.gz\"' EXIT; cat > \"$stage/payload.tar.gz\"; printf '%s  %s\\n' '" + strings.ToLower(*sum) + "' \"$stage/payload.tar.gz\" | sha256sum -c -; mkdir \"$stage/payload\"; tar -xzf \"$stage/payload.tar.gz\" -C \"$stage/payload\"; sh \"$stage/payload/device/install-device.sh\""
		return ssh(ctx, *host, script, f)
	case "uninstall":
		return ssh(ctx, *host, "sh /home/root/drive-cloud/device/uninstall.sh", nil)
	default:
		return fmt.Errorf("unknown command")
	}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "drive-cloud-setup:", err)
		os.Exit(1)
	}
}
