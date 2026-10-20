// SPDX-License-Identifier: AGPL-3.0-only
package deviceops

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const routingOwner = "# remarkable-drive-cloud owned routing\n"
const nativeConfig = "/home/root/.config/remarkable/xochitl.conf"

type LocalAccount struct {
	DeviceToken string `json:"device_token"`
	UserToken   string `json:"user_token"`
}

type SwitchFile struct {
	Path          string `json:"path"`
	Before        string `json:"before,omitempty"`
	Candidate     string `json:"candidate"`
	BeforeHash    string `json:"before_sha256,omitempty"`
	CandidateHash string `json:"candidate_sha256"`
	Mode          uint32 `json:"mode"`
	BeforeMode    uint32 `json:"before_mode"`
	Absent        bool   `json:"absent"`
}

// SwitchJournal records configuration changes, never replacement library data.
// Once candidate clients can run, recovery leaves the old cloud disconnected.
type SwitchJournal struct {
	Version     int               `json:"version"`
	Transaction string            `json:"transaction"`
	Phase       string            `json:"phase"`
	BootID      string            `json:"boot_id"`
	Created     time.Time         `json:"created"`
	Files       []SwitchFile      `json:"files"`
	SourceFiles map[string]string `json:"source_files"`
}

type SwitchStore struct {
	Root string
	Base string
}

func DeviceIdentityHash(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "etc/machine-id"))
	if err != nil {
		return "", err
	}
	if len(bytes.TrimSpace(data)) < 16 {
		return "", fmt.Errorf("device identity unavailable")
	}
	return hashBytes(data), nil
}
func BootIdentity(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "proc/sys/kernel/random/boot_id"))
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if !regexp.MustCompile(`^[a-f0-9-]{36}$`).MatchString(value) {
		return "", fmt.Errorf("boot identity unavailable")
	}
	return value, nil
}

func (s SwitchStore) InstallRecoveryUnit() error {
	path := s.path("/etc/systemd/system/remarkable-drive-cloud-recover.service")
	const owner = "# remarkable-drive-cloud owned recovery\n"
	if old, err := os.ReadFile(path); err == nil && !bytes.HasPrefix(old, []byte(owner)) {
		return fmt.Errorf("foreign recovery unit already exists")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	body := owner + "[Unit]\nDescription=Recover an interrupted Drive cloud migration\nAfter=home.mount\nBefore=xochitl.service rm-sync.service\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=" + s.Base + "/bin/drive-cloud recover-switch --base " + s.Base + "\n[Install]\nWantedBy=multi-user.target\n"
	return durableFile(path, 0644, []byte(body))
}

// EmergencyOffline preserves native data when an interrupted journal is damaged.
// Isolation is installed before the GUI starts; no old library is restored.
func (s SwitchStore) EmergencyOffline() error {
	hosts, err := os.ReadFile(s.path("/etc/hosts"))
	if err != nil {
		return err
	}
	hosts, err = RoutedHosts(hosts, "127.0.0.2")
	if err != nil {
		return err
	}
	nss, err := os.ReadFile(s.path("/etc/nsswitch.conf"))
	if err != nil {
		return err
	}
	re := regexp.MustCompile(`(?m)^hosts:.*$`)
	if len(re.FindAll(nss, -1)) != 1 {
		return fmt.Errorf("NSS recovery unavailable")
	}
	nss = re.ReplaceAll(nss, []byte("hosts: files dns"))
	ca, err := os.ReadFile(s.path("/etc/ssl/certs/ca-certificates.crt"))
	if err != nil {
		return err
	}
	for name, data := range map[string][]byte{"hosts": hosts, "nsswitch.conf": nss, "ca-bundle.crt": ca} {
		if err = durableFile(filepath.Join(s.base(), "private/routes", name), 0600, data); err != nil {
			return err
		}
	}
	for _, path := range s.targets()[1:4] {
		if err = durableFile(s.path(path), 0644, routingDrop(s.Base)); err != nil {
			return err
		}
	}
	if err = durableFile(filepath.Join(s.base(), "private/recovery-needed"), 0600, []byte("Interrupted migration requires reconciliation. Documents preserved.\n")); err != nil {
		return err
	}
	if err = os.Remove(filepath.Join(s.base(), "private/active")); err != nil && !os.IsNotExist(err) {
		return err
	}
	if _, err = os.Lstat(s.pending()); err == nil {
		destination := filepath.Join(s.base(), "private", "switch.interrupted-"+time.Now().UTC().Format("20060102T150405.000000000Z")+".json")
		if err = os.Rename(s.pending(), destination); err != nil {
			return err
		}
	}
	return nil
}

// BackupSwitchData requires stopped writers and never replaces a backup.
func (s SwitchStore) BackupSwitchData() error {
	journal, err := s.Read()
	if err != nil {
		return err
	}
	if journal.Phase != "prepared" {
		return fmt.Errorf("backup requires a prepared migration")
	}
	for _, pair := range []struct{ source, name string }{{"/home/root/.local/share/remarkable/xochitl", "library"}, {"/home/root/.cache/remarkable/xochitl", "cache"}, {"/home/root/.local/share/remarkable/user-auth", "user-auth"}} {
		source := s.path(pair.source)
		if _, err = os.Stat(source); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		dest := filepath.Join(journal.Transaction, "native-backup", pair.name)
		if err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			relative, e := filepath.Rel(source, path)
			if e != nil {
				return e
			}
			target := filepath.Join(dest, relative)
			if entry.IsDir() {
				return os.MkdirAll(target, 0700)
			}
			info, e := entry.Info()
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("unsupported native backup file")
			}
			if _, e = os.Lstat(target); e == nil {
				return fmt.Errorf("native backup already exists")
			} else if !os.IsNotExist(e) {
				return e
			}
			data, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			if e = durableFile(target, 0600, data); e != nil {
				return e
			}
			copy, e := os.ReadFile(target)
			if e != nil {
				return e
			}
			if !bytes.Equal(data, copy) {
				return fmt.Errorf("native backup verification failed")
			}
			return os.Chtimes(target, info.ModTime(), info.ModTime())
		}); err != nil {
			return err
		}
	}
	return durableFile(filepath.Join(journal.Transaction, "native-backup.complete"), 0600, []byte("Native files copied and verified with writers stopped.\n"))
}

