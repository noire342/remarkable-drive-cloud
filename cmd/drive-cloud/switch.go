// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ddvk/rmfakecloud/internal/deviceops"
	"github.com/ddvk/rmfakecloud/internal/drivecloud"
)

func service(args ...string) error {
	output, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("system service operation failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}
func isActive(name string) bool {
	return exec.Command("systemctl", "is-active", "--quiet", name).Run() == nil
}

func recoverSwitch(base string, force bool) error {
	store := deviceops.SwitchStore{Root: "/", Base: base}
	if _, err := store.Read(); os.IsNotExist(err) {
		return nil
	}
	boot, err := deviceops.BootIdentity("/")
	if err != nil {
		return err
	}
	if force {
		service("stop", "xochitl", "rm-sync", "remarkable-drive-cloud")
	}
	recovered, err := store.Recover(boot, force)
	if err != nil {
		if fallback := store.EmergencyOffline(); fallback != nil {
			return fmt.Errorf("cloud recovery requires manual repair; native documents were preserved")
		}
		recovered = true
	}
	if recovered {
		guard := "/run/systemd/system/xochitl.service.d/zzzz-drive-cloud-switch.conf"
		if data, e := os.ReadFile(guard); e == nil && strings.HasPrefix(string(data), "# remarkable-drive-cloud migration guard\n") {
			os.Remove(guard)
		}
		if err = service("daemon-reload"); err != nil {
			return err
		}
		if force {
			service("reset-failed", "xochitl", "rm-sync", "remarkable-drive-cloud")
			if err = service("start", "remarkable-drive-cloud", "rm-sync", "xochitl"); err != nil {
				return err
			}
			service("start", "remarkable-quickshare")
		}
	}
	return nil
}

func switchCommand(command, base, backup, evidence string, force bool) (bool, error) {
	commands := map[string]bool{"check-switch": true, "prepare-recovery": true, "pair-local": true, "begin-switch": true, "backup-switch": true, "merge-backup": true, "settle-switch": true, "resume-switch": true, "install-switch": true, "start-switch": true, "observe-native": true, "wait-observer": true, "verify-switch": true, "finish-switch": true, "recover-switch": true, "guard-switch": true, "wait-local": true}
	if !commands[command] {
		return false, nil
	}
	if base != "/home/root/drive-cloud" {
		return true, fmt.Errorf("migration requires the supported tablet installation")
	}
	store := deviceops.SwitchStore{Root: "/", Base: base}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	switch command {
	case "check-switch":
		_, err := checkedBackup(base, backup, evidence)
		if err != nil {
			return true, err
		}
		dir, err := os.MkdirTemp(filepath.Join(base, "private"), "verify-source-")
		if err != nil {
			return true, err
		}
		engine, err := drivecloud.NewEngine(filepath.Join(dir, "state"), filepath.Join(dir, "objects"), "backup-verifier", nil)
		if err != nil {
			return true, err
		}
		defer engine.Close()
		_, count, err := engine.ReadNativeArchives(backup)
		if err != nil {
			return true, err
		}
		return true, json.NewEncoder(os.Stdout).Encode(map[string]any{"verified_backup_items": count, "native_cloud_changed": false})
	case "prepare-recovery":
		return true, store.InstallRecoveryUnit()
	case "pair-local":
		return true, pairLocal(ctx, base)
	case "resume-switch":
		if isActive("xochitl") || isActive("rm-sync") || isActive("remarkable-drive-cloud") || !deviceops.Inspect("/").FirmwareMatch {
			return true, fmt.Errorf("resume requires the supported firmware and stopped writers")
		}
		var proof struct {
			Generation int64     `json:"generation"`
			Root       string    `json:"root"`
			Checked    time.Time `json:"checked"`
		}
		data, err := os.ReadFile(filepath.Join(base, "private/recovery-settled.json"))
		if err != nil || json.Unmarshal(data, &proof) != nil || proof.Checked.After(time.Now()) || time.Since(proof.Checked) > time.Hour {
			return true, fmt.Errorf("complete and verify the pending Drive snapshot before resuming")
		}
		var state drivecloud.State
		data, err = os.ReadFile(filepath.Join(base, "state/state.json"))
		if err != nil || json.Unmarshal(data, &state) != nil || state.Pending != nil || state.Current.Root != proof.Root || state.Current.Generation != proof.Generation {
			return true, fmt.Errorf("verified recovery snapshot no longer matches")
		}
		boot, err := deviceops.BootIdentity("/")
		if err != nil {
			return true, err
		}
		return true, store.Resume(boot)
	case "settle-switch":
		if isActive("remarkable-drive-cloud") || !deviceops.Inspect("/").FirmwareMatch {
			return true, fmt.Errorf("pending recovery requires the supported firmware and stopped bridge")
		}
		if _, err := os.Stat(filepath.Join(base, "private/recovery-needed")); err != nil {
			return true, fmt.Errorf("offline recovery state required")
		}
		client, err := drivecloud.AuthorizedClient(ctx, filepath.Join(base, "credentials.json"))
		if err != nil {
			return true, err
		}
		engine, err := drivecloud.NewEngine(filepath.Join(base, "state"), filepath.Join(base, "data/users", user, "sync"), user, drivecloud.NewDrive(client, filepath.Join(base, "private/uploads")))
		if err != nil {
			return true, err
		}
		defer engine.Close()
		var state drivecloud.State
		data, err := os.ReadFile(filepath.Join(base, "state/state.json"))
		if err != nil || json.Unmarshal(data, &state) != nil {
			return true, fmt.Errorf("pending native state unavailable")
		}
		if state.Pending != nil {
			if err = engine.SeedVerifiedBackup(ctx, state.Pending.Root, state.Pending.Base); err != nil {
				return true, err
			}
		}
		count, err := engine.VerifyRemote(ctx)
		if err != nil {
			return true, err
		}
		if err = privateJSON(filepath.Join(base, "private/recovery-settled.json"), map[string]any{"generation": engine.Status().Generation, "root": func() string {
			if state.Pending != nil {
				return state.Pending.Root
			}
			return state.Current.Root
		}(), "checked": time.Now().UTC()}); err != nil {
			return true, err
		}
		return true, json.NewEncoder(os.Stdout).Encode(map[string]any{"verified_remote_documents": count, "generation": engine.Status().Generation, "native_cloud_offline": true})
	case "begin-switch":
		if isActive("xochitl") || isActive("rm-sync") || isActive("remarkable-quickshare") {
			return true, fmt.Errorf("native writers must be stopped before the transaction")
		}
		manifest, err := checkedBackup(base, backup, evidence)
		if err != nil {
			return true, err
		}
		data, err := os.ReadFile(filepath.Join(base, "private/local-account.json"))
		if err != nil {
			return true, err
		}
		var account deviceops.LocalAccount
		if err = json.Unmarshal(data, &account); err != nil {
			return true, err
		}
		boot, err := deviceops.BootIdentity("/")
		if err != nil {
			return true, err
		}
		journal, err := store.Prepare(account, manifest.Files, boot, time.Now())
		if err != nil {
			return true, err
		}
		return true, json.NewEncoder(os.Stdout).Encode(map[string]string{"phase": journal.Phase, "transaction": journal.Transaction})
	case "backup-switch":
		return true, store.BackupSwitchData()
	case "merge-backup":
		if isActive("xochitl") || isActive("rm-sync") || isActive("remarkable-drive-cloud") {
			return true, fmt.Errorf("native and cloud writers must be stopped before merging")
		}
		if _, err := checkedBackup(base, backup, evidence); err != nil {
			return true, err
		}
		journal, err := store.Read()
		if err != nil || journal.Phase != "prepared" {
			return true, fmt.Errorf("a prepared migration is required")
		}
		client, err := drivecloud.AuthorizedClient(ctx, filepath.Join(base, "credentials.json"))
		if err != nil {
			return true, err
		}
		remote := drivecloud.NewDrive(client, filepath.Join(base, "private/uploads"))
		engine, err := drivecloud.NewEngine(filepath.Join(base, "state"), filepath.Join(base, "data/users", user, "sync"), user, remote)
		if err != nil {
			return true, err
		}
		defer engine.Close()
		var prior drivecloud.State
		stateData, err := os.ReadFile(filepath.Join(base, "state/state.json"))
		if err != nil {
			return true, err
		}
		if err = json.Unmarshal(stateData, &prior); err != nil {
			return true, err
		}
		if prior.Pending != nil || prior.Current.Generation < 1 {
			return true, fmt.Errorf("complete the prepared Drive archive before activation")
		}
		root, count, err := engine.ReadNativeArchives(backup)
		if err != nil {
			return true, err
		}
		if count != len(prior.Current.Documents) {
			return true, fmt.Errorf("native and archived item counts differ; reconcile first")
		}
		// The private source verifier below also checks the UUID set, preventing
		// equal counts with a missing/replaced native document.
		for id := range prior.Current.Documents {
			if _, err = os.Stat(filepath.Join(backup, id+".rmdoc")); os.IsNotExist(err) {
				if _, err = os.Stat(filepath.Join(backup, id+".metadata")); err != nil {
					return true, fmt.Errorf("archive contains an item absent from the verified source")
				}
			} else if err != nil {
				return true, err
			}
		}
		if err = engine.SeedVerifiedBackup(ctx, root, prior.Current.Generation); err != nil {
			return true, err
		}
		verified, err := engine.VerifyRemote(ctx)
		if err != nil {
			return true, err
		}
		if err = privateJSON(filepath.Join(journal.Transaction, "source-drive-verification.json"), map[string]any{"documents": verified, "generation": engine.Status().Generation, "checked": time.Now().UTC()}); err != nil {
			return true, err
		}
		return true, json.NewEncoder(os.Stdout).Encode(map[string]any{"verified_remote_documents": verified, "generation": engine.Status().Generation})
	case "install-switch":
		journal, err := store.Read()
		if err != nil {
			return true, err
		}
		if _, err = os.Stat(filepath.Join(journal.Transaction, "native-backup.complete")); err != nil {
			return true, fmt.Errorf("verified native backup missing")
		}
		if _, err = os.Stat(filepath.Join(journal.Transaction, "source-drive-verification.json")); err != nil {
			return true, fmt.Errorf("verified Drive snapshot missing")
		}
		return true, store.Install()
	case "start-switch":
		return true, store.MarkStarting()
	case "observe-native":
		return true, observeNative(ctx, store)
	case "wait-observer":
		journal, err := store.Read()
		if err != nil {
			return true, err
		}
		for attempt := 0; attempt < 100; attempt++ {
			if _, err = os.Stat(filepath.Join(journal.Transaction, "native-observer.ready")); err == nil {
				return true, nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		return true, fmt.Errorf("native synchronization observer not ready")
	case "wait-local":
		deadline := time.NewTimer(75 * time.Second)
		defer deadline.Stop()
		for {
			var status drivecloud.Status
			if err := controlRequest(ctx, base, "GET", "/v1/state", nil, &status); err == nil && status.Active && status.Ready && !status.RecoveryNeeded {
				return true, nil
			}
			select {
			case <-deadline.C:
				return true, fmt.Errorf("local bridge not ready")
			case <-time.After(500 * time.Millisecond):
			}
		}
	case "verify-switch":
		return true, verifySwitch(ctx, base, store)
	case "finish-switch":
		journal, err := store.Read()
		if err != nil {
			return true, err
		}
		for _, name := range []string{"native-batch-verified.json", "native-payload-verified.json", "active-drive-verification.json"} {
			if _, err = os.Stat(filepath.Join(journal.Transaction, name)); err != nil {
				return true, fmt.Errorf("physical migration verification incomplete")
			}
		}
		return true, store.Finish()
	case "recover-switch":
		return true, recoverSwitch(base, force)
	case "guard-switch":
		time.Sleep(12 * time.Minute)
		if _, err := store.Read(); os.IsNotExist(err) {
			return true, nil
		}
		return true, recoverSwitch(base, true)
	}
	return true, fmt.Errorf("unsupported migration operation")
}

func observeNative(ctx context.Context, store deviceops.SwitchStore) error {
	journal, err := store.Read()
	if err != nil {
		return err
	}
	if journal.Phase != "starting" {
		return fmt.Errorf("candidate clients were not started")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	// Match interfaces instead of resolving the service's current owner: the
	// native synchronizer is deliberately stopped until this observer is ready.
	cmd := exec.CommandContext(ctx, "busctl", "--match=interface='no.remarkable.sync.Synchronizer'", "--match=interface='org.freedesktop.DBus.Properties',path='/Synchronizer'", "monitor")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	status, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	go func() {
		scanner := bufio.NewScanner(status)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), "Monitoring bus message stream.") {
				privateJSON(filepath.Join(journal.Transaction, "native-observer.ready"), map[string]any{"subscribed": true})
			}
		}
	}()
	// Keep the private monitor pipe flowing even after a quiet final signal.
	// These property reads do not update the e-ink display or wake the tablet.
	var retryNeeded atomic.Bool
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		attempt := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				output, err := exec.CommandContext(ctx, "busctl", "get-property", "no.remarkable.sync", "/Synchronizer", "no.remarkable.sync.Synchronizer", "isSyncing").Output()
				attempt++
				if err == nil && retryNeeded.Load() && attempt%15 == 0 && strings.TrimSpace(string(output)) == "b false" {
					var state drivecloud.Status
					if controlRequest(ctx, store.Base, "GET", "/v1/state", nil, &state) == nil && state.Active && state.Ready && state.PendingSnapshots == 0 {
						// A native root request can time out at 30 seconds while its
						// durable Drive commit continues. Retry only after that commit.
						ids := []string{}
						for name := range journal.SourceFiles {
							if strings.HasSuffix(name, ".metadata") && !strings.Contains(name, "/") {
								ids = append(ids, strings.TrimSuffix(name, ".metadata"))
							}
						}
						sort.Strings(ids)
						args := []string{"call", "no.remarkable.sync", "/Synchronizer", "no.remarkable.sync.Synchronizer", "execute", "s(asasasb)", "/home/root/.local/share/remarkable/xochitl", fmt.Sprint(len(ids))}
						args = append(args, ids...)
						args = append(args, "0", "0", "false")
						exec.CommandContext(ctx, "busctl", args...).Run()
					}
				}
			}
		}
	}()
	defer func() { cancel(); cmd.Wait() }()
	if err := successfulNativeBatch(pipe, func() { retryNeeded.Store(true) }); err != nil {
		return err
	}
	return privateJSON(filepath.Join(journal.Transaction, "native-batch-verified.json"), map[string]any{"result": 0, "checked": time.Now().UTC()})
}

