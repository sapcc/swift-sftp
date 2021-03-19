package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"github.com/sirupsen/logrus"
)

const (
	Delimiter = "/" // Delimiter is used to split object names.
)

// SwiftFS implements sftp.Handlers interface.
type SwiftFS struct {
	log *logrus.Entry

	lock         sync.Mutex
	swift        *Swift
	waitReadings []*SwiftFile
	waitWritings []*SwiftFile
}

func NewSwiftFS(s *Swift) *SwiftFS {
	fs := &SwiftFS{
		log:   log,
		swift: s,
	}

	return fs
}

func (fs *SwiftFS) SetLogger(clog *logrus.Entry) {
	fs.log = clog
}

func (fs *SwiftFS) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	fs.lock.Lock()
	defer fs.lock.Unlock()

	f, err := fs.lookup(r.Filepath)
	if err != nil || f == nil {
		fs.log.Infof("%s %s", r.Method, r.Filepath)

		fs.log.Warnf("%s %s", r.Filepath, err.Error())
		return nil, sftp.ErrSshFxFailure

	} else if f == nil {
		fs.log.Infof("%s %s", r.Method, r.Filepath)

		err = fmt.Errorf("File not found. [%s]", r.Filepath)
		fs.log.Warnf("%s %s", r.Filepath, err.Error())
		return nil, sftp.ErrSshFxFailure
	}

	fs.log.Infof("%s %s (size=%d)", r.Method, r.Filepath, f.Size())

	reader := &swiftReader{
		log:     fs.log,
		swift:   fs.swift,
		sf:      f,
		timeout: time.Duration(fs.swift.config.SwiftTimeout) * time.Second,

		afterClosed: func(r *swiftReader) {
			if r.downloadErr != nil {
				fs.log.Infof("Faild to transfer '%s' [%s]", f.Name(), r.downloadErr)
			} else {
				fs.log.Infof("'%s' was successfully transferred", f.Name())
			}
		},
	}

	if err = reader.Begin(); err != nil {
		reader.Close()

		fs.log.Warnf("%s %s", r.Filepath, err.Error())
		return nil, sftp.ErrSshFxFailure
	}

	fs.log.Infof("Transferring %s ...", r.Filepath)

	return reader, nil
}

func (fs *SwiftFS) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	fs.lock.Lock()
	defer fs.lock.Unlock()

	fs.log.Infof("%s %s", r.Method, r.Filepath)

	f := &SwiftFile{
		name:    r.Filepath[1:], // strip slash
		size:    0,
		modtime: time.Now(),
		symlink: "",
	}

	writer := &swiftWriter{
		log:     fs.log,
		swift:   fs.swift,
		sf:      f,
		timeout: time.Duration(fs.swift.config.SwiftTimeout) * time.Second,
		afterClosed: func(w *swiftWriter) {
			if w.uploadErr != nil {
				fs.log.Infof("Failed to transfer '%s' [%s]", f.Name(), w.uploadErr)
			} else {
				fs.log.Infof("'%s' was successfully transferred", f.Name())
			}
		},
	}

	if err := writer.Begin(); err != nil {
		writer.Close()

		fs.log.Warnf("%s %s", r.Filepath, err.Error())
		return nil, sftp.ErrSshFxFailure
	}

	fs.log.Infof("Transferring %s ...", r.Filepath)

	return writer, nil
}

func (fs *SwiftFS) Filecmd(r *sftp.Request) error {
	fs.lock.Lock()
	defer fs.lock.Unlock()

	if r.Target != "" {
		fs.log.Infof("%s %s %s", r.Method, r.Filepath, r.Target)
	} else {
		fs.log.Infof("%s %s", r.Method, r.Filepath)
	}

	switch r.Method {
	case "Rename":
		f, err := fs.lookup(r.Filepath)
		if err != nil {
			fs.log.Warnf("%s %s", r.Filepath, err.Error())
			return sftp.ErrSshFxNoSuchFile
		}

		tf := SwiftFile{
			name: r.Target,
		}
		target := &SwiftFile{
			name:    tf.Name(),
			size:    0,
			modtime: time.Now(),
			symlink: "",
		}

		return fs.swift.Rename(f.Name(), target.Name())

	case "Remove":
		f, err := fs.lookup(r.Filepath)
		if err != nil {
			fs.log.Warnf("%s %s", r.Filepath, err.Error())
			return sftp.ErrSshFxNoSuchFile
		}

		err = fs.swift.Delete(f.Abs())
		if err != nil {
			fs.log.Warnf("%s %s", r.Filepath, err.Error())
			return sftp.ErrSshFxFailure
		}

	case "Mkdir":
		fs.log.Infof("Creating directory %s ...", r.Filepath)
		if err := fs.swift.CreateDirectory(r.Filepath[1:] + "/"); err != nil {
			fs.log.Warnf("%s %s", r.Filepath, err.Error())
			return sftp.ErrSshFxFailure
		}

	default:
		fs.log.Warnf("Unsupported operation (method=%s, target=%s)", r.Method, r.Target)
		return sftp.ErrSshFxOpUnsupported
	}
	return nil
}