func hashBytes(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func (s SwitchStore) path(path string) string {
	return filepath.Join(s.Root, filepath.FromSlash(strings.TrimPrefix(path, "/")))
}
func (s SwitchStore) base() string    { return s.path(s.Base) }
func (s SwitchStore) pending() string { return filepath.Join(s.base(), "private/switch.pending.json") }

func durableFile(path string, mode os.FileMode, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".switch-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		directory, e := os.Open(filepath.Dir(path))
		if e != nil {
			return e
		}
		defer directory.Close()
		return directory.Sync()
	}
	return nil
}
func durableJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return durableFile(path, 0600, append(data, '\n'))
}

// LibraryFiles includes every persistent non-hidden native file. Generated
// thumbnails and private synchronizer state are backed up separately.
func LibraryFiles(library string) (map[string]string, error) {
	result := map[string]string{}
	err := filepath.WalkDir(library, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == library {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") || strings.HasSuffix(entry.Name(), ".thumbnails") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported native library entry")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(library, path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = hashBytes(data)
		return nil
	})
	return result, err
}

func AccountConfig(original []byte, account LocalAccount) ([]byte, error) {
	jwt := regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)
	if !jwt.MatchString(account.DeviceToken) || !jwt.MatchString(account.UserToken) {
		return nil, fmt.Errorf("invalid local account tokens")
	}
	lines := strings.Split(string(original), "\n")
	section := ""
	seen := map[string]int{}
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = trimmed
			continue
		}
		if section != "[General]" {
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if key == "UserToken" {
			seen[key]++
			lines[index] = key + "=@ByteArray(" + account.UserToken + ")"
		}
		if key == "devicetoken" {
			seen[key]++
			lines[index] = key + "=@ByteArray(" + account.DeviceToken + ")"
		}
	}
	if seen["UserToken"] != 1 || seen["devicetoken"] != 1 {
		return nil, fmt.Errorf("native account layout is not recognized")
	}
	return []byte(strings.Join(lines, "\n")), nil
}

func RoutedHosts(original []byte, address string) ([]byte, error) {
	if address != "127.0.0.1" && address != "127.0.0.2" {
		return nil, fmt.Errorf("cloud routing must use loopback")
	}
	known := map[string]bool{}
	for _, host := range CloudHosts {
		known[host] = true
	}
	var result []string
	for _, line := range strings.Split(strings.TrimSuffix(string(original), "\n"), "\n") {
		content, comment, _ := strings.Cut(line, "#")
		fields := strings.Fields(content)
		if len(fields) < 2 {
			result = append(result, line)
			continue
		}
		kept := []string{fields[0]}
		for _, alias := range fields[1:] {
			if !known[alias] {
				kept = append(kept, alias)
			}
		}
		if len(kept) == len(fields) {
			result = append(result, line)
		} else if len(kept) > 1 {
			next := strings.Join(kept, " ")
			if comment != "" {
				next += " #" + comment
			}
			result = append(result, next)
		}
	}
	result = append(result, address+" "+strings.Join(CloudHosts, " "))
	return []byte(strings.Join(result, "\n") + "\n"), nil
}

