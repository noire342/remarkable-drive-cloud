// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/storage/fs"
	"github.com/ddvk/rmfakecloud/internal/storage/models"
	"github.com/google/uuid"
)

type fakeRemote struct {
	mu             sync.Mutex
	files          map[string]File
	data           map[string][]byte
	events         []Change
	sequence       int
	failRole       string
	failAfterStore bool
	offline        bool
	puts           int
}

func newFake() *fakeRemote { return &fakeRemote{files: map[string]File{}, data: map[string][]byte{}} }
func (f *fakeRemote) Get(_ context.Context, id string) (File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.offline {
		return File{}, errors.New("offline")
	}
	v, ok := f.files[id]
	if !ok {
		return File{}, ErrNotFound
	}
	return v, nil
}
func (f *fakeRemote) List(_ context.Context, q string) ([]File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.offline {
		return nil, errors.New("offline")
	}
	parent := strings.Split(q, "'")[1]
	var files []File
	for _, v := range f.files {
		if hasParent(v, parent) && !v.Trashed {
			files = append(files, v)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ID < files[j].ID })
	return files, nil
}
func (f *fakeRemote) Download(_ context.Context, id string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.data[id]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), data...))), nil
}
func (f *fakeRemote) NewID(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sequence++
	return fmt.Sprintf("file-%06d", f.sequence), nil
}
func (f *fakeRemote) event(v File) {
	copy := v
	f.events = append(f.events, Change{FileID: v.ID, File: &copy})
}
func (f *fakeRemote) Put(_ context.Context, v File, path string) (File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.offline {
		return File{}, errors.New("offline")
	}
	if f.failRole != "" && f.failRole == v.Properties["role"] && !f.failAfterStore {
		return File{}, errors.New("injected upload failure")
	}
	if old, ok := f.files[v.ID]; ok {
		if v.MIME == FolderMIME {
			return old, nil
		}
		if v.Properties["role"] == "object" || v.Properties["role"] == "snapshot" {
			b, _ := os.ReadFile(path)
			sum := md5.Sum(b)
			if old.MD5 != hex.EncodeToString(sum[:]) {
				return File{}, ErrSuspended
			}
			return old, nil
		}
	}
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return File{}, err
		}
		f.data[v.ID] = b
		sum := md5.Sum(b)
		v.MD5 = hex.EncodeToString(sum[:])
		v.Size = strconv.Itoa(len(b))
	}
	v.Version = strconv.Itoa(len(f.events) + 1)
	f.files[v.ID] = v
	f.event(v)
	f.puts++
	if f.failRole != "" && f.failRole == v.Properties["role"] && f.failAfterStore {
		return File{}, errors.New("lost acknowledgement")
	}
	return v, nil
}
func (f *fakeRemote) Patch(_ context.Context, id string, fields map[string]any, add, remove string) (File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.files[id]
	if !ok {
		return File{}, ErrNotFound
	}
	if name, ok := fields["name"].(string); ok {
		v.Name = name
	}
	if trashed, ok := fields["trashed"].(bool); ok {
		v.Trashed = trashed
	}
	if props, ok := fields["appProperties"].(map[string]string); ok {
		v.Properties = props
	}
	if add != "" {
		v.Parents = []string{add}
	}
	v.Version = strconv.Itoa(len(f.events) + 1)
	f.files[id] = v
	f.event(v)
	return v, nil
}
func (f *fakeRemote) StartToken(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strconv.Itoa(len(f.events)), nil
}
func (f *fakeRemote) Changes(_ context.Context, cursor string) (Changes, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.offline {
		return Changes{}, errors.New("offline")
	}
	n, err := strconv.Atoi(cursor)
	if err != nil || n > len(f.events) {
		return Changes{}, errors.New("invalid cursor")
	}
	return Changes{Changes: append([]Change{}, f.events[n:]...), Start: strconv.Itoa(len(f.events))}, nil
}
func (f *fakeRemote) Account(context.Context) (string, Quota, error) {
	if f.offline {
		return "", Quota{}, errors.New("offline")
	}
	return "test@example.invalid", Quota{Limit: "1000000000", Usage: "0"}, nil
}
func (f *fakeRemote) edit(id string, edit func(*File, []byte) []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.files[id]
	data := edit(&v, append([]byte{}, f.data[id]...))
	if data != nil {
		sum := md5.Sum(data)
		v.MD5 = hex.EncodeToString(sum[:])
		f.data[id] = data
	}
	v.Version = strconv.Itoa(len(f.events) + 1)
	f.files[id] = v
	f.event(v)
}

