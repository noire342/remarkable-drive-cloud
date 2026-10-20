//go:build !ci && !cloud

package rmfakecloud

import "embed"

//go:embed dist/*
var Assets embed.FS
