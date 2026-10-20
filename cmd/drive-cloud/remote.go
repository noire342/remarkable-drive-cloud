// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ddvk/rmfakecloud/internal/drivecloud"
	"github.com/ddvk/rmfakecloud/internal/nativeexport"
	"os"
	"path/filepath"
	"time"
)

func prepareRemote(command, base, backup, output string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	client, err := drivecloud.AuthorizedClient(ctx, filepath.Join(base, "credentials.json"))
	if err != nil {
		return err
	}
	remote := drivecloud.NewDrive(client, filepath.Join(base, "private", "uploads"))
	e, err := drivecloud.NewEngine(filepath.Join(base, "state"), filepath.Join(base, "data", "users", user, "sync"), user, remote)
	if err != nil {
		return err
	}
	defer e.Close()
	if err = e.Bootstrap(ctx); err != nil {
		return err
	}
	if command == "seed-backup" {
		if e.Status().Generation != 0 {
			return fmt.Errorf("seed requires an unused archive; refusing to replace current work")
		}
		root, _, err := e.ReadNativeArchives(backup)
		if err != nil {
			return err
		}
		if err = e.SeedVerifiedBackup(ctx, root, 0); err != nil {
			return err
		}
	}
	if command == "publish-library" {
		e.SetExporter((nativeexport.Client{}).Export)
		e.SetExportVerifier(drivecloud.LibraryRevisionVerifier("/home/root/.local/share/remarkable/xochitl"))
		if err = e.PublishLibrary(ctx); err != nil {
			return err
		}
	}
	count := 0
	if command != "prepare-drive" {
		count, err = e.VerifyRemote(ctx)
		if err != nil {
			return err
		}
	}
	if command == "export-backup" {
		count, err = e.ExportNativeBackup(ctx, output)
		if err != nil {
			return err
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"status": e.Status(), "verified_remote_documents": count, "native_cloud_switched": false})
}
