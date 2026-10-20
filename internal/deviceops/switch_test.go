// SPDX-License-Identifier: AGPL-3.0-only
package deviceops

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupSwitch(t *testing.T) (SwitchStore, LocalAccount, map[string]string, []byte) {
	t.Helper()
	store := SwitchStore{Root: t.TempDir(), Base: "/home/root/drive-cloud"}
	original := []byte("[General]\nUserToken=@ByteArray(old.user.token)\ndevicetoken=@ByteArray(old.device.token)\nKeep=untouched\n[Other]\nUserToken=also-untouched\n")
	files := map[string][]byte{nativeConfig: original, "/etc/hosts": []byte("127.0.0.1 localhost\n192.0.2.1 unrelated.example my.remarkable.com\n"), "/etc/nsswitch.conf": []byte("passwd: files\nhosts: resolve [!UNAVAIL=return] files dns\n"), "/etc/ssl/certs/ca-certificates.crt": []byte("original trust"), store.Base + "/private/pki/ca.crt": []byte("local CA"), "/home/root/.local/share/remarkable/xochitl/doc.metadata": []byte("native metadata"), "/home/root/.local/share/remarkable/xochitl/doc/page.rm": []byte("precious ink"), "/home/root/.local/share/remarkable/xochitl/.tree": []byte("old native tree")}
	for path, data := range files {
		if err := durableFile(store.path(path), 0600, data); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(store.base(), "backups"), 0700); err != nil {
		t.Fatal(err)
	}
	source, err := LibraryFiles(store.path("/home/root/.local/share/remarkable/xochitl"))
	if err != nil {
		t.Fatal(err)
	}
	return store, LocalAccount{DeviceToken: "new.device.token", UserToken: "new.user.token"}, source, original
}

