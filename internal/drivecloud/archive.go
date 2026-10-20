// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ddvk/rmfakecloud/internal/storage/models"
)

// VerifyNativeRoot validates a complete, already staged native tree without
// publishing it or modifying the tablet library.
func (e *Engine) VerifyNativeRoot(root string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	g, err := e.graph(root)
	if err != nil {
		return 0, err
	}
	return len(g.Documents), nil
}

// ReadNativeArchives stages a read-only RMDOC backup in a private object cache.
// It does not touch Xochitl's library, change accounts or publish a root.
func (e *Engine) ReadNativeArchives(folder string) (string, int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := os.Stat(filepath.Join(folder, ".incomplete")); err == nil {
		return "", 0, fmt.Errorf("backup export is incomplete")
	} else if !os.IsNotExist(err) {
		return "", 0, err
	}
	files, err := os.ReadDir(folder)
	if err != nil {
		return "", 0, err
	}
	graph := &Graph{Documents: map[string]*Document{}, Objects: map[string]bool{}, Schema: "4"}
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".metadata") {
			id := strings.TrimSuffix(file.Name(), ".metadata")
			if !validDocumentID(id) {
				return "", 0, fmt.Errorf("invalid backup folder UUID")
			}
			data, err := os.ReadFile(filepath.Join(folder, file.Name()))
			if err != nil {
				return "", 0, err
			}
			var metadata map[string]json.RawMessage
			if err = json.Unmarshal(data, &metadata); err != nil {
				return "", 0, err
			}
			doc := &Document{ID: id, Metadata: metadata}
			if !doc.Folder() {
				return "", 0, fmt.Errorf("separate metadata must describe a folder")
			}
			hash, err := e.stage(data)
			if err != nil {
				return "", 0, err
			}
			doc.Entries = []*models.HashEntry{models.NewHashEntry(hash, file.Name(), int64(len(data)))}
			if err = e.stageDocument(doc); err != nil {
				return "", 0, err
			}
			graph.Documents[id] = doc
			continue
		}
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".rmdoc") {
			continue
		}
		archive, err := zip.OpenReader(filepath.Join(folder, file.Name()))
		if err != nil {
			return "", 0, err
		}
		doc, err := e.readArchive(&archive.Reader)
		archive.Close()
		if err != nil {
			return "", 0, err
		}
		if _, ok := graph.Documents[doc.ID]; ok {
			return "", 0, fmt.Errorf("duplicate document UUID in backup")
		}
		graph.Documents[doc.ID] = doc
	}
	if len(graph.Documents) == 0 {
		return "", 0, fmt.Errorf("backup has no native documents")
	}
	var entries []*models.HashEntry
	for _, doc := range graph.Documents {
		size := int64(0)
		for _, entry := range doc.Entries {
			size += entry.Size
		}
		entries = append(entries, &models.HashEntry{Hash: doc.Hash, Type: "0", EntryName: doc.ID, Subfiles: len(doc.Entries), Size: size})
	}
	_, err = models.HashEntries(entries)
	if err != nil {
		return "", 0, err
	}
	data := indexBytes(entries, "4", true)
	hash := digest(data)
	if err = e.putObject(hash, strings.NewReader(string(data))); err != nil {
		return "", 0, err
	}
	if _, err = e.graph(hash); err != nil {
		return "", 0, err
	}
	return hash, len(graph.Documents), nil
}
func (e *Engine) readArchive(archive *zip.Reader) (*Document, error) {
	var id string
	seen := map[string]bool{}
	doc := &Document{}
	for _, entry := range archive.File {
		if strings.HasSuffix(entry.Name, "/") {
			continue
		}
		if strings.HasSuffix(entry.Name, ".metadata") && !strings.Contains(entry.Name, "/") {
			candidate := strings.TrimSuffix(entry.Name, ".metadata")
			if !validDocumentID(candidate) || id != "" {
				return nil, fmt.Errorf("invalid archive metadata identity")
			}
			id = candidate
		}
	}
	if id == "" {
		return nil, fmt.Errorf("RMDOC has no native metadata")
	}
	doc.ID = id
	total := uint64(0)
	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		if path.Clean(entry.Name) != entry.Name || strings.Contains(entry.Name, "\\") || strings.HasPrefix(entry.Name, "/") || strings.Contains(entry.Name, "..") || seen[entry.Name] || entry.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("unsafe archive entry")
		}
		seen[entry.Name] = true
		if !(strings.HasPrefix(entry.Name, id+".") || strings.HasPrefix(entry.Name, id+"/")) {
			return nil, fmt.Errorf("RMDOC contains another document")
		}
		total += entry.UncompressedSize64
		if total > 512<<20 {
			return nil, fmt.Errorf("RMDOC too large")
		}
		r, err := entry.Open()
		if err != nil {
			return nil, err
		}
		temp, err := os.CreateTemp(e.dir, ".archive-")
		if err != nil {
			r.Close()
			return nil, err
		}
		n, err := io.Copy(temp, io.LimitReader(r, int64(entry.UncompressedSize64)+1))
		r.Close()
		temp.Close()
		if err != nil || n != int64(entry.UncompressedSize64) {
			os.Remove(temp.Name())
			return nil, fmt.Errorf("RMDOC entry size or CRC mismatch")
		}
		f, err := os.Open(temp.Name())
		if err != nil {
			os.Remove(temp.Name())
			return nil, err
		}
		hash, _, err := models.Hash(f)
		if err == nil {
			_, err = f.Seek(0, 0)
		}
		if err == nil {
			err = e.putObject(hash, f)
		}
		f.Close()
		if entry.Name == id+".metadata" && err == nil {
			data, readErr := os.ReadFile(temp.Name())
			if readErr != nil {
				err = readErr
			} else {
				err = json.Unmarshal(data, &doc.Metadata)
			}
		}
		os.Remove(temp.Name())
		if err != nil {
			return nil, err
		}
		doc.Entries = append(doc.Entries, models.NewHashEntry(hash, entry.Name, n))
	}
	if doc.Metadata == nil {
		return nil, fmt.Errorf("missing native metadata")
	}
	if err := e.stageDocument(doc); err != nil {
		return nil, err
	}
	return doc, nil
}
func (e *Engine) SeedVerifiedBackup(ctx context.Context, root string, expectedGeneration int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state.Current.Generation != expectedGeneration {
		return fmt.Errorf("cloud changed since backup verification")
	}
	_, err := e.commit(ctx, root, expectedGeneration)
	return err
}
