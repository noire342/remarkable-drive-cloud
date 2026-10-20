// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

func (e *Engine) consumeChanges(ctx context.Context) error {
	if e.state.Cursor == "" {
		return ErrSuspended
	}
	cursor := e.state.Cursor
	var all []Change
	next := ""
	for {
		page, err := e.remote.Changes(ctx, cursor)
		if err != nil {
			return err
		}
		all = append(all, page.Changes...)
		if page.Next == "" {
			next = page.Start
			break
		}
		cursor = page.Next
	}
	// Adopt new folders before moves into them, irrespective of event ordering.
	for pass := 0; pass < 100; pass++ {
		progress := false
		for _, change := range all {
			f := change.File
			if f == nil || f.Trashed || f.MIME != FolderMIME {
				continue
			}
			if _, ok := e.nativeParent(f.ID); ok {
				continue
			}
			parent, ok := e.nativeParent(firstParent(*f))
			if !ok {
				continue
			}
			if err := e.newFolder(ctx, *f, parent); err != nil {
				return err
			}
			progress = true
		}
		if !progress {
			break
		}
	}
	for _, change := range all {
		// Immutable archive changes are integrity errors, never document deletions.
		for _, obj := range e.state.Current.Objects {
			if obj.ID == change.FileID {
				f := change.File
				if change.Removed || f == nil || f.Trashed || f.MD5 != obj.MD5 || !hasParent(*f, e.state.Layout.Objects) {
					return fmt.Errorf("archive object changed: %w", ErrSuspended)
				}
			}
		}
		if change.FileID == e.state.Current.ID && (change.Removed || change.File == nil || change.File.Trashed) {
			return fmt.Errorf("current snapshot removed: %w", ErrSuspended)
		}
		var mappingID string
		for id, m := range e.state.Mappings {
			if m.DriveID == change.FileID {
				mappingID = id
				break
			}
		}
		if mappingID == "" {
			continue
		}
		if err := e.applyDriveChange(ctx, mappingID, change); err != nil {
			return err
		}
	}
	if next == "" {
		return fmt.Errorf("Drive changes response missing cursor")
	}
	e.state.Cursor = next
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
		name = f.Name
		if !doc.Folder() {
			name = strings.TrimSuffix(name, filepath.Ext(name))
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
