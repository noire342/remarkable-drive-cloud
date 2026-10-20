// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/danjacques/gofslock/fslock"
	"github.com/ddvk/rmfakecloud/internal/storage/fs"
	"github.com/google/uuid"
)

type Engine struct {
	lock         fslock.Handle
	closeOnce    sync.Once
	status       atomic.Value
	phase        string
	mu           sync.Mutex
	dir          string
	objects      string
	remote       Remote
	state        State
	wake         chan struct{}
	notify       func()
	exporter     func(string, string) (io.ReadCloser, error)
	verifyExport func(*Document) error
	now          func() time.Time
}

func NewEngine(dir, objects, user string, remote Remote) (*Engine, error) {
	if err := os.MkdirAll(objects, 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	e := &Engine{dir: dir, objects: objects, remote: remote, wake: make(chan struct{}, 1), now: time.Now}
	lock, err := fslock.Lock(filepath.Join(dir, "engine.lock"))
	if err != nil {
		return nil, fmt.Errorf("another cloud process owns this state: %w", err)
	}
	e.lock = lock
	success := false
	defer func() {
		if !success {
			e.Close()
		}
	}()
	err = loadJSON(filepath.Join(dir, "state.json"), &e.state)
	if errors.Is(err, os.ErrNotExist) {
		e.state = State{Version: FormatVersion, Vault: uuid.NewString(), Writer: uuid.NewString(), User: user, Mappings: map[string]Mapping{}, Receipts: map[string]string{}}
		err = e.save()
	}
	if err != nil {
		return nil, fmt.Errorf("state unreadable; refusing empty-library fallback: %w", err)
	}
	if e.state.Version != FormatVersion || e.state.User != user || e.state.Vault == "" || e.state.Writer == "" || e.state.Mappings == nil || e.state.Receipts == nil {
		return nil, fmt.Errorf("invalid state; synchronization suspended")
	}
	e.status.Store(e.statusUnlocked())
	success = true
	return e, nil
}
func (e *Engine) Close() {
	e.closeOnce.Do(func() {
		if e.lock != nil {
			e.lock.Unlock()
		}
	})
}
func (e *Engine) save() error {
	e.status.Store(e.statusUnlocked())
	return saveJSON(filepath.Join(e.dir, "state.json"), &e.state)
}
func (e *Engine) objectPath(hash string) string { return filepath.Join(e.objects, hash) }
func (e *Engine) SetExporter(exporter func(string, string) (io.ReadCloser, error)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.exporter = exporter
}
func (e *Engine) SetExportVerifier(verify func(*Document) error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.verifyExport = verify
}
func (e *Engine) SetNotifier(notify func()) { e.mu.Lock(); defer e.mu.Unlock(); e.notify = notify }
func (e *Engine) Wake() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}
func (e *Engine) Status() Status {
	if e.mu.TryLock() {
		v := e.statusUnlocked()
		e.status.Store(v)
		e.mu.Unlock()
		return v
	}
	if v := e.status.Load(); v != nil {
		return v.(Status)
	}
	return Status{}
}
func (e *Engine) statusUnlocked() Status {
	snapshots, exports := 0, 0
	if e.state.Pending != nil {
		snapshots++
	}
	for id, hash := range e.state.Current.Documents {
		mapping := e.state.Mappings[id]
		if mapping.ExportHash != hash && !(mapping.Trashed && mapping.NativeHash == hash) {
			exports++
		}
	}
	return Status{Phase: e.phase, Active: e.state.Active, Linked: e.remote != nil, Ready: e.state.Initialized, Paused: e.state.Paused, Account: e.state.Account, Generation: e.state.Current.Generation, Pending: snapshots + exports, PendingSnapshots: snapshots, PendingExports: exports, LastSync: e.state.LastSync, LastError: e.state.LastError, Quota: e.state.Quota}
}
func (e *Engine) Pause(paused bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state.Paused = paused
	return e.save()
}
func (e *Engine) Bootstrap(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	err := e.bootstrap(ctx)
	if err != nil {
		e.state.LastError = err.Error()
		e.save()
	}
	return err
}
func (e *Engine) bootstrap(ctx context.Context) error {
	if e.state.Initialized {
		return e.checkLayout(ctx)
	}
	if e.remote == nil {
		return ErrNotLinked
	}
	account, quota, err := e.remote.Account(ctx)
	if err != nil {
		return err
	}
	e.state.Account = account
	e.state.Quota = quota
	folders := []struct {
		slot       *string
		name, role string
		parent     *string
	}{{&e.state.Layout.Root, "reMarkable", "root", nil}, {&e.state.Layout.Library, "Libreria", "library", &e.state.Layout.Root}, {&e.state.Layout.Inbox, "Da importare", "inbox", &e.state.Layout.Root}, {&e.state.Layout.Archive, "_Archivio", "archive", &e.state.Layout.Root}, {&e.state.Layout.Objects, "oggetti", "objects", &e.state.Layout.Archive}, {&e.state.Layout.Snapshots, "snapshot", "snapshots", &e.state.Layout.Archive}}
	for _, folder := range folders {
		if *folder.slot == "" {
			id, err := e.remote.NewID(ctx)
			if err != nil {
				return err
			}
			*folder.slot = id
			if err = e.save(); err != nil {
				return err
			}
		}
		parents := []string{}
		if folder.parent != nil {
			parents = append(parents, *folder.parent)
		}
		props := map[string]string{"vault": e.state.Vault, "role": folder.role}
		remote, err := e.remote.Get(ctx, *folder.slot)
		if errors.Is(err, ErrNotFound) {
			remote, err = e.remote.Put(ctx, File{ID: *folder.slot, Name: folder.name, MIME: FolderMIME, Parents: parents, Properties: props}, "")
		}
		if err != nil {
			return err
		}
		if remote.Trashed || remote.MIME != FolderMIME || remote.Properties["vault"] != e.state.Vault || remote.Properties["role"] != folder.role {
			return fmt.Errorf("invalid vault folder: %w", ErrSuspended)
		}
	}
	if e.state.Cursor == "" {
		e.state.Cursor, err = e.remote.StartToken(ctx)
		if err != nil {
			return err
		}
	}
	e.state.Initialized = true
	return e.save()
}
func (e *Engine) checkLayout(ctx context.Context) error {
	if e.remote == nil {
		return ErrNotLinked
	}
	if e.state.Paused {
		return ErrSuspended
	}
	for _, item := range []struct{ id, role, parent string }{{e.state.Layout.Root, "root", ""}, {e.state.Layout.Library, "library", e.state.Layout.Root}, {e.state.Layout.Inbox, "inbox", e.state.Layout.Root}, {e.state.Layout.Archive, "archive", e.state.Layout.Root}, {e.state.Layout.Objects, "objects", e.state.Layout.Archive}, {e.state.Layout.Snapshots, "snapshots", e.state.Layout.Archive}} {
		if item.id == "" {
			return ErrNotLinked
		}
		f, err := e.remote.Get(ctx, item.id)
		if err != nil {
			return fmt.Errorf("vault unavailable: %w", err)
		}
		if f.Trashed || f.MIME != FolderMIME || f.Properties["vault"] != e.state.Vault || f.Properties["role"] != item.role || (item.parent != "" && !hasParent(f, item.parent)) {
			return ErrSuspended
		}
	}
	return nil
}
func hasParent(f File, id string) bool {
	for _, p := range f.Parents {
		if p == id {
			return true
		}
	}
	return false
}
func (e *Engine) putObject(id string, reader io.Reader) error {
	if !nativeHash.MatchString(id) {
		return fmt.Errorf("invalid native object ID")
	}
	path := e.objectPath(id)
	if _, err := os.Stat(path); err == nil {
		incoming, err := os.CreateTemp(e.objects, ".incoming-")
		if err != nil {
			return err
		}
		defer os.Remove(incoming.Name())
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(incoming, h), io.LimitReader(reader, (2<<30)+1))
		incoming.Close()
		if err != nil {
			return err
		}
		if n > 2<<30 {
			return fmt.Errorf("object exceeds tablet limit")
		}
		old, err := os.Open(path)
		if err != nil {
			return err
		}
		defer old.Close()
		original := sha256.New()
		if _, err = io.Copy(original, old); err != nil {
			return err
		}
		if !bytes.Equal(original.Sum(nil), h.Sum(nil)) {
			return fmt.Errorf("native immutable object content changed")
		}
		return nil
	}
	return atomicWrite(path, 0600, func(w io.Writer) error {
		n, err := io.Copy(w, io.LimitReader(reader, (2<<30)+1))
		if err == nil && n > 2<<30 {
			return fmt.Errorf("object exceeds tablet limit")
		}
		return err
	})
}
func (e *Engine) StoreBlob(user, id string, reader io.Reader, lastGen int64) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if user != e.state.User {
		return 0, fmt.Errorf("unexpected native user")
	}
	if id != "root" {
		err := e.putObject(id, reader)
		return 1, err
	}
	b, err := io.ReadAll(io.LimitReader(reader, 65))
	if err != nil {
		return 0, err
	}
	hash := string(b)
	if !nativeHash.MatchString(hash) {
		return 0, fmt.Errorf("invalid root request")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	gen, err := e.commit(ctx, hash, lastGen)
	if err != nil {
		e.state.LastError = err.Error()
		e.save()
	}
	return gen, err
}
func (e *Engine) LoadBlob(user, id string) (io.ReadCloser, int64, int64, string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if user != e.state.User {
		return nil, 0, 0, "", fmt.Errorf("unexpected native user")
	}
	if id == "root" {
		if e.state.Current.Root == "" {
			return nil, 0, 0, "", fs.ErrorNotFound
		}
		body := []byte(e.state.Current.Root)
		return io.NopCloser(bytes.NewReader(body)), e.state.Current.Generation, int64(len(body)), crc(body), nil
	}
	if !nativeHash.MatchString(id) {
		return nil, 0, 0, "", fs.ErrorNotFound
	}
	if err := e.ensureLocal(context.Background(), id); err != nil {
		return nil, 0, 0, "", err
	}
	f, err := os.Open(e.objectPath(id))
	if err != nil {
		return nil, 0, 0, "", fs.ErrorNotFound
	}
	h := crc32.New(crc32.MakeTable(crc32.Castagnoli))
	n, err := io.Copy(h, f)
	if err != nil {
		f.Close()
		return nil, 0, 0, "", err
	}
	if _, err = f.Seek(0, 0); err != nil {
		f.Close()
		return nil, 0, 0, "", err
	}
	return f, 1, n, "crc32c=" + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}
