package webassets

import (
	"embed"
	"io/fs"
)

// The all: prefix keeps a tracked .gitkeep in the pattern set so a fresh
// worktree can compile tests before web assets are built. Real releases
// replace the directory with web/dist.
//go:embed all:dist
var content embed.FS

func FS() fs.FS {
	result, err := fs.Sub(content, "dist")
	if err != nil {
		panic(err)
	}
	return result
}
