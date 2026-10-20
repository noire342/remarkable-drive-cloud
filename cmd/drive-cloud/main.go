// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ddvk/rmfakecloud/internal/app"
	"github.com/ddvk/rmfakecloud/internal/config"
	"github.com/ddvk/rmfakecloud/internal/deviceops"
	"github.com/ddvk/rmfakecloud/internal/drivecloud"
	"github.com/ddvk/rmfakecloud/internal/model"
	"github.com/ddvk/rmfakecloud/internal/nativeexport"
	"github.com/ddvk/rmfakecloud/internal/nativeimport"
	"github.com/ddvk/rmfakecloud/internal/storage/fs"
	log "github.com/sirupsen/logrus"
)

var version = "0.1.0-dev"

const user = "tablet@drive.local"

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: drive-cloud serve|init|doctor|version [--base PATH]")
	}
	command := os.Args[1]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	base := flags.String("base", "/home/root/drive-cloud", "private installation path")
	root := flags.String("root", "/", "root used by read-only doctor")
	backup := flags.String("backup", "", "native backup folder")
	input := flags.String("input", "/usr/bin/xochitl", "supported native image")
	output := flags.String("output", "", "prepared local image path")
	listenHost := flags.String("listen-host", "127.0.0.1", "loopback address for isolated protocol validation")
	readOnly := flags.Bool("native-read-only", false, "reject every native write during protocol validation")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if command == "version" {
		fmt.Println(version)
		return nil
	}
	if command == "doctor" {
		return json.NewEncoder(os.Stdout).Encode(deviceops.Inspect(*root))
	}
	if command == "prepare-native" {
		return nativeimport.Prepare(*input, *output)
	}
	if command == "prepare-drive" || command == "seed-backup" || command == "verify-drive" || command == "export-backup" || command == "publish-library" {
		return prepareRemote(command, *base, *backup, *output)
	}
	if command == "verify-backup" || command == "verify-root" {
		engine, err := drivecloud.NewEngine(filepath.Join(*base, "state"), filepath.Join(*base, "objects"), "backup-verifier", nil)
		if err != nil {
			return err
		}
		defer engine.Close()
		hash := *input
		var count int
		if command == "verify-root" {
			count, err = engine.VerifyNativeRoot(hash)
		} else {
			hash, count, err = engine.ReadNativeArchives(*backup)
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"documents": count, "native_root": hash, "archive_graph_verified": true, "physical_restore_verified": false})
	}
	if command != "serve" && command != "init" && command != "prepare-ui" {
		return fmt.Errorf("unknown command")
	}
	if ip := net.ParseIP(*listenHost); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("the document bridge must bind exclusively to loopback")
	}
	log.SetLevel(log.WarnLevel)
	if err := os.MkdirAll(*base, 0700); err != nil {
		return err
	}
	token, err := drivecloud.ControlToken(filepath.Join(*base, "control-token"))
	if err != nil {
		return err
	}
	secret, err := drivecloud.ControlToken(filepath.Join(*base, "identity-key"))
	if err != nil {
		return err
	}
	if command == "prepare-ui" {
		return deviceops.PrepareUI(*base, token)
	}
	if err = deviceops.EnsureTLS(filepath.Join(*base, "private", "pki")); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(*base, "ui"), 0700); err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(*base, "ui", "Config.js")); os.IsNotExist(err) {
		if err = deviceops.WriteQMLConfig(filepath.Join(*base, "ui", "Config.js"), token, ""); err != nil {
			return err
		}
	}
	if command == "init" {
		fmt.Println("Private local identity prepared; native cloud unchanged.")
		return nil
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cfg := config.FromEnv()
	cfg.DataDir = filepath.Join(*base, "data")
	cfg.Port = "443"
	cfg.BindHost = *listenHost
	cfg.TabletOnly = true
	cfg.HashSchemaVersion = "4"
	cfg.JWTSecretKey = []byte(secret)
	cfg.JWTRandom = false
	cfg.StorageURL = "https://local.appspot.com"
	cfg.CloudHost = "local.appspot.com"
	cfg.MQTTPort = "8883"
	cfg.ICEServers = nil
	cfg.Certificate, err = tls.LoadX509KeyPair(filepath.Join(*base, "private", "pki", "server.crt"), filepath.Join(*base, "private", "pki", "server.key"))
	if err != nil {
		return err
	}
	storage := fs.NewStorage(cfg)
	if _, err = storage.GetUser(user); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		password, err := model.GenPassword()
		if err != nil {
			return err
		}
		profile, err := model.NewUser(user, password)
		if err != nil {
			return err
		}
		if err = storage.RegisterUser(profile); err != nil {
			return err
		}
	}
	var remote drivecloud.Remote
	client, err := drivecloud.AuthorizedClient(ctx, filepath.Join(*base, "credentials.json"))
	if err == nil {
		remote = drivecloud.NewDrive(client, filepath.Join(*base, "private", "uploads"))
	} else if !errors.Is(err, drivecloud.ErrNotLinked) {
		return err
	}
	engine, err := drivecloud.NewEngine(filepath.Join(*base, "state"), filepath.Join(cfg.DataDir, "users", user, "sync"), user, remote)
	if err != nil {
		return err
	}
	defer engine.Close()
	engine.SetExporter((nativeexport.Client{}).Export)
	engine.SetExportVerifier(drivecloud.LibraryRevisionVerifier("/home/root/.local/share/remarkable/xochitl"))
	backend := engine.NativeBackend(*readOnly)
	bridge := app.NewAppWithBackend(cfg, backend)
	engine.SetNotifier(func() { bridge.NotifySync(user) })
	control := drivecloud.Control{Engine: engine, Token: token, UIReady: func(nonce string) error { return deviceops.UIReady(*base, nonce) }, Code: func() (string, error) {
		if !engine.Status().Ready {
			return "", drivecloud.ErrNotLinked
		}
		return bridge.PairCode(user)
	}}
	listener, err := net.Listen("tcp4", net.JoinHostPort(*listenHost, "12347"))
	if err != nil {
		return err
	}
	server := &http.Server{Handler: control.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	go server.Serve(listener)
	defer server.Close()
	go bridge.Start()
	defer bridge.Stop()
	if remote != nil {
		prepareCtx, stop := context.WithTimeout(ctx, 2*time.Minute)
		err = engine.Bootstrap(prepareCtx)
		stop()
		if err != nil {
			log.Warn("Drive archive unavailable; native cloud not switched")
		}
	}
	if _, err := os.Stat(filepath.Join(*base, "private", "active")); err == nil && !*readOnly {
		engine.SetActive(true)
	} else {
		engine.SetActive(false)
	}
	if remote != nil && !*readOnly {
		go engine.Run(ctx)
		engine.Wake()
	}
	<-ctx.Done()
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "drive-cloud:", err)
		os.Exit(1)
	}
}