func crc(b []byte) string {
	h := crc32.New(crc32.MakeTable(crc32.Castagnoli))
	h.Write(b)
	return "crc32c=" + base64.StdEncoding.EncodeToString(h.Sum(nil))
}
func (e *Engine) ensureLocal(ctx context.Context, hash string) error {
	if !nativeHash.MatchString(hash) {
		return fs.ErrorNotFound
	}
	if _, err := os.Stat(e.objectPath(hash)); err == nil {
		return nil
	}
	obj, ok := e.state.Current.Objects[hash]
	if !ok || e.remote == nil {
		return fs.ErrorNotFound
	}
	r, err := e.remote.Download(ctx, obj.ID)
	if err != nil {
		return err
	}
	defer r.Close()
	return atomicWrite(e.objectPath(hash), 0600, func(w io.Writer) error {
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(r, obj.Size+1))
		if err != nil {
			return err
		}
		if n != obj.Size || hex.EncodeToString(h.Sum(nil)) != obj.SHA256 {
			return fmt.Errorf("remote object checksum mismatch")
		}
		return nil
	})
}
func (e *Engine) commit(ctx context.Context, root string, base int64) (int64, error) {
	if err := e.checkLayout(ctx); err != nil {
		return 0, err
	}
	if e.state.Pending != nil {
		if err := e.finishPending(ctx); err != nil {
			return 0, err
		}
	}
	current := e.state.Current
	if current.Root == root && (base == current.Generation || base == current.Generation-1) {
		return current.Generation, nil
	}
	if current.Generation != base {
		return current.Generation, fs.ErrorWrongGeneration
	}
	graph, err := e.graph(root)
	if err != nil {
		return 0, err
	}
	id, err := e.remote.NewID(ctx)
	if err != nil {
		return 0, err
	}
	snap := Snapshot{Version: FormatVersion, Vault: e.state.Vault, Writer: e.state.Writer, ID: id, Previous: current.ID, Generation: base + 1, Root: root, Created: e.now().UTC(), Objects: map[string]Object{}, Documents: map[string]string{}, Names: map[string]string{}}
	for hash := range graph.Objects {
		if obj, ok := current.Objects[hash]; ok {
			snap.Objects[hash] = obj
			continue
		}
		id, err := e.remote.NewID(ctx)
		if err != nil {
			return 0, err
		}
		f, err := os.Open(e.objectPath(hash))
		if err != nil {
			return 0, err
		}
		sha := sha256.New()
		md := md5.New()
		size, err := io.Copy(io.MultiWriter(sha, md), f)
		f.Close()
		if err != nil {
			return 0, err
		}
		snap.Objects[hash] = Object{ID: id, SHA256: hex.EncodeToString(sha.Sum(nil)), MD5: hex.EncodeToString(md.Sum(nil)), Size: size}
	}
	for id, doc := range graph.Documents {
		snap.Documents[id] = doc.Hash
		snap.Names[id] = doc.Text("visibleName")
	}
	e.state.Pending = &Pending{Root: root, Base: base, Snapshot: snap}
	if err = e.save(); err != nil {
		return 0, err
	}
	if err = e.finishPending(ctx); err != nil {
		return 0, err
	}
	return e.state.Current.Generation, nil
}
func (e *Engine) finishPending(ctx context.Context) error {
	pending := e.state.Pending
	if pending == nil {
		return nil
	}
	if err := e.checkLayout(ctx); err != nil {
		return err
	}
	if err := e.verifyArchiveEvents(ctx); err != nil {
		return err
	}
	snap := pending.Snapshot
	keys := make([]string, 0, len(snap.Objects))
	for hash := range snap.Objects {
		keys = append(keys, hash)
	}
	sort.Strings(keys)
	for _, hash := range keys {
		obj := snap.Objects[hash]
		if previous, ok := e.state.Current.Objects[hash]; ok && previous == obj {
			continue
		}
		// Existing objects are checked remotely too: an incomplete vault must never
		// be promoted merely because an earlier local receipt existed.
		f, err := e.remote.Put(ctx, File{ID: obj.ID, Name: obj.SHA256, MIME: "application/octet-stream", Parents: []string{e.state.Layout.Objects}, Properties: map[string]string{"vault": e.state.Vault, "role": "object", "native_hash": hash, "sha256": obj.SHA256}}, e.objectPath(hash))
		if err != nil {
			return err
		}
		if f.MD5 != obj.MD5 || f.Trashed || !hasParent(f, e.state.Layout.Objects) || f.Properties["vault"] != e.state.Vault {
			return fmt.Errorf("remote object verification failed")
		}
	}
	path := filepath.Join(e.dir, "pending-snapshot.json")
	if err := saveJSON(path, snap); err != nil {
		return err
	}
	f, err := e.remote.Put(ctx, File{ID: snap.ID, Name: fmt.Sprintf("%020d-%s.json", snap.Generation, snap.ID), MIME: "application/json", Parents: []string{e.state.Layout.Snapshots}, Properties: map[string]string{"vault": e.state.Vault, "role": "snapshot", "writer": e.state.Writer, "generation": strconv.FormatInt(snap.Generation, 10)}}, path)
	if err != nil {
		return err
	}
	md, _, err := fileMD5(path)
	if err != nil {
		return err
	}
	if f.MD5 != md || f.Trashed || !hasParent(f, e.state.Layout.Snapshots) {
		return fmt.Errorf("remote snapshot verification failed")
	}
	old := e.state
	e.state.Current = snap
	e.state.Pending = nil
	e.state.LastSync = e.now().UTC()
	e.state.LastError = ""
	if err = e.save(); err != nil {
		e.state = old
		return err
	}
	e.Wake()
	return nil
}

