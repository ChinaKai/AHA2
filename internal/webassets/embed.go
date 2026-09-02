package webassets

import (
	"embed"
	"io/fs"
)

//go:embed dist/*
var content embed.FS

func FS() fs.FS {
	result, err := fs.Sub(content, "dist")
	if err != nil {
		panic(err)
	}
	return result
}
