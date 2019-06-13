package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SwiftFile implements os.FileInfo interfaces.
// There interfaces are necessary for sftp.Handlers.
type SwiftFile struct {
	name    string
	size    int64
	modtime time.Time
	symlink string

	tmpFile *os.File
}

func newSwiftFile(name string, isdir bool) *SwiftFile {
	return &SwiftFile{
		name:    name,
		modtime: time.Now(),
	}
}

// io.Fileinfo interface
func (f *SwiftFile) Name() string {
	return strings.TrimSuffix(filepath.Base(f.name), "/")
}

func (f *SwiftFile) Abs() string {
	return filepath.Join(f.DirName(), f.Name())
}

func (f *SwiftFile) DirName() string {
	return filepath.Dir(strings.TrimSuffix(f.name, "/"))
}

func (f *SwiftFile) Dir() string {
	if strings.HasSuffix(f.name, Delimiter) {
		// f.name is directory name
		return f.name

	} else if !strings.Contains(f.name, Delimiter) {
		// f.objectname is the file on root file path
		return Delimiter

	} else {
		pos := strings.LastIndex(f.name, Delimiter)
		return f.name[:pos+1]
	}
}

func (f *SwiftFile) Size() int64 {
	return f.size
}

func (f *SwiftFile) Mode() os.FileMode {
	ret := os.FileMode(0644)
	if f.IsDir() {
		ret = os.FileMode(0755) | os.ModeDir
	}
	if f.symlink != "" {
		ret = os.FileMode(0777) | os.ModeSymlink
	}
	return ret
}

func (f *SwiftFile) ModTime() time.Time {
	return f.modtime
}

func (f *SwiftFile) IsDir() bool {
	return strings.HasSuffix(f.name, "/")
}

func (f *SwiftFile) Sys() interface{} {
	return dummyStat()
}