func (fs *SwiftFS) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	fs.lock.Lock()
	defer fs.lock.Unlock()

	fs.log.Infof("%s %s", r.Method, r.Filepath)

	switch r.Method {
	case "List":
		files, err := fs.swift.ListDirectory(fs.filepath2object(r.Filepath))
		if err != nil {
			fs.log.Warnf("%s %s", r.Filepath, err.Error())
			return nil, sftp.ErrSshFxFailure
		}
		ret := fs.swift.GetFileInfos(files)
		return listerat(ret), nil
	case "Stat":
		// root path is not on the object storage and return it manually.
		if r.Filepath == "/" {
			fakeRoot := []os.FileInfo{
				&SwiftFile{
					name:    "/",
					modtime: time.Now(),
				},
			}
			return listerat(fakeRoot), nil
		}

		// Check for xyz and xyz/
		subdir := fs.filepath2object(path.Dir(r.Filepath))
		files, err := fs.swift.ListDirectory(subdir)
		if err != nil {
			fs.log.Warnf("%s %s", subdir, err.Error())
			return nil, sftp.ErrSshFxFailure
		}

		for _, f := range files {
			fileInfo := fs.swift.GetFileInfo(f)
			if r.Filepath == fileInfo.Abs() {
				return listerat([]os.FileInfo{fileInfo}), nil
			}
		}
		return nil, os.ErrNotExist
	}

	return nil, sftp.ErrSshFxFailure
}

func (fs *SwiftFS) filepath2object(path string) string {
	return strings.TrimPrefix(path, "/")
}
func (fs *SwiftFS) object2filepath(name string) string {
	return Delimiter + name
}

// Return SwiftFile object with the path
func (fs *SwiftFS) lookup(path string) (*SwiftFile, error) {
	// root path is not on the object storage and return it manually.
	if path == "/" {
		f := &SwiftFile{
			name:    "/",
			modtime: time.Now(),
		}
		return f, nil
	}

	name := fs.filepath2object(path)
	header, err := fs.swift.Get(name)
	if err != nil {
		return nil, err
	}

	f := &SwiftFile{
		name:    name,
		size:    int64(header.SizeBytes().Get()),
		modtime: header.UpdatedAt().Get(),
		symlink: "",
	}
	return f, nil

}

// To synchronize objects on object storage and fs.files
func (fs *SwiftFS) allFiles() ([]*SwiftFile, error) {
	fs.log.Debugf("Updating file list...")

	// Get object list from object storage
	objs, err := fs.swift.List()
	if err != nil {
		return nil, err
	}

	files := make([]*SwiftFile, len(objs))
	for i, obj := range objs {
		isDir := strings.HasSuffix(obj.Name(), "/")
		var objName string
		if isDir {
			objName = filepath.Clean(obj.Name())
		} else {
			objName = obj.Name()
		}
		headers, err := obj.Headers()
		if err != nil {
			return nil, err
		}
		files[i] = &SwiftFile{
			name:    objName,
			size:    int64(headers.SizeBytes().Get()),
			modtime: headers.UpdatedAt().Get(),
		}
	}
	return files, nil
}

// Modeled after strings.Reader's ReadAt() implementation
type listerat []os.FileInfo

func (f listerat) ListAt(ls []os.FileInfo, offset int64) (int, error) {
	var n int
	if offset >= int64(len(f)) {
		return 0, io.EOF
	}
	n = copy(ls, f[offset:])
	if n < len(ls) {
		return n, io.EOF
	}
	return n, nil
}