func routingDrop(base string) []byte {
	return []byte(routingOwner + "[Unit]\nWants=remarkable-drive-cloud.service\nRequires=remarkable-drive-cloud-recover.service\nAfter=remarkable-drive-cloud.service remarkable-drive-cloud-recover.service\n[Service]\nBindReadOnlyPaths=" + base + "/private/routes/hosts:/etc/hosts " + base + "/private/routes/nsswitch.conf:/etc/nsswitch.conf " + base + "/private/routes/ca-bundle.crt:/etc/ssl/certs/ca-certificates.crt\n")
}
func (s SwitchStore) targets() []string {
	return []string{nativeConfig, "/etc/systemd/system/xochitl.service.d/80-drive-cloud-routing.conf", "/home/root/xovi/services/xochitl.service/80-drive-cloud-routing.conf", "/etc/systemd/system/rm-sync.service.d/80-drive-cloud-routing.conf", s.Base + "/private/active"}
}

// Prepare requires stopped native writers and a stable, externally verified
// source map. Callers must finish Drive verification before Install.
func (s SwitchStore) Prepare(account LocalAccount, source map[string]string, boot string, now time.Time) (SwitchJournal, error) {
	var journal SwitchJournal
	if s.Base != "/home/root/drive-cloud" || boot == "" || len(source) == 0 {
		return journal, fmt.Errorf("invalid switch context")
	}
	for _, name := range []string{"private/active", "private/recovery-needed"} {
		if _, err := os.Lstat(filepath.Join(s.base(), name)); err == nil {
			return journal, fmt.Errorf("reconcile the current cloud before another activation")
		} else if !os.IsNotExist(err) {
			return journal, err
		}
	}
	if _, err := os.Lstat(s.pending()); err == nil {
		return journal, fmt.Errorf("another migration needs recovery")
	} else if !os.IsNotExist(err) {
		return journal, err
	}
	actual, err := LibraryFiles(s.path("/home/root/.local/share/remarkable/xochitl"))
	if err != nil {
		return journal, err
	}
	if !reflect.DeepEqual(actual, source) {
		return journal, fmt.Errorf("native library changed since the verified backup")
	}
	original, err := os.ReadFile(s.path(nativeConfig))
	if err != nil {
		return journal, err
	}
	candidate, err := AccountConfig(original, account)
	if err != nil {
		return journal, err
	}
	hosts, err := os.ReadFile(s.path("/etc/hosts"))
	if err != nil {
		return journal, err
	}
	routed, err := RoutedHosts(hosts, "127.0.0.1")
	if err != nil {
		return journal, err
	}
	nss, err := os.ReadFile(s.path("/etc/nsswitch.conf"))
	if err != nil {
		return journal, err
	}
	hostsLine := regexp.MustCompile(`(?m)^hosts:.*$`)
	if len(hostsLine.FindAll(nss, -1)) != 1 {
		return journal, fmt.Errorf("unrecognized NSS configuration")
	}
	nss = hostsLine.ReplaceAll(nss, []byte("hosts: files dns"))
	ca, err := os.ReadFile(filepath.Join(s.base(), "private/pki/ca.crt"))
	if err != nil {
		return journal, err
	}
	bundle, err := os.ReadFile(s.path("/etc/ssl/certs/ca-certificates.crt"))
	if err != nil {
		return journal, err
	}
	routes := filepath.Join(s.base(), "private/routes")
	for name, data := range map[string][]byte{"hosts": routed, "hosts.original": hosts, "nsswitch.conf": nss, "ca-bundle.crt": append(append(bundle, '\n'), ca...)} {
		if err = durableFile(filepath.Join(routes, name), 0600, data); err != nil {
			return journal, err
		}
	}
	transaction := filepath.Join(s.base(), "backups", "switch-"+now.UTC().Format("20060102T150405.000000000Z"))
	if err = os.Mkdir(transaction, 0700); err != nil {
		return journal, err
	}
	journal = SwitchJournal{Version: 1, Transaction: transaction, Phase: "prepared", BootID: boot, Created: now.UTC(), SourceFiles: source}
	drop := routingDrop(s.Base)
	bodies := [][]byte{candidate, drop, drop, drop, []byte(transaction + "\n")}
	for index, target := range s.targets() {
		entry := SwitchFile{Path: target, Mode: 0600, Candidate: filepath.Join(transaction, fmt.Sprintf("%d.candidate", index)), CandidateHash: hashBytes(bodies[index])}
		if index > 0 && index < 4 {
			entry.Mode = 0644
		}
		path := s.path(target)
		info, e := os.Lstat(path)
		if os.IsNotExist(e) {
			entry.Absent = true
		} else if e != nil {
			return journal, e
		} else {
			if !info.Mode().IsRegular() {
				return journal, fmt.Errorf("refusing a non-regular switch target")
			}
			data, e := os.ReadFile(path)
			if e != nil {
				return journal, e
			}
			if index > 0 && index < 4 && !bytes.HasPrefix(data, []byte(routingOwner)) {
				return journal, fmt.Errorf("foreign routing drop-in already exists")
			}
			entry.Before = filepath.Join(transaction, fmt.Sprintf("%d.before", index))
			entry.BeforeHash = hashBytes(data)
			entry.BeforeMode = uint32(info.Mode().Perm())
			if e = durableFile(entry.Before, os.FileMode(entry.BeforeMode), data); e != nil {
				return journal, e
			}
		}
		if e = durableFile(entry.Candidate, os.FileMode(entry.Mode), bodies[index]); e != nil {
			return journal, e
		}
		journal.Files = append(journal.Files, entry)
	}
	if err = durableJSON(filepath.Join(transaction, "journal.json"), journal); err != nil {
		return journal, err
	}
	return journal, durableJSON(s.pending(), journal)
}

