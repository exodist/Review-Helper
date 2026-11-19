// Command gen copies the repository's shared files (the page, the skills and
// the standards) into files/ beside this package, so share.go can embed them:
// go:embed reads no symlinks and nothing outside the module. The copies are
// gitignored and remade on every run (go generate, or make go).
package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
)

// What the binary serves or ships, relative to the repository root.
var shared = []string{"ux/html", "ux/css", "ux/js", "skills", "standards"}

func main() {
	// It removes ./files, so it runs only beside share.go, as go generate
	// runs it.
	if _, err := os.Stat("share.go"); err != nil {
		log.Fatal("gen: run it in internal/share (go generate ./...): ", err)
	}
	root := filepath.Join("..", "..", "..")
	if err := os.RemoveAll("files"); err != nil {
		log.Fatal(err)
	}
	for _, dir := range shared {
		if err := copyTree(filepath.Join(root, dir), filepath.Join("files", dir)); err != nil {
			log.Fatal(err)
		}
	}
}

func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		return copyFile(path, dest)
	})
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
