// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"fmt"
	"io"
)

// NativeBackend can expose a private snapshot for real firmware download tests.
// A read-only probe rejects all writes before consuming their content and never
// publishes a root, even if the native client tries to acknowledge a change.
type NativeBackend struct {
	engine   *Engine
	readOnly bool
}

func (e *Engine) NativeBackend(readOnly bool) NativeBackend {
	return NativeBackend{engine: e, readOnly: readOnly}
}

func (b NativeBackend) StoreBlob(user, id string, reader io.Reader, generation int64) (int64, error) {
	if b.readOnly {
		return 0, fmt.Errorf("native protocol probe is read-only")
	}
	return b.engine.StoreBlob(user, id, reader, generation)
}

func (b NativeBackend) LoadBlob(user, id string) (io.ReadCloser, int64, int64, string, error) {
	return b.engine.LoadBlob(user, id)
}
