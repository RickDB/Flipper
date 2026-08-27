// Package version exposes the Flipper build version. The checked-in VERSION
// file is the fallback for local/dev builds; container builds override it
// with -ldflags -X.
package version

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var source string

// Value may be overridden at build time, e.g.:
//
//	-ldflags="-X github.com/RickDB/Flipper/internal/version.Value=0.02"
var Value string

// Current returns the running Flipper version, always prefixed with "v".
func Current() string {
	v := strings.TrimSpace(Value)
	if v == "" {
		v = strings.TrimSpace(source)
	}
	if v == "" {
		return "dev"
	}
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}