func successfulNativeBatch(reader io.Reader, failed func()) error {
	scanner := bufio.NewScanner(reader)
	inBatch, success, hasCode := false, false, false
	code := regexp.MustCompile(`BYTE ([0-9]+);`)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Type=") {
			inBatch, success, hasCode = false, false, false
		}
		if strings.Contains(line, "Member=batchFinished") {
			inBatch = true
		}
		if !inBatch {
			continue
		}
		if match := code.FindStringSubmatch(line); len(match) == 2 {
			success = match[1] == "0"
			hasCode = true
		}
		if strings.Contains(line, "BOOLEAN true;") && hasCode {
			if success {
				return nil
			}
			if failed != nil {
				failed()
			}
		}
	}
	return fmt.Errorf("a successful final native synchronization batch was not observed")
}

func verifySwitch(ctx context.Context, base string, store deviceops.SwitchStore) error {
	journal, err := store.Read()
	if err != nil {
		return err
	}
	if journal.Phase != "starting" {
		return fmt.Errorf("candidate not running")
	}
	if !isActive("xochitl") || !isActive("rm-sync") {
		return fmt.Errorf("native interface or synchronizer is not running")
	}
	actual, err := deviceops.LibraryFiles("/home/root/.local/share/remarkable/xochitl")
	if err != nil {
		return err
	}
	for name, hash := range journal.SourceFiles {
		if strings.HasSuffix(name, ".metadata") {
			before, err := os.ReadFile(filepath.Join(journal.Transaction, "native-backup/library", filepath.FromSlash(name)))
			if err != nil {
				return err
			}
			after, err := os.ReadFile(filepath.Join("/home/root/.local/share/remarkable/xochitl", filepath.FromSlash(name)))
			if err != nil {
				return err
			}
			var original, current map[string]any
			if json.Unmarshal(before, &original) != nil || json.Unmarshal(after, &current) != nil {
				return fmt.Errorf("native metadata could not be verified")
			}
			bookkeeping := map[string]bool{"synced": true, "modified": true, "metadatamodified": true, "version": true, "lastModified": true, "lastOpened": true, "lastOpenedPage": true}
			for key, value := range original {
				if !bookkeeping[key] && !reflect.DeepEqual(value, current[key]) {
					return fmt.Errorf("native metadata changed during the account transition")
				}
			}
			continue
		}
		if strings.HasSuffix(name, ".dirty") {
			continue
		}
		if actual[name] != hash {
			return fmt.Errorf("native document payload changed during the account transition")
		}
	}
	if err = privateJSON(filepath.Join(journal.Transaction, "native-payload-verified.json"), map[string]any{"payloads_unchanged": true, "checked": time.Now().UTC()}); err != nil {
		return err
	}
	var result struct {
		Documents int               `json:"verified_remote_documents"`
		Status    drivecloud.Status `json:"status"`
	}
	if err = controlRequest(ctx, base, "POST", "/v1/verify", map[string]any{}, &result); err != nil {
		return err
	}
	count := 0
	for name := range journal.SourceFiles {
		if strings.HasSuffix(name, ".metadata") && !strings.Contains(name, "/") {
			count++
		}
	}
	if result.Documents < count || !result.Status.Active || result.Status.PendingSnapshots > 0 {
		return fmt.Errorf("remote migration verification incomplete")
	}
	return privateJSON(filepath.Join(journal.Transaction, "active-drive-verification.json"), map[string]any{"documents": result.Documents, "generation": result.Status.Generation, "checked": time.Now().UTC()})
}