func setupEngine(t *testing.T) (*Engine, *fakeRemote) {
	t.Helper()
	dir := t.TempDir()
	remote := newFake()
	e, err := NewEngine(filepath.Join(dir, "state"), filepath.Join(dir, "objects"), "tablet", remote)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	if err = e.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.SetExporter(func(id, format string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("%PDF-1.7\n" + id + "\n%%EOF\n")), nil
	})
	return e, remote
}
func fixture(t *testing.T, e *Engine, id, name string) *Document {
	t.Helper()
	if id == "" {
		id = uuid.NewString()
	}
	ink := []byte("reMarkable .lines file, version=6          ")
	hash, err := e.stage(ink)
	if err != nil {
		t.Fatal(err)
	}
	doc := &Document{ID: id, Metadata: map[string]json.RawMessage{"visibleName": raw(name), "parent": raw(""), "type": raw("DocumentType"), "futureFirmwareField": raw(map[string]any{"nested": []string{"keep", "all"}}), "tags": raw([]map[string]string{{"name": "important"}})}, Entries: []*models.HashEntry{models.NewHashEntry(hash, id+"/page.rm", int64(len(ink)))}}
	if err = e.updateMetadata(doc, nil); err != nil {
		t.Fatal(err)
	}
	return doc
}
func addFixture(t *testing.T, e *Engine) *Document {
	t.Helper()
	doc := fixture(t, e, "", "Quaderno")
	g, _ := e.currentGraph()
	g.Documents[doc.ID] = doc
	if err := e.commitGraph(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestUnchangedDriveFilenamePreservesNativePDFTitle(t *testing.T) {
	e, r := setupEngine(t)
	doc := fixture(t, e, "", "Original.pdf")
	g, _ := e.currentGraph()
	g.Documents[doc.ID] = doc
	if err := e.commitGraph(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	if err := e.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	mapping := e.state.Mappings[doc.ID]
	if _, err := r.Patch(context.Background(), mapping.DriveID, map[string]any{"name": "Original.pdf"}, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := e.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	g, _ = e.currentGraph()
	if g.Documents[doc.ID].Text("visibleName") != "Original.pdf" {
		t.Fatal("unchanged Drive name removed the native title suffix")
	}
}

func TestNativeBackupReconstructsFromRemoteAndPreservesUUID(t *testing.T) {
	e, _ := setupEngine(t)
	doc := addFixture(t, e)
	for hash := range e.state.Current.Objects {
		if err := os.Remove(e.objectPath(hash)); err != nil {
			t.Fatal(err)
		}
	}
	dest := filepath.Join(t.TempDir(), "recovered")
	if n, err := e.ExportNativeBackup(context.Background(), dest); err != nil || n != 1 {
		t.Fatalf("export count=%d err=%v", n, err)
	}
	if _, err := e.ExportNativeBackup(context.Background(), dest); err == nil {
		t.Fatal("existing backup was overwritten")
	}
	other, _ := setupEngine(t)
	root, n, err := other.ReadNativeArchives(dest)
	if err != nil || n != 1 {
		t.Fatalf("recovered graph count=%d err=%v", n, err)
	}
	g, err := other.graph(root)
	if err != nil {
		t.Fatal(err)
	}
	if g.Documents[doc.ID] == nil || string(g.Documents[doc.ID].Metadata["futureFirmwareField"]) != string(doc.Metadata["futureFirmwareField"]) {
		t.Fatal("backup lost identity or unknown metadata")
	}
}

func TestRootAcknowledgedOnlyAfterVerifiedSnapshot(t *testing.T) {
	e, remote := setupEngine(t)
	doc := fixture(t, e, "", "Originale")
	g, _ := e.currentGraph()
	g.Documents[doc.ID] = doc
	remote.failRole = "snapshot"
	err := e.commitGraph(context.Background(), g)
	if err == nil {
		t.Fatal("acknowledged failed snapshot")
	}
	if e.state.Current.Generation != 0 || e.state.Pending == nil {
		t.Fatal("failed commit visible or not recoverable")
	}
	if _, _, _, _, err = e.LoadBlob("tablet", "root"); !errors.Is(err, fs.ErrorNotFound) {
		t.Fatalf("root exposed before commit: %v", err)
	}
	remote.failRole = ""
	e.Close()
	restarted, err := NewEngine(e.dir, e.objects, "tablet", remote)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if err = restarted.Cycle(context.Background()); err == nil {
		t.Fatal("expected missing exporter, after recovering original snapshot")
	}
	if restarted.state.Current.Generation != 1 || restarted.state.Pending != nil {
		t.Fatal("pending commit did not recover")
	}
	if _, err = restarted.graph(restarted.state.Current.Root); err != nil {
		t.Fatal(err)
	}
}
func TestLostSnapshotAcknowledgementAndRetryAreIdempotent(t *testing.T) {
	e, remote := setupEngine(t)
	doc := fixture(t, e, "", "A")
	g, _ := e.currentGraph()
	g.Documents[doc.ID] = doc
	remote.failRole = "snapshot"
	remote.failAfterStore = true
	if e.commitGraph(context.Background(), g) == nil {
		t.Fatal("expected lost acknowledgement")
	}
	remote.failRole = ""
	count := remote.puts
	if err := e.finishPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if remote.puts != count || e.state.Current.Generation != 1 {
		t.Fatal("created duplicate commit after lost acknowledgement")
	}
	gen, err := e.StoreBlob("tablet", "root", strings.NewReader(e.state.Current.Root), 0)
	if err != nil || gen != 1 {
		t.Fatalf("retry was not idempotent: %v", err)
	}
}
func TestIncompleteOrCorruptGraphCannotCommit(t *testing.T) {
	e, _ := setupEngine(t)
	doc := fixture(t, e, "", "A")
	if err := os.WriteFile(e.objectPath(doc.Entries[0].Hash), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	g, _ := e.currentGraph()
	g.Documents[doc.ID] = doc
	if e.commitGraph(context.Background(), g) == nil {
		t.Fatal("corrupt native graph accepted")
	}
	if e.state.Current.Generation != 0 {
		t.Fatal("invalid root became current")
	}
}
func TestUnknownMetadataAndInkSurviveDriveRename(t *testing.T) {
	e, r := setupEngine(t)
	doc := addFixture(t, e)
	if err := e.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	m := e.state.Mappings[doc.ID]
	r.edit(m.DriveID, func(f *File, b []byte) []byte { f.Name = "Rinominato.pdf"; return nil })
	if err := e.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	g, err := e.currentGraph()
	if err != nil {
		t.Fatal(err)
	}
	after := g.Documents[doc.ID]
	if after.Text("visibleName") != "Rinominato" {
		t.Fatal(after.Text("visibleName"))
	}
	if !bytes.Equal(after.Metadata["futureFirmwareField"], doc.Metadata["futureFirmwareField"]) || !bytes.Equal(after.Metadata["tags"], doc.Metadata["tags"]) {
		t.Fatal("unknown metadata or tags lost")
	}
	if len(after.Entries) != len(doc.Entries) {
		t.Fatal("native ink lost")
	}
}
func TestRootFolderDisappearanceDoesNotDeleteLibrary(t *testing.T) {
	e, r := setupEngine(t)
	addFixture(t, e)
	before := e.state.Current.Root
	r.edit(e.state.Layout.Root, func(f *File, b []byte) []byte { f.Trashed = true; return nil })
	if e.Cycle(context.Background()) == nil {
		t.Fatal("missing vault did not suspend")
	}
	if e.state.Current.Root != before {
		t.Fatal("missing vault became empty library")
	}
}
func TestExternalPDFEditCreatesAnotherDocument(t *testing.T) {
	e, r := setupEngine(t)
	doc := addFixture(t, e)
	if err := e.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	m := e.state.Mappings[doc.ID]
	r.edit(m.DriveID, func(f *File, b []byte) []byte { return []byte("%PDF-1.7\nexternal edits\n%%EOF") })
	if err := e.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	g, _ := e.currentGraph()
	if len(g.Documents) != 2 {
		t.Fatalf("expected two documents, got %d", len(g.Documents))
	}
	if g.Documents[doc.ID].Hash != doc.Hash {
		t.Fatal("original quaderno overwritten")
	}
	if err := e.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	g, _ = e.currentGraph()
	if len(g.Documents) != 2 {
		t.Fatal("retry duplicated external import")
	}
}
func TestInboxReceiptSurvivesRestart(t *testing.T) {
	e, r := setupEngine(t)
	id, _ := r.NewID(context.Background())
	path := filepath.Join(t.TempDir(), "in.pdf")
	os.WriteFile(path, []byte("%PDF-1.7\ninput\n%%EOF"), 0600)
	r.Put(context.Background(), File{ID: id, Name: "Input.pdf", MIME: "application/pdf", Parents: []string{e.state.Layout.Inbox}}, path)
	if err := e.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.Close()
	restarted, err := NewEngine(e.dir, e.objects, "tablet", r)
	if err != nil {
		t.Fatal(err)
	}
	restarted.SetExporter(e.exporter)
	t.Cleanup(restarted.Close)
	if err = restarted.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	g, _ := restarted.currentGraph()
	if len(g.Documents) != 1 || len(restarted.state.Receipts) != 1 {
		t.Fatal("inbox import duplicated across restart")
	}
}
func TestRestoreCreatesNewUUIDPreservingExistingWork(t *testing.T) {
	e, _ := setupEngine(t)
	doc := addFixture(t, e)
	snap := e.state.Current.ID
	id, err := e.Restore(context.Background(), snap, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	g, _ := e.currentGraph()
	if id == doc.ID || len(g.Documents) != 2 || g.Documents[doc.ID].Hash != doc.Hash {
		t.Fatal("restore replaced existing work")
	}
	if !bytes.Equal(g.Documents[id].Metadata["futureFirmwareField"], doc.Metadata["futureFirmwareField"]) {
		t.Fatal("restore lost unknown metadata")
	}
}
func TestNativeGenerationConflict(t *testing.T) {
	e, _ := setupEngine(t)
	addFixture(t, e)
	doc := fixture(t, e, "", "B")
	g, _ := e.currentGraph()
	g.Documents[doc.ID] = doc
	entries := append([]*models.HashEntry{}, g.RootEntries...)
	entries = append(entries, &models.HashEntry{Hash: doc.Hash, Type: "80000000", EntryName: doc.ID, Subfiles: len(doc.Entries)})
	root, _ := models.HashEntries(entries)
	e.putObject(root, bytes.NewReader(indexBytes(entries, "4", true)))
	_, err := e.StoreBlob("tablet", "root", strings.NewReader(root), 0)
	if !errors.Is(err, fs.ErrorWrongGeneration) {
		t.Fatalf("expected generation conflict, got %v", err)
	}
}
func TestCorruptStateNeverBecomesEmptyLibrary(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "state.json"), []byte("{bad"), 0600)
	if _, err := NewEngine(dir, filepath.Join(dir, "objects"), "tablet", newFake()); err == nil {
		t.Fatal("corrupt state accepted")
	}
}
func TestRetentionKeepsTenVersionsAndDeletionForNinetyDays(t *testing.T) {
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	var snapshots []Snapshot
	for i := 0; i < 20; i++ {
		snapshots = append(snapshots, Snapshot{ID: strconv.Itoa(i), Generation: int64(i), Created: now.AddDate(0, 0, -100+i), Documents: map[string]string{"doc": strconv.Itoa(i)}})
	}
	keep := RetainSnapshots(snapshots, now)
	if len(keep) < 10 || !keep["19"] {
		t.Fatal("last ten versions not retained")
	}
	snapshots = append(snapshots, Snapshot{ID: "deleted", Generation: 20, Created: now.AddDate(0, 0, -60), Documents: map[string]string{}})
	if !RetainSnapshots(snapshots, now)["19"] {
		t.Fatal("deleted document not recoverable for ninety days")
	}
}
