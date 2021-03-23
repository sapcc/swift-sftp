package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/majewsky/schwift"
	"github.com/majewsky/schwift/gopherschwift"
)

type Swift struct {
	config     Config
	container  string
	authClient *gophercloud.ProviderClient

	// Need to be exported
	SchwiftClient *schwift.Account
}

func NewSwift(c Config) *Swift {
	return &Swift{
		config: c,
	}
}

func (s *Swift) Init() (err error) {
	if err = s.initializeAuthClient(); err != nil {
		return err
	}

	swiftClient, err := s.getObjectStorageClient()
	if err != nil {
		return err
	}

	s.SchwiftClient, err = gopherschwift.Wrap(swiftClient, nil)
	if err != nil {
		return err
	}

	return nil
}

func (s *Swift) setContainer(container string) {
	s.container = container
}


func (s *Swift) getContainer() *schwift.Container {
	return s.SchwiftClient.Container(s.container)
}

func (s *Swift) ExistsContainer() (exists bool, err error) {
	return s.getContainer().Exists()
}

func (s *Swift) CreateContainer() (err error) {
	return s.getContainer().Create(nil)
}

func (s *Swift) GetObject(path string) *schwift.Object {
	return s.getContainer().Object(path)
}

func (s *Swift) CreateDirectory(path string) (err error) {
	obj := s.getContainer().Object(path)
	hdr := schwift.NewObjectHeaders()
	hdr.ContentType().Set("application/directory")
	opts := hdr.ToOpts() //type *schwift.RequestOptions

	buffer := ""
	return obj.Upload(bytes.NewReader([]byte(buffer)), nil, opts)
}

func (fs *Swift) GetFileInfo(f schwift.ObjectInfo) *SwiftFile {
	var name string
	if f.Object == nil {
		name = "/" + f.SubDirectory
	} else {
		name = "/" + f.Object.Name()
	}

	file := &SwiftFile{
		name:    name,
		size:    int64(f.SizeBytes),
		modtime: f.LastModified,
		symlink: "",
	}
	return file
}

func (fs *Swift) GetFileInfos(files []schwift.ObjectInfo) []os.FileInfo {
	list := make([]os.FileInfo, 0, len(files))
	for _, f := range files {
		list = append(list, fs.GetFileInfo(f))
	}
	return list
}

func (s *Swift) ListDirectory(path string) ([]schwift.ObjectInfo, error) {
	iter := s.getContainer().Objects()
	if path != "" {
		// Directories in swift end always with /
		if !strings.HasSuffix(path, "/") {
			path += "/"
		}
		iter.Prefix = path
	}
	iter.Delimiter = "/"
	ls := make([]schwift.ObjectInfo, 0)
	err := iter.ForeachDetailed(func(oi schwift.ObjectInfo) error {
		if oi.Object == nil || oi.Object.Name() != path {
			ls = append(ls, oi)
		}
		return nil
	})
	return ls, err
}

func (s *Swift) List() ([]*schwift.Object, error) {
	return s.getContainer().Objects().Collect()
}

func (s *Swift) Get(name string) (schwift.ObjectHeaders, error) {
	return s.getContainer().Object(name).Headers()
}

func (s *Swift) Download(name string) (content io.ReadCloser, size int64, err error) {
	o := s.getContainer().Object(name)
	hs, err := o.Headers()
	if err != nil {
		return nil, 0, err
	}

	rs, err := o.Download(nil).AsReadCloser()
	if err != nil {
		return nil, 0, err
	}

	return rs, int64(hs.SizeBytes().Get()), nil
}

func (s *Swift) Put(name string, content io.Reader) error {
	return s.getContainer().Object(name).Upload(content, nil, nil)
}

func (s *Swift) Delete(name string) error {
	return s.getContainer().Object(name).Delete(nil, nil)
}

func (s *Swift) Rename(oldName, newName string) error {
	dest := fmt.Sprintf("%s%s%s", s.container, Delimiter, newName)
	oldObject := s.getContainer().Object(oldName)
	newObject := s.getContainer().Object(dest)

	if err := oldObject.CopyTo(newObject, nil, nil); err != nil {
		return err
	}
	return s.Delete(oldName)
}

func (s *Swift) getObjectStorageClient() (*gophercloud.ServiceClient, error) {
	if s.authClient == nil {
		return nil, errors.New("Auth client must be initialized in advance")
	}

	opts := gophercloud.EndpointOpts{}
	if s.config.OsRegion != "" {
		opts.Region = s.config.OsRegion
	}

	return openstack.NewObjectStorageV1(s.authClient, opts)
}

func (s *Swift) initializeAuthClient() error {
	var (
		err  error
		opts gophercloud.AuthOptions
	)

	if (s.config.OsUsername != "") && s.config.OsPassword != "" {
		opts = gophercloud.AuthOptions{
			IdentityEndpoint: s.config.OsIdentityEndpoint,
			Username:         s.config.OsUsername,
			Password:         s.config.OsPassword,
			DomainName:       s.config.OsUserDomainName,
			Scope: &gophercloud.AuthScope{
				ProjectName: s.config.OsProjectName,
				DomainName:  s.config.OsProjectDomainName,
			},

			AllowReauth: true,
		}

	} else if opts, err = openstack.AuthOptionsFromEnv(); err != nil {
		return err
	}

	s.authClient, err = openstack.AuthenticatedClient(opts)
	return err
}
