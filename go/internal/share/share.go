// Package share holds the files every backend ships: the review page, the
// agent skills and the standards, copied here by gen and embedded.
package share

import (
	"embed"
	"io/fs"
)

//go:generate go run ./gen

//go:embed all:files
var files embed.FS

// FS is the shared files, rooted at the repository layout: ux/html,
// ux/css, ux/js, skills and standards.
var FS = func() fs.FS {
	sub, err := fs.Sub(files, "files")
	if err != nil {
		panic(err)
	}
	return sub
}()
