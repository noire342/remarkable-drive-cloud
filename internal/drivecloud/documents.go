// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ddvk/rmfakecloud/internal/storage/models"
	"github.com/google/uuid"
)

func (e *Engine) currentGraph() (*Graph, error) {
	if e.state.Current.Root == "" {
		return &Graph{Documents: map[string]*Document{}, Objects: map[string]bool{}, Schema: "4", RawRoot: true}, nil
	}
	return e.graph(e.state.Current.Root)
}
func (e *Engine) stage(data []byte) (string, error) {
	hash := digest(data)
	return hash, e.putObject(hash, bytes.NewReader(data))
}
func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func copyDocument(d *Document) *Document {
	copy := *d
	copy.Metadata = map[string]json.RawMessage{}
	for k, v := range d.Metadata {
		copy.Metadata[k] = append(json.RawMessage{}, v...)
	}
	copy.Entries = nil
	for _, entry := range d.Entries {
		next := *entry
		copy.Entries = append(copy.Entries, &next)
	}
	return &copy
}
func (e *Engine) updateMetadata(doc *Document, fields map[string]any) error {
	for k, v := range fields {
		doc.Metadata[k] = raw(v)
	}
	var version int
	json.Unmarshal(doc.Metadata["version"], &version)
	doc.Metadata["version"] = raw(version + 1)
	doc.Metadata["lastModified"] = raw(strconv.FormatInt(e.now().UnixMilli(), 10))
	doc.Metadata["metadatamodified"] = raw(true)
	doc.Metadata["synced"] = raw(true)
	data, err := json.Marshal(doc.Metadata)
	if err != nil {
		return err
	}
	hash, err := e.stage(data)
	if err != nil {
		return err
	}
	found := false
	for _, entry := range doc.Entries {
		if strings.HasSuffix(entry.EntryName, ".metadata") {
			entry.Hash = hash
			entry.Size = int64(len(data))
			found = true
		}
	}
	if !found {
		doc.Entries = append(doc.Entries, models.NewHashEntry(hash, doc.ID+".metadata", int64(len(data))))
	}
	return e.stageDocument(doc)
}
func (e *Engine) stageDocument(doc *Document) error {
	hash, err := models.HashEntries(doc.Entries)
	if err != nil {
		return err
	}
	data := indexBytes(doc.Entries, "3", false)
	if err = e.putObject(hash, bytes.NewReader(data)); err != nil {
		return err
	}
	doc.Hash = hash
	return nil
}
func (e *Engine) commitGraph(ctx context.Context, g *Graph) error {
	var entries []*models.HashEntry
	for _, doc := range g.Documents {
		size := int64(0)
		for _, p := range doc.Entries {
			size += p.Size
		}
		kind := "80000000"
		if g.RawRoot {
			kind = "0"
		}
		entries = append(entries, &models.HashEntry{Hash: doc.Hash, Type: kind, EntryName: doc.ID, Subfiles: len(doc.Entries), Size: size})
	}
	hash, err := models.HashEntries(entries)
	if err != nil {
		return err
	}
	data := indexBytes(entries, g.Schema, true)
	if g.RawRoot {
		hash = digest(data)
	}
	if err = e.putObject(hash, bytes.NewReader(data)); err != nil {
		return err
	}
	_, err = e.commit(ctx, hash, e.state.Current.Generation)
	if err == nil && e.notify != nil {
		e.notify()
	}
	return err
}
func (e *Engine) newFolder(ctx context.Context, file File, parent string) error {
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(e.state.Vault+"/folder/"+file.ID)).String()
	g, err := e.currentGraph()
	if err != nil {
		return err
	}
	if _, exists := g.Documents[id]; !exists {
		doc := &Document{ID: id, Metadata: map[string]json.RawMessage{"visibleName": raw(file.Name), "parent": raw(parent), "type": raw("CollectionType"), "createdTime": raw(strconv.FormatInt(e.now().UnixMilli(), 10))}}
		if err = e.updateMetadata(doc, nil); err != nil {
			return err
		}
		g.Documents[id] = doc
		if err = e.commitGraph(ctx, g); err != nil {
			return err
		}
	}
	doc := g.Documents[id]
	e.state.Mappings[id] = Mapping{NativeID: id, DriveID: file.ID, Name: doc.Text("visibleName"), Parent: parent, NativeHash: doc.Hash, ExportHash: doc.Hash, DriveVersion: file.Version}
	return e.save()
}
func (e *Engine) clone(doc *Document, id, name string) *Document {
	result := copyDocument(doc)
	old := result.ID
	result.ID = id
	for _, entry := range result.Entries {
		if strings.HasPrefix(entry.EntryName, old) {
			entry.EntryName = id + strings.TrimPrefix(entry.EntryName, old)
		}
	}
	result.Metadata["visibleName"] = raw(name)
	result.Metadata["deleted"] = raw(false)
	return result
}
func (e *Engine) importFile(ctx context.Context, file File, parent, receipt string) (string, error) {
	if id, ok := e.state.Receipts[receipt]; ok {
		return id, nil
	}
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(e.state.Vault+"/import/"+receipt)).String()
	g, err := e.currentGraph()
	if err != nil {
		return "", err
	}
	if _, ok := g.Documents[id]; ok {
		e.state.Receipts[receipt] = id
		return id, e.save()
	}
	ext := strings.ToLower(filepath.Ext(file.Name))
	if ext != ".pdf" && ext != ".epub" {
		return "", fmt.Errorf("only PDF and EPUB imports supported")
	}
	r, err := e.remote.Download(ctx, file.ID)
	if err != nil {
		return "", err
	}
	defer r.Close()
	temp, err := os.CreateTemp(e.dir, ".import-")
	if err != nil {
		return "", err
	}
	defer os.Remove(temp.Name())
	n, err := io.Copy(temp, io.LimitReader(r, (512<<20)+1))
	temp.Close()
	if err != nil {
		return "", err
	}
	if n > 512<<20 || n == 0 {
		return "", fmt.Errorf("import exceeds tablet limit or is empty")
	}
	sum, _, err := fileMD5(temp.Name())
	if err != nil {
		return "", err
	}
	if file.MD5 != "" && sum != file.MD5 {
		return "", fmt.Errorf("import changed while downloading; retry")
	}
	if ext == ".pdf" {
		f, _ := os.Open(temp.Name())
		var head [5]byte
		_, err = f.Read(head[:])
		f.Close()
		if err != nil || string(head[:]) != "%PDF-" {
			return "", fmt.Errorf("invalid PDF")
		}
	} else {
		z, err := zip.OpenReader(temp.Name())
		if err != nil {
			return "", fmt.Errorf("invalid EPUB")
		}
		valid := false
		for _, entry := range z.File {
			if entry.Name == "mimetype" {
				rc, er := entry.Open()
				if er == nil {
					b, _ := io.ReadAll(io.LimitReader(rc, 100))
					rc.Close()
					valid = string(b) == "application/epub+zip"
				}
				break
			}
		}
		z.Close()
		if !valid {
			return "", fmt.Errorf("invalid EPUB mimetype")
		}
	}
	payload, err := os.Open(temp.Name())
	if err != nil {
		return "", err
	}
	hash, size, err := models.Hash(payload)
	if err == nil {
		_, err = payload.Seek(0, 0)
	}
	if err == nil {
		err = e.putObject(hash, payload)
	}
	payload.Close()
	if err != nil {
		return "", err
	}
	content := map[string]any{"dummyDocument": false, "fileType": strings.TrimPrefix(ext, "."), "fontName": "", "lastOpenedPage": 0, "lineHeight": -1, "margins": 180, "orientation": "portrait", "pageCount": 0, "pages": []any{}, "textScale": 1, "transform": map[string]int{"m11": 1, "m12": 0, "m13": 0, "m21": 0, "m22": 1, "m23": 0, "m31": 0, "m32": 0, "m33": 1}}
	contentBytes, _ := json.Marshal(content)
	contentHash, err := e.stage(contentBytes)
	if err != nil {
		return "", err
	}
	doc := &Document{ID: id, Metadata: map[string]json.RawMessage{"visibleName": raw(strings.TrimSuffix(file.Name, filepath.Ext(file.Name))), "parent": raw(parent), "type": raw("DocumentType"), "createdTime": raw(strconv.FormatInt(e.now().UnixMilli(), 10)), "driveCloudImport": raw(receipt)}, Entries: []*models.HashEntry{models.NewHashEntry(hash, id+ext, size), models.NewHashEntry(contentHash, id+".content", int64(len(contentBytes)))}}
	if err = e.updateMetadata(doc, nil); err != nil {
		return "", err
	}
	g.Documents[id] = doc
	if err = e.commitGraph(ctx, g); err != nil {
		return "", err
	}
	e.state.Receipts[receipt] = id
	return id, e.save()
}

