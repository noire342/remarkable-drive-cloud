//go:build ci || cloud

// SPDX-License-Identifier: AGPL-3.0-or-later
package rmfakecloud

import "testing/fstest"

// The tablet build uses native QML. The inherited web application is disabled.
var Assets = fstest.MapFS{"dist/index.html": &fstest.MapFile{Data: []byte("Native tablet configuration only")}}