// Run uses an ordinary timer. Linux suspend pauses execution and no wake timer
// or inhibitor is installed. Every cycle is serialized with native commits.
func (e *Engine) Run(ctx context.Context) {
	timer := time.NewTicker(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.wake:
		case <-timer.C:
		}
		status := e.Status()
		if status.Paused || (!status.Active && status.PendingExports == 0 && status.PendingSnapshots == 0) {
			continue
		}
		e.mu.Lock()
		e.phase = "exports"
		e.state.LastError = ""
		if status.Active {
			e.phase = "sync"
		}
		e.status.Store(e.statusUnlocked())
		e.mu.Unlock()
		cycle, cancel := context.WithTimeout(ctx, 4*time.Minute)
		var err error
		if status.Active {
			err = e.Cycle(cycle)
		} else {
			err = e.PublishLibrary(cycle)
		}
		cancel()
		e.mu.Lock()
		e.phase = ""
		if err != nil {
			e.state.LastError = err.Error()
		} else {
			e.state.LastError = ""
		}
		e.save()
		e.mu.Unlock()
	}
}
func (e *Engine) Cycle(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkLayout(ctx); err != nil {
		return err
	}
	if err := e.finishPending(ctx); err != nil {
		return err
	}
	if err := e.reconcileLibrary(ctx); err != nil {
		return err
	}
	if err := e.publishLibrary(ctx); err != nil {
		return err
	}
	if err := e.importInbox(ctx); err != nil {
		return err
	}
	account, quota, err := e.remote.Account(ctx)
	if err != nil {
		return err
	}
	e.state.Account = account
	e.state.Quota = quota
	e.state.LastError = ""
	return e.save()
}

