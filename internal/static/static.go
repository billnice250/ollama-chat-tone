package static

import (
	"embed"
	"io/fs"
)

//go:embed web
var files embed.FS

func Files() (fs.FS, error) {
	return fs.Sub(files, "web")
}
