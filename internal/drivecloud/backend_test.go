// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"io"
	"testing"
)

type forbiddenRead struct{ t *testing.T }

func (r forbiddenRead) Read([]byte) (int, error) {
	r.t.Fatal("read-only probe consumed a native write")
	return 0, io.EOF
}

func TestReadOnlyNativeProbeRejectsEveryWriteWithoutEngineOrRemote(t *testing.T) {
	var engine *Engine
	backend := engine.NativeBackend(true)
	for _, id := range []string{"root", "some-object", ""} {
		if _, err := backend.StoreBlob("tablet", id, forbiddenRead{t}, 0); err == nil {
			t.Fatalf("read-only probe accepted %q", id)
		}
	}
}