// Verify only the immutable objects in our own archive. Reading the global
// changes feed makes unrelated account activity part of every native commit.
func (e *Engine) verifyArchiveEvents(ctx context.Context) error {
	if e.state.Current.ID == "" {
		return nil
	}
	expected, err := json.Marshal(e.state.Current)
	if err != nil {
		return err
	}
	sum := md5.Sum(append(expected, '\n'))
	snap, err := e.remote.Get(ctx, e.state.Current.ID)
	if err != nil {
		return err
	}
	if snap.Trashed || snap.MD5 != hex.EncodeToString(sum[:]) || !hasParent(snap, e.state.Layout.Snapshots) {
		return ErrSuspended
	}
	ids := map[string]Object{}
	for _, obj := range e.state.Current.Objects {
		ids[obj.ID] = obj
	}
	files, err := e.remote.List(ctx, "'"+e.state.Layout.Objects+"' in parents and trashed = false")
	if err != nil {
		return err
	}
	for _, file := range files {
		if obj, ok := ids[file.ID]; ok {
			if file.Trashed || file.MD5 != obj.MD5 || !hasParent(file, e.state.Layout.Objects) || file.Properties["vault"] != e.state.Vault {
				return ErrSuspended
			}
			delete(ids, file.ID)
		}
	}
	if len(ids) != 0 {
		return fmt.Errorf("archive objects missing: %w", ErrSuspended)
	}
	return nil
}

func (e *Engine) SetActive(active bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state.Active = active
	return e.save()
}
