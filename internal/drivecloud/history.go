// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"context"
	"fmt"
	"sort"
	"time"
)

type HistoryEntry struct {
	Names      map[string]string `json:"names"`
	ID         string            `json:"snapshot_id"`
	Generation int64             `json:"generation"`
	Created    time.Time         `json:"created"`
	Documents  map[string]string `json:"documents"`
}

func (e *Engine) History(ctx context.Context) ([]HistoryEntry, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.history(ctx)
}
func (e *Engine) history(ctx context.Context) ([]HistoryEntry, error) {
	if err := e.checkLayout(ctx); err != nil {
		return nil, err
	}
	files, err := e.remote.List(ctx, "'"+e.state.Layout.Snapshots+"' in parents and trashed = false")
	if err != nil {
		return nil, err
	}
	result := []HistoryEntry{}
	for _, f := range files {
		if f.Properties["vault"] != e.state.Vault || f.Properties["role"] != "snapshot" {
			continue
		}
		snap, err := e.readSnapshot(ctx, f.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, HistoryEntry{ID: snap.ID, Generation: snap.Generation, Created: snap.Created, Documents: snap.Documents, Names: snap.Names})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Generation > result[j].Generation })
	return result, nil
}

// RetainSnapshots is deliberately conservative. Besides all 30-day states and
// ten distinct versions/document, it keeps 90 days around document deletion.
func RetainSnapshots(snapshots []Snapshot, now time.Time) map[string]bool {
	ordered := append([]Snapshot(nil), snapshots...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Generation > ordered[j].Generation })
	keep := map[string]bool{}
	versions := map[string]map[string]bool{}
	for i, snap := range ordered {
		retain := i == 0 || !snap.Created.Before(now.AddDate(0, 0, -30))
		for id, hash := range snap.Documents {
			if versions[id] == nil {
				versions[id] = map[string]bool{}
			}
			if !versions[id][hash] && len(versions[id]) < 10 {
				retain = true
			}
			versions[id][hash] = true
		}
		if i > 0 && !ordered[i-1].Created.Before(now.AddDate(0, 0, -90)) {
			for id := range snap.Documents {
				if _, exists := ordered[i-1].Documents[id]; !exists {
					retain = true
				}
			}
		}
		if retain {
			keep[snap.ID] = true
		}
	}
	return keep
}
func (e *Engine) Prune(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkLayout(ctx); err != nil {
		return err
	}
	if e.state.Pending != nil {
		return fmt.Errorf("finish pending transaction before pruning")
	}
	files, err := e.remote.List(ctx, "'"+e.state.Layout.Snapshots+"' in parents and trashed = false")
	if err != nil {
		return err
	}
	var snapshots []Snapshot
	for _, f := range files {
		if f.Properties["vault"] != e.state.Vault || f.Properties["role"] != "snapshot" {
			continue
		}
		s, err := e.readSnapshot(ctx, f.ID)
		if err != nil {
			return err
		}
		snapshots = append(snapshots, s)
	}
	keep := RetainSnapshots(snapshots, e.now())
	objects := map[string]bool{}
	for _, s := range snapshots {
		if keep[s.ID] {
			for _, obj := range s.Objects {
				objects[obj.ID] = true
			}
		}
	}
	for _, s := range snapshots {
		if keep[s.ID] {
			continue
		}
		if _, err = e.remote.Patch(ctx, s.ID, map[string]any{"trashed": true}, "", ""); err != nil {
			return err
		}
	}
	// Unreferenced objects use Drive trash, never permanent deletion. Unknown
	// items and objects from other vaults are not touched.
	candidates, err := e.remote.List(ctx, "'"+e.state.Layout.Objects+"' in parents and trashed = false")
	if err != nil {
		return err
	}
	for _, f := range candidates {
		if objects[f.ID] || f.Properties["vault"] != e.state.Vault || f.Properties["role"] != "object" {
			continue
		}
		if _, err = e.remote.Patch(ctx, f.ID, map[string]any{"trashed": true}, "", ""); err != nil {
			return err
		}
	}
	return nil
}
