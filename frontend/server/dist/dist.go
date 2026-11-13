// Package dist is the console's static export, embedded into the server.
//
// The image build copies `next build`'s out/ here before compiling; in a
// checkout the directory holds only .gitkeep, and the server refuses to start
// on it.
package dist

import (
	"embed"
	"io/fs"
)

//go:embed all:out
var files embed.FS

// Root is the directory the embedded files are kept under.
const Root = "out"

// Files is the export, rooted at its own top.
func Files() (fs.FS, error) { return fs.Sub(files, Root) }