func (s SwitchStore) Read() (SwitchJournal, error) {
	return s.readJournal(s.pending())
}
func (s SwitchStore) readJournal(path string) (SwitchJournal, error) {
	var journal SwitchJournal
	b, err := os.ReadFile(path)
	if err != nil {
		return journal, err
	}
	if err = json.Unmarshal(b, &journal); err != nil {
		return journal, err
	}
	parent := filepath.Join(s.base(), "backups")
	if journal.Version != 1 || filepath.Dir(journal.Transaction) != parent || !strings.HasPrefix(filepath.Base(journal.Transaction), "switch-") || len(journal.Files) != len(s.targets()) {
		return journal, fmt.Errorf("invalid migration journal")
	}
	for index, file := range journal.Files {
		if file.Path != s.targets()[index] || filepath.Dir(file.Candidate) != journal.Transaction || (!file.Absent && filepath.Dir(file.Before) != journal.Transaction) {
			return journal, fmt.Errorf("untrusted migration paths")
		}
	}
	return journal, nil
}

// Resume reuses a recovered local account and its original transaction. Since
// native clients already ran, any subsequent failure must stay offline too.
// Native writers and the bridge must be stopped by the caller.
func (s SwitchStore) Resume(boot string) error {
	if _, err := os.Lstat(s.pending()); !os.IsNotExist(err) {
		return fmt.Errorf("another migration needs recovery")
	}
	marker, err := os.ReadFile(filepath.Join(s.base(), "private/recovery-needed"))
	if err != nil {
		return err
	}
	transaction := strings.TrimSpace(string(marker))
	if filepath.Dir(transaction) != filepath.Join(s.base(), "backups") || !strings.HasPrefix(filepath.Base(transaction), "switch-") {
		return fmt.Errorf("manual reconciliation required for damaged recovery metadata")
	}
	journal, err := s.readJournal(filepath.Join(transaction, "journal.json"))
	if err != nil || journal.Transaction != transaction || journal.Phase != "recovered" || boot == "" {
		return fmt.Errorf("recovered transaction is not resumable")
	}
	data, err := os.ReadFile(filepath.Join(s.base(), "private/local-account.json"))
	if err != nil {
		return err
	}
	var account LocalAccount
	if json.Unmarshal(data, &account) != nil {
		return fmt.Errorf("local account unavailable")
	}
	current, err := os.ReadFile(s.path(nativeConfig))
	if err != nil || !strings.Contains(string(current), "devicetoken=@ByteArray("+account.DeviceToken+")") {
		return fmt.Errorf("native account changed since interruption")
	}
	for _, path := range s.targets()[1:4] {
		data, err := os.ReadFile(s.path(path))
		if err != nil || !bytes.Equal(data, routingDrop(s.Base)) {
			return fmt.Errorf("native routing changed since interruption")
		}
	}
	original, err := os.ReadFile(filepath.Join(s.base(), "private/routes/hosts.original"))
	if err != nil {
		return err
	}
	hosts, err := RoutedHosts(original, "127.0.0.1")
	if err != nil {
		return err
	}
	journal.Phase, journal.BootID = "starting", boot
	if err = durableJSON(s.pending(), journal); err != nil {
		return err
	}
	for _, name := range []string{"native-observer.ready", "native-batch-verified.json", "native-payload-verified.json", "active-drive-verification.json"} {
		if err = os.Remove(filepath.Join(transaction, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err = durableFile(filepath.Join(s.base(), "private/routes/hosts"), 0600, hosts); err != nil {
		return err
	}
	if err = durableFile(filepath.Join(s.base(), "private/active"), 0600, []byte(transaction+"\n")); err != nil {
		return err
	}
	return os.Remove(filepath.Join(s.base(), "private/recovery-needed"))
}
func (s SwitchStore) Install() error {
	journal, err := s.Read()
	if err != nil {
		return err
	}
	if journal.Phase != "prepared" {
		return fmt.Errorf("migration is not prepared")
	}
	for _, file := range journal.Files {
		data, e := os.ReadFile(file.Candidate)
		if e != nil {
			return e
		}
		if hashBytes(data) != file.CandidateHash {
			return fmt.Errorf("candidate checksum mismatch")
		}
		if e = durableFile(s.path(file.Path), os.FileMode(file.Mode), data); e != nil {
			return e
		}
	}
	return nil
}
func (s SwitchStore) MarkStarting() error {
	journal, err := s.Read()
	if err != nil {
		return err
	}
	if journal.Phase != "prepared" {
		return fmt.Errorf("unexpected migration phase")
	}
	journal.Phase = "starting"
	return durableJSON(s.pending(), journal)
}
func (s SwitchStore) Finish() error {
	journal, err := s.Read()
	if err != nil {
		return err
	}
	if journal.Phase != "starting" {
		return fmt.Errorf("candidate was not started")
	}
	journal.Phase = "verified"
	if err = durableJSON(filepath.Join(journal.Transaction, "journal.json"), journal); err != nil {
		return err
	}
	return os.Remove(s.pending())
}

// Recover returns true when a recovery was performed. A candidate that could
// have produced new edits is kept offline; reconnecting the old account is a
// separate reconciliation decision. No library file is restored or deleted.
func (s SwitchStore) Recover(boot string, force bool) (bool, error) {
	journal, err := s.Read()
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !force && journal.BootID == boot {
		return false, nil
	}
	if journal.Phase == "prepared" {
		// Verify every backup before changing any target.
		for _, file := range journal.Files {
			if !file.Absent {
				data, e := os.ReadFile(file.Before)
				if e != nil {
					return false, e
				}
				if hashBytes(data) != file.BeforeHash {
					return false, fmt.Errorf("migration backup checksum mismatch")
				}
			}
		}
		for _, file := range journal.Files {
			if file.Absent {
				if e := os.Remove(s.path(file.Path)); e != nil && !os.IsNotExist(e) {
					return false, e
				}
			} else {
				data, _ := os.ReadFile(file.Before)
				if e := durableFile(s.path(file.Path), os.FileMode(file.BeforeMode), data); e != nil {
					return false, e
				}
			}
		}
	} else if journal.Phase == "starting" {
		hosts, e := os.ReadFile(filepath.Join(s.base(), "private/routes/hosts.original"))
		if e != nil {
			return false, e
		}
		hosts, e = RoutedHosts(hosts, "127.0.0.2")
		if e != nil {
			return false, e
		}
		if e = durableFile(filepath.Join(s.base(), "private/routes/hosts"), 0600, hosts); e != nil {
			return false, e
		}
		if e = durableFile(filepath.Join(s.base(), "private/recovery-needed"), 0600, []byte(journal.Transaction+"\n")); e != nil {
			return false, e
		}
		if e = os.Remove(filepath.Join(s.base(), "private/active")); e != nil && !os.IsNotExist(e) {
			return false, e
		}
	} else {
		return false, fmt.Errorf("unknown interrupted migration phase")
	}
	journal.Phase = "recovered"
	if err = durableJSON(filepath.Join(journal.Transaction, "journal.json"), journal); err != nil {
		return false, err
	}
	if err = os.Remove(s.pending()); err != nil {
		return false, err
	}
	return true, nil
}
