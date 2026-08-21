//go:build !unix

package editorintelligence

import "io/fs"

// fileUID answers nothing where files carry no unix owner; without one no
// cache ever reads as foreign, so the migration stays quiet.
func fileUID(_ fs.FileInfo) (int, bool) { return 0, false }
