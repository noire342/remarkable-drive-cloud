// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
)

func (e *Engine) nativeParent(driveID string) (string, bool) {
	if driveID == e.state.Layout.Library {
		return "", true
	}
	for id, m := range e.state.Mappings {
		if m.DriveID == driveID {
			return id, true
		}
	}
	return "", false
}
func (e *Engine) driveParent(nativeID string) (string, bool) {
	if nativeID == "" || nativeID == "trash" {
		return e.state.Layout.Library, true
	}
	m, ok := e.state.Mappings[nativeID]
	return m.DriveID, ok
}
func firstParent(f File) string {
	if len(f.Parents) == 1 {
		return f.Parents[0]
	}
	return ""
}
func (e *Engine) libraryAncestor(ctx context.Context, id string) bool {
	seen := map[string]bool{}
	for i := 0; i < 100 && id != ""; i++ {
		if id == e.state.Layout.Library {
			return true
		}
		if seen[id] {
			return false
		}
		seen[id] = true
		f, err := e.remote.Get(ctx, id)
		if err != nil || f.Trashed {
			return false
		}
		id = firstParent(f)
	}
	return false
}

func (e *Engine) reconcileLibrary(ctx context.Context) error {
	// Poll only our own tree. The account-wide change feed can contain millions
	// of unrelated deletions and must never delay a native commit or PDF export.
	if err := e.verifyArchiveEvents(ctx); err != nil {
		return err
	}
	queue := []string{e.state.Layout.Library}
	seen := map[string]bool{}
	for len(queue) > 0 {
		parentID := queue[0]
		queue = queue[1:]
		if seen[parentID] {
			continue
		}
		seen[parentID] = true
		files, err := e.remote.List(ctx, "'"+parentID+"' in parents and trashed = false and mimeType = '"+FolderMIME+"'")
		if err != nil {
			return err
		}
		for _, file := range files {
			if file.Trashed || file.MIME != FolderMIME || !hasParent(file, parentID) {
				continue
			}
			if _, exists := e.nativeParent(file.ID); !exists {
				parent, ok := e.nativeParent(parentID)
				if !ok {
					return ErrSuspended
				}
				if err := e.newFolder(ctx, file, parent); err != nil {
					return err
				}
			}
			queue = append(queue, file.ID)
		}
	}
	ids := make([]string, 0, len(e.state.Mappings))
	for id := range e.state.Mappings {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		mapping := e.state.Mappings[id]
		file, err := e.remote.Get(ctx, mapping.DriveID)
		change := Change{FileID: mapping.DriveID}
		if errors.Is(err, ErrNotFound) {
			if mapping.NativeHash == "" && mapping.ExportHash == "" {
				// A reserved ID whose first upload failed is not a deletion.
				continue
			}
			change.Removed = true
		} else if err != nil {
			return err
		} else {
			change.File = &file
		}
		if err := e.applyDriveChange(ctx, id, change); err != nil {
			return err
		}
	}
	return e.save()
}
func (e *Engine) applyDriveChange(ctx context.Context, id string, change Change) error {
	mapping := e.state.Mappings[id]
	f := change.File
	if f != nil && f.Version == mapping.DriveVersion {
		return nil
	}
	graph, err := e.currentGraph()
	if err != nil {
		return err
	}
	doc, exists := graph.Documents[id]
	if !exists {
		return nil
	}
	if f != nil && mapping.PendingHash != "" && f.MD5 == mapping.PendingMD5 && f.Properties["native_revision"] == mapping.PendingHash {
		mapping.Name = mapping.PendingName
		mapping.Parent = mapping.PendingParent
		mapping.MD5 = f.MD5
		mapping.ExportHash = mapping.PendingHash
		mapping.NativeHash = mapping.PendingHash
		mapping.PendingHash = ""
		mapping.PendingMD5 = ""
		mapping.PendingName = ""
		mapping.PendingParent = ""
	}
	comparisonMD5 := mapping.MD5
	if comparisonMD5 == "" {
		comparisonMD5 = mapping.PendingMD5
	}
	deleted := change.Removed || f == nil || f.Trashed
	name, parent := mapping.Name, mapping.Parent
	if f != nil {
		if doc.Folder() {
			name = f.Name
		} else if f.Name != strings.TrimSuffix(mapping.Name, ".pdf")+".pdf" {
			// An unchanged derived filename is not a native rename. In
			// particular, a native title may itself end in .pdf.
			name = strings.TrimSuffix(f.Name, filepath.Ext(f.Name))
		}
		if !deleted {
			var ok bool
			parent, ok = e.nativeParent(firstParent(*f))
			if !ok {
				return fmt.Errorf("managed file moved outside library; synchronization paused")
			}
		}
	}
	if f != nil && !doc.Folder() && comparisonMD5 != "" && f.MD5 != "" && f.MD5 != comparisonMD5 {
		receipt := "external/" + f.ID + "/" + f.MD5
		preserved := *f
		preserved.Name = name + " (modificato su Drive).pdf"
		if _, err = e.importFile(ctx, preserved, parent, receipt); err != nil {
			return err
		}
		// The external bytes are now part of a verified native snapshot; keep that
		// receipt before replacing the derived PDF with a fresh native export.
		graph, err = e.currentGraph()
		if err != nil {
			return err
		}
		doc = graph.Documents[id]
		mapping.ExportHash = ""
	}
	fields := map[string]any{}
	conflict := false
	if name != mapping.Name {
		if doc.Text("visibleName") != mapping.Name && doc.Text("visibleName") != name {
			conflict = true
		} else {
			fields["visibleName"] = name
		}
	}
	if parent != mapping.Parent {
		if doc.Text("parent") != mapping.Parent && doc.Text("parent") != parent {
			conflict = true
		} else {
			fields["parent"] = parent
		}
	}
	if deleted && !mapping.Trashed {
		if doc.Hash != mapping.NativeHash {
			conflict = true
		} else {
			fields["parent"] = "trash"
		}
	}
	if !deleted && mapping.Trashed {
		fields["parent"] = parent
		fields["deleted"] = false
	}
	if conflict {
		copyID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(e.state.Vault+"/conflict/"+id+"/"+changeVersion(change))).String()
		if _, ok := graph.Documents[copyID]; !ok {
			copy := e.clone(doc, copyID, name+" (conflitto Drive)")
			target := parent
			if deleted {
				target = ""
			}
			if err = e.updateMetadata(copy, map[string]any{"parent": target}); err != nil {
				return err
			}
			graph.Documents[copyID] = copy
			if err = e.commitGraph(ctx, graph); err != nil {
				return err
			}
		}
	} else if len(fields) > 0 {
		copy := copyDocument(doc)
		if err = e.updateMetadata(copy, fields); err != nil {
			return err
		}
		graph.Documents[id] = copy
		if err = e.commitGraph(ctx, graph); err != nil {
			return err
		}
		doc = copy
	}
	mapping.Name = doc.Text("visibleName")
	mapping.Parent = doc.Text("parent")
	mapping.Trashed = doc.Deleted()
	mapping.NativeHash = doc.Hash
	if f != nil {
		mapping.DriveVersion = f.Version
		mapping.MD5 = f.MD5
	}
	e.state.Mappings[id] = mapping
	return e.save()
}
func changeVersion(change Change) string {
	if change.File != nil {
		return change.File.Version
	}
	return "removed"
}

