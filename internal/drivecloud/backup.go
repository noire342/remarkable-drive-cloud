// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// ExportNativeBackup reconstructs a portable native graph in a new private
// directory. It never writes to Xochitl, overwrites a backup or switches clouds.
// Folder records are also archived; restore them through the bridge, not the
// document-only USB import endpoint.
func (e *Engine) ExportNativeBackup(ctx context.Context, destination string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkLayout(ctx); err != nil {
		return 0, err
	}
	if e.state.Pending != nil || e.state.Current.ID == "" {
		return 0, fmt.Errorf("complete the pending snapshot before exporting")
	}
	if err := e.verifyArchiveEvents(ctx); err != nil {
		return 0, err
	}
	for hash := range e.state.Current.Objects {
		if err := e.ensureLocal(ctx, hash); err != nil {
			return 0, err
		}
	}
	graph, err := e.graph(e.state.Current.Root)
	if err != nil {
		return 0, err
	}
	if destination == "" {
		return 0, fmt.Errorf("a new private output directory is required")
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return 0, err
	}
	if err = os.Mkdir(destination, 0700); err != nil {
		return 0, fmt.Errorf("backup destination must not already exist: %w", err)
	}
	marker := filepath.Join(destination, ".incomplete")
	if err = os.WriteFile(marker, []byte("Export incomplete. Do not use for migration.\n"), 0600); err != nil {
		return 0, err
	}
	ids := make([]string, 0, len(graph.Documents))
	for id := range graph.Documents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err = ctx.Err(); err != nil {
			return 0, err
		}
		doc := graph.Documents[id]
		err = atomicWrite(filepath.Join(destination, id+".rmdoc"), 0600, func(w io.Writer) error {
			archive := zip.NewWriter(w)
			for _, entry := range doc.Entries {
				out, err := archive.Create(entry.EntryName)
				if err != nil {
					archive.Close()
					return err
				}
				in, err := os.Open(e.objectPath(entry.Hash))
				if err != nil {
					archive.Close()
					return err
				}
				n, err := io.Copy(out, in)
				in.Close()
				if err != nil || n != entry.Size {
					archive.Close()
					return fmt.Errorf("native object changed during export")
				}
			}
			return archive.Close()
		})
		if err != nil {
			return 0, err
		}
	}
	if err = saveJSON(filepath.Join(destination, "drive-snapshot.json"), e.state.Current); err != nil {
		return 0, err
	}
	if err = os.Remove(marker); err != nil {
		return 0, err
	}
	return len(ids), nil
}