// Restore always creates a distinct native UUID. Existing work is never replaced.
func (e *Engine) Restore(ctx context.Context, snapshotID, documentID string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkLayout(ctx); err != nil {
		return "", err
	}
	snapshot, err := e.readSnapshot(ctx, snapshotID)
	if err != nil {
		return "", err
	}
	for hash, obj := range snapshot.Objects {
		if _, err := os.Stat(e.objectPath(hash)); err == nil {
			continue
		}
		r, err := e.remote.Download(ctx, obj.ID)
		if err != nil {
			return "", err
		}
		err = atomicWrite(e.objectPath(hash), 0600, func(w io.Writer) error {
			h := sha256.New()
			n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(r, obj.Size+1))
			if err != nil {
				return err
			}
			if n != obj.Size || hex.EncodeToString(h.Sum(nil)) != obj.SHA256 {
				return fmt.Errorf("snapshot object checksum mismatch")
			}
			return nil
		})
		r.Close()
		if err != nil {
			return "", err
		}
	}
	old, err := e.graph(snapshot.Root)
	if err != nil {
		return "", err
	}
	source, ok := old.Documents[documentID]
	if !ok {
		return "", fmt.Errorf("document absent from snapshot")
	}
	if source.Folder() {
		return "", fmt.Errorf("restore a document, not an entire folder")
	}
	current, err := e.currentGraph()
	if err != nil {
		return "", err
	}
	id := uuid.NewString()
	copy := e.clone(source, id, source.Text("visibleName")+" (ripristinato)")
	if err = e.updateMetadata(copy, map[string]any{"parent": ""}); err != nil {
		return "", err
	}
	current.Documents[id] = copy
	return id, e.commitGraph(ctx, current)
}
func (e *Engine) readSnapshot(ctx context.Context, id string) (Snapshot, error) {
	f, err := e.remote.Get(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}
	if f.Trashed || !hasParent(f, e.state.Layout.Snapshots) || f.Properties["vault"] != e.state.Vault || f.Properties["role"] != "snapshot" {
		return Snapshot{}, ErrSuspended
	}
	r, err := e.remote.Download(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}
	defer r.Close()
	var snap Snapshot
	err = json.NewDecoder(io.LimitReader(r, 32<<20)).Decode(&snap)
	if err != nil {
		return snap, err
	}
	if snap.Version != FormatVersion || snap.ID != id || snap.Vault != e.state.Vault || !nativeHash.MatchString(snap.Root) || snap.Objects == nil {
		return snap, ErrSuspended
	}
	for hash, obj := range snap.Objects {
		if !nativeHash.MatchString(hash) || !nativeHash.MatchString(obj.SHA256) || obj.ID == "" || obj.Size < 0 || obj.Size > 2<<30 {
			return snap, ErrSuspended
		}
	}
	return snap, nil
}