func TestResumeKeepsLocalAccountAndNewInkAcrossAnotherFailure(t *testing.T) {
	s, account, source, _ := setupSwitch(t)
	if _, err := s.Prepare(account, source, "boot-a", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Install(); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkStarting(); err != nil {
		t.Fatal(err)
	}
	ink := s.path("/home/root/.local/share/remarkable/xochitl/doc/page.rm")
	if err := os.WriteFile(ink, []byte("new work"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recover("boot-a", true); err != nil {
		t.Fatal(err)
	}
	if err := durableJSON(filepath.Join(s.base(), "private/local-account.json"), account); err != nil {
		t.Fatal(err)
	}
	if err := s.Resume("boot-b"); err != nil {
		t.Fatal(err)
	}
	j, err := s.Read()
	if err != nil || j.Phase != "starting" || j.BootID != "boot-b" {
		t.Fatalf("resume: %+v %v", j, err)
	}
	if _, err := s.Recover("boot-c", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(ink)
	config, _ := os.ReadFile(s.path(nativeConfig))
	hosts, _ := os.ReadFile(filepath.Join(s.base(), "private/routes/hosts"))
	if string(data) != "new work" || !strings.Contains(string(config), account.DeviceToken) || !strings.Contains(string(hosts), "127.0.0.2 local.appspot.com") {
		t.Fatal("retry recovery lost work, account or offline isolation")
	}
}

func TestSwitchCrashBeforeClientsStartRestoresOnlyConfiguration(t *testing.T) {
	s, account, source, original := setupSwitch(t)
	journal, err := s.Prepare(account, source, "boot-before", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Exercise recovery after every possible partial installation boundary.
	for _, file := range journal.Files[:3] {
		data, _ := os.ReadFile(file.Candidate)
		if err = durableFile(s.path(file.Path), os.FileMode(file.Mode), data); err != nil {
			t.Fatal(err)
		}
	}
	if recovered, err := s.Recover("boot-after", false); err != nil || !recovered {
		t.Fatalf("recovery=%v err=%v", recovered, err)
	}
	actual, _ := os.ReadFile(s.path(nativeConfig))
	if !bytes.Equal(actual, original) {
		t.Fatal("native account not restored")
	}
	if _, err = os.Stat(s.path(s.targets()[1])); !os.IsNotExist(err) {
		t.Fatal("candidate routing retained")
	}
	if _, err = os.Stat(filepath.Join(s.base(), "private/active")); !os.IsNotExist(err) {
		t.Fatal("candidate remains active")
	}
	native, err := LibraryFiles(s.path("/home/root/.local/share/remarkable/xochitl"))
	if err != nil || native["doc/page.rm"] != source["doc/page.rm"] {
		t.Fatal("recovery changed ink")
	}
	if recovered, err := s.Recover("boot-after", true); err != nil || recovered {
		t.Fatal("recovery is not idempotent")
	}
}

func TestSwitchCrashAfterClientsStartPreservesNewWorkOffline(t *testing.T) {
	s, account, source, _ := setupSwitch(t)
	if _, err := s.Prepare(account, source, "boot-a", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Install(); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkStarting(); err != nil {
		t.Fatal(err)
	}
	ink := s.path("/home/root/.local/share/remarkable/xochitl/doc/page.rm")
	if err := os.WriteFile(ink, []byte("new handwritten work"), 0600); err != nil {
		t.Fatal(err)
	}
	if recovered, err := s.Recover("boot-b", false); err != nil || !recovered {
		t.Fatal(recovered, err)
	}
	data, _ := os.ReadFile(ink)
	if string(data) != "new handwritten work" {
		t.Fatal("rollback replaced work")
	}
	conf, _ := os.ReadFile(s.path(nativeConfig))
	if !bytes.Contains(conf, []byte(account.UserToken)) {
		t.Fatal("reconnected the old account after possible edits")
	}
	hosts, _ := os.ReadFile(filepath.Join(s.base(), "private/routes/hosts"))
	if !bytes.Contains(hosts, []byte("127.0.0.2 local.appspot.com")) {
		t.Fatal("old cloud was not isolated")
	}
	if _, err := os.Stat(filepath.Join(s.base(), "private/recovery-needed")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.base(), "private/active")); !os.IsNotExist(err) {
		t.Fatal("automatic processing still active")
	}
	if _, err := s.Prepare(account, source, "boot-b", time.Now()); err == nil {
		t.Fatal("unreconciled activation accepted")
	}
}

func TestSwitchFailsClosedOnChangedSourceOrCorruptBackups(t *testing.T) {
	s, account, source, _ := setupSwitch(t)
	source["doc/page.rm"] = "wrong"
	if _, err := s.Prepare(account, source, "boot", time.Now()); err == nil {
		t.Fatal("stale backup accepted")
	}
	source, _ = LibraryFiles(s.path("/home/root/.local/share/remarkable/xochitl"))
	journal, err := s.Prepare(account, source, "boot", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(journal.Files[0].Before, []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Recover("newboot", false); err == nil {
		t.Fatal("corrupt account backup restored")
	}
}

func TestSwitchProtectsOtherExtensionsAndRejectsJournalPathInjection(t *testing.T) {
	s, account, source, _ := setupSwitch(t)
	foreign := s.path(s.targets()[1])
	if err := durableFile(foreign, 0644, []byte("# other extension\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prepare(account, source, "boot", time.Now()); err == nil {
		t.Fatal("foreign extension overwritten")
	}
	os.Remove(foreign)
	journal, err := s.Prepare(account, source, "boot", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	journal.Files[0].Path = "/unrelated-file"
	data, _ := json.Marshal(journal)
	os.WriteFile(s.pending(), data, 0600)
	if err = s.Install(); err == nil {
		t.Fatal("journal path injection accepted")
	}
}

func TestSwitchFinishRequiresStartedClientsAndRetainsBackups(t *testing.T) {
	s, account, source, _ := setupSwitch(t)
	journal, err := s.Prepare(account, source, "boot", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(); err == nil {
		t.Fatal("unstarted candidate marked verified")
	}
	if recovered, err := s.Recover("boot", false); err != nil || recovered {
		t.Fatal("recovery interrupted current transaction")
	}
	if err = s.Install(); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkStarting(); err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(journal.Files[0].Before); err != nil {
		t.Fatal("backup removed on success")
	}
	if _, err = os.Stat(s.pending()); !os.IsNotExist(err) {
		t.Fatal("pending recovery retained")
	}
	conf, _ := os.ReadFile(s.path(nativeConfig))
	if !strings.Contains(string(conf), "Keep=untouched") || !strings.Contains(string(conf), "[Other]\nUserToken=also-untouched") {
		t.Fatal("unrelated native settings changed")
	}
	globalHosts, _ := os.ReadFile(s.path("/etc/hosts"))
	if !strings.Contains(string(globalHosts), "192.0.2.1 unrelated.example my.remarkable.com") {
		t.Fatal("global DNS modified")
	}
}

func TestEmergencyOfflinePreservesLibraryAndUnrelatedGlobalNetwork(t *testing.T) {
	s, _, source, original := setupSwitch(t)
	if err := s.EmergencyOffline(); err != nil {
		t.Fatal(err)
	}
	conf, _ := os.ReadFile(s.path(nativeConfig))
	if !bytes.Equal(conf, original) {
		t.Fatal("emergency recovery replaced credentials")
	}
	actual, _ := LibraryFiles(s.path("/home/root/.local/share/remarkable/xochitl"))
	if len(actual) != len(source) || actual["doc/page.rm"] != source["doc/page.rm"] {
		t.Fatal("emergency recovery changed documents")
	}
	hosts, _ := os.ReadFile(s.path("/etc/hosts"))
	if !bytes.Contains(hosts, []byte("192.0.2.1 unrelated.example")) {
		t.Fatal("global network configuration changed")
	}
	if err := s.EmergencyOffline(); err != nil {
		t.Fatal("emergency recovery is not repeatable", err)
	}
}

func TestNativeBackupContainsPrivateStateAndCannotOverwritePriorCopies(t *testing.T) {
	s, account, source, _ := setupSwitch(t)
	journal, err := s.Prepare(account, source, "boot", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BackupSwitchData(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(journal.Transaction, "native-backup/library/.tree"))
	if err != nil || string(data) != "old native tree" {
		t.Fatal("private native state was not saved")
	}
	if err = s.BackupSwitchData(); err == nil {
		t.Fatal("existing raw backup overwritten")
	}
}