func (e *Engine) publishLibrary(ctx context.Context) error {
	graph, err := e.currentGraph()
	if err != nil {
		return err
	}
	// Parents precede their children; malformed cycles fail without flattening.
	remaining := map[string]*Document{}
	for id, doc := range graph.Documents {
		remaining[id] = doc
	}
	for len(remaining) > 0 {
		progress := false
		for id, doc := range remaining {
			if doc.Folder() {
				parent, ok := e.driveParent(doc.Text("parent"))
				if !ok {
					continue
				}
				if err = e.publishOne(ctx, doc, parent); err != nil {
					return err
				}
				delete(remaining, id)
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	for id, doc := range remaining {
		if doc.Folder() {
			return fmt.Errorf("unresolved folder hierarchy at %s", id)
		}
		parent, ok := e.driveParent(doc.Text("parent"))
		if !ok {
			return fmt.Errorf("missing document parent")
		}
		if err = e.publishOne(ctx, doc, parent); err != nil {
			return err
		}
	}
	// A native root may remove a document entirely. Derived files go to trash;
	// immutable historical data remain reachable through retained snapshots.
	for id, m := range e.state.Mappings {
		if _, exists := graph.Documents[id]; exists || m.Trashed {
			continue
		}
		if _, err = e.remote.Patch(ctx, m.DriveID, map[string]any{"trashed": true}, "", ""); err != nil {
			return err
		}
		m.Trashed = true
		e.state.Mappings[id] = m
		if err = e.save(); err != nil {
			return err
		}
	}
	return nil
}

// PublishLibrary prepares readable Drive copies without importing the inbox or
// changing the tablet's cloud account. Existing external changes are reconciled
// before any derived file is updated.
func (e *Engine) PublishLibrary(ctx context.Context) error {
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
	e.state.LastError = ""
	return e.save()
}
func (e *Engine) publishOne(ctx context.Context, doc *Document, parent string) error {
	m, exists := e.state.Mappings[doc.ID]
	if !exists {
		id, err := e.remote.NewID(ctx)
		if err != nil {
			return err
		}
		m = Mapping{NativeID: doc.ID, DriveID: id}
		e.state.Mappings[doc.ID] = m
		if err = e.save(); err != nil {
			return err
		}
	}
	name := doc.Text("visibleName")
	display := name
	if !doc.Folder() {
		display = strings.TrimSuffix(name, ".pdf") + ".pdf"
	}
	props := map[string]string{"vault": e.state.Vault, "role": "document", "native_id": doc.ID, "native_revision": doc.Hash}
	mime := "application/pdf"
	if doc.Folder() {
		mime = FolderMIME
		props["role"] = "folder"
	}
	remote, err := e.remote.Get(ctx, m.DriveID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err == nil && remote.Properties["vault"] != "" && remote.Properties["vault"] != e.state.Vault {
		return ErrSuspended
	}
	if err == nil && exists && m.DriveVersion != "" && remote.Version != m.DriveVersion {
		// A user can edit Drive after this cycle's reconciliation pass. Leave
		// that file alone; reconcile its newer revision on the next pass.
		return nil
	}
	if doc.Deleted() {
		if err == nil && !remote.Trashed {
			remote, err = e.remote.Patch(ctx, m.DriveID, map[string]any{"trashed": true}, "", "")
			if err != nil {
				return err
			}
		}
		m.Trashed = true
		m.Name = name
		m.Parent = doc.Text("parent")
		m.NativeHash = doc.Hash
		e.state.Mappings[doc.ID] = m
		return e.save()
	}
	if doc.Folder() && errors.Is(err, ErrNotFound) {
		remote, err = e.remote.Put(ctx, File{ID: m.DriveID, Name: display, MIME: mime, Parents: []string{parent}, Properties: props}, "")
		if err != nil {
			return err
		}
	}
	if !doc.Folder() && (m.ExportHash != doc.Hash || errors.Is(err, ErrNotFound)) {
		if e.exporter == nil {
			return fmt.Errorf("native PDF exporter unavailable")
		}
		if e.verifyExport != nil {
			if err := e.verifyExport(doc); err != nil {
				return err
			}
		}
		r, err := e.exporter(doc.ID, "pdf")
		if err != nil {
			return err
		}
		path := filepath.Join(e.dir, "export-"+doc.ID+".pdf")
		err = atomicWrite(path, 0600, func(w io.Writer) error {
			n, err := io.Copy(w, io.LimitReader(r, (512<<20)+1))
			if err == nil && n > 512<<20 {
				return fmt.Errorf("native PDF export too large")
			}
			return err
		})
		r.Close()
		if err != nil {
			return err
		}
		if e.verifyExport != nil {
			if err := e.verifyExport(doc); err != nil {
				os.Remove(path)
				return err
			}
		}
		md5sum, _, err := fileMD5(path)
		if err != nil {
			return err
		}
		m.PendingHash = doc.Hash
		m.PendingMD5 = md5sum
		m.PendingName = name
		m.PendingParent = doc.Text("parent")
		e.state.Mappings[doc.ID] = m
		if err = e.save(); err != nil {
			return err
		}
		remote, err = e.remote.Put(ctx, File{ID: m.DriveID, Name: display, MIME: mime, Parents: []string{parent}, Properties: props}, path)
		if err != nil {
			return err
		}
		os.Remove(path)
		m.ExportHash = doc.Hash
		m.PendingHash = ""
		m.PendingMD5 = ""
		m.PendingName = ""
		m.PendingParent = ""
	}
	if remote.Name != display || firstParent(remote) != parent || remote.Trashed {
		remove := ""
		if firstParent(remote) != parent {
			remove = firstParent(remote)
		}
		remote, err = e.remote.Patch(ctx, m.DriveID, map[string]any{"name": display, "trashed": false, "appProperties": props}, parent, remove)
		if err != nil {
			return err
		}
	}
	m.Name = name
	m.Parent = doc.Text("parent")
	m.NativeHash = doc.Hash
	m.Trashed = false
	m.MD5 = remote.MD5
	m.DriveVersion = remote.Version
	if doc.Folder() {
		m.ExportHash = doc.Hash
		m.PendingHash = ""
		m.PendingMD5 = ""
		m.PendingName = ""
		m.PendingParent = ""
	}
	e.state.Mappings[doc.ID] = m
	return e.save()
}
func (e *Engine) importInbox(ctx context.Context) error {
	files, err := e.remote.List(ctx, "'"+e.state.Layout.Inbox+"' in parents and trashed = false")
	if err != nil {
		return err
	}
	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(file.Name))
		if file.MIME == FolderMIME || ext != ".pdf" && ext != ".epub" {
			continue
		}
		receipt := "inbox/" + file.ID + "/" + file.MD5
		if file.MD5 == "" {
			receipt = "inbox/" + file.ID + "/" + file.Version
		}
		if _, err = e.importFile(ctx, file, "", receipt); err != nil {
			return err
		}
	}
	return nil
}
