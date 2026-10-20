// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

// VerifyRemote reads every referenced object back from Drive. Local upload
// receipts alone are not sufficient evidence for migration.
func (e *Engine) VerifyRemote(ctx context.Context) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkLayout(ctx); err != nil {
		return 0, err
	}
	if e.state.Pending != nil || e.state.Current.ID == "" {
		return 0, fmt.Errorf("no complete snapshot to verify")
	}
	if err := e.verifyArchiveEvents(ctx); err != nil {
		return 0, err
	}
	snapshot, err := e.readSnapshot(ctx, e.state.Current.ID)
	if err != nil {
		return 0, err
	}
	for _, obj := range snapshot.Objects {
		r, err := e.remote.Download(ctx, obj.ID)
		if err != nil {
			return 0, err
		}
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(r, obj.Size+1))
		r.Close()
		if err != nil {
			return 0, err
		}
		if n != obj.Size || hex.EncodeToString(h.Sum(nil)) != obj.SHA256 {
			return 0, fmt.Errorf("remote object checksum mismatch")
		}
	}
	return len(snapshot.Documents), nil
}
