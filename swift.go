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
	"github.com/gophercloud/gophercloud/openstack/objectstorage/v1/containers"
	"github.com/gophercloud/gophercloud/openstack/objectstorage/v1/objects"
	"github.com/gophercloud/gophercloud/pagination"
	"github.com/majewsky/schwift"
	"github.com/majewsky/schwift/gopherschwift"
)

type Swift struct {
	config     Config
	authClient *gophercloud.ProviderClient

	// Need to be exported
	SwiftClient   *gophercloud.ServiceClient
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

	s.SwiftClient, err = s.getObjectStorageClient()
	if err != nil {
		return err
	}

	s.SchwiftClient, err = gopherschwift.Wrap(s.SwiftClient, nil)
	if err != nil {
		return err
	}

	return nil
}

func (s *Swift) ListContainer() (list []containers.Container, err error) {
	opts := containers.ListOpts{
		Full: true,
	}

	list = make([]containers.Container, 0, 10)
	containers.List(s.SwiftClient, opts).EachPage(func(page pagination.Page) (bool, error) {
		cs, err := containers.ExtractInfo(page)
		if err != nil {
			return false, err
		}
		list = append(list, cs...)
		return true, nil
	})

	return list, nil
}

func (s *Swift) getContainer() *schwift.Container {
	return s.SchwiftClient.Container(s.config.Container)
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

func (s *Swift) DeleteContainer() (err error) {
	ls, err := s.List()
	if err != nil {
		return err
	}

	// Recursive deletion for all objects in the container
	for _, obj := range ls {
		drs := objects.Delete(s.SwiftClient, s.config.Container, obj.Name, objects.DeleteOpts{})
		if drs.Err != nil {
			return drs.Err
		}
	}

	rs := containers.Delete(s.SwiftClient, s.config.Container)
	return rs.Err
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

func (s *Swift) List() (ls []objects.Object, err error) {
	ls = make([]objects.Object, 0, 10)
	err = objects.List(s.SwiftClient, s.config.Container, objects.ListOpts{
		Full: true,
	}).EachPage(func(p pagination.Page) (bool, error) {
		ls, err = objects.ExtractInfo(p)
		if err != nil {
			return false, err
		}
		return true, nil
	})

	return ls, err
}

func (s *Swift) Get(name string) (header *objects.GetHeader, err error) {
	return objects.Get(s.SwiftClient, s.config.Container, name, objects.GetOpts{}).Extract()
}

func (s *Swift) Download(name string) (content io.ReadCloser, size int64, err error) {
	rs := objects.Download(s.SwiftClient, s.config.Container, name, objects.DownloadOpts{})
	if rs.Err != nil {
		return nil, 0, rs.Err
	}

	info, err := rs.Extract()
	if err != nil {
		return nil, 0, err
	}

	return rs.Body, info.ContentLength, nil
}

func (s *Swift) Put(name string, content io.Reader) error {
	// temporary object name
	tmpname := "tmp_" + name

	// delete a temporary file from container
	defer func() {
		objects.Delete(s.SwiftClient, s.config.Container, tmpname, objects.DeleteOpts{})
	}()

	cOpts := objects.CreateOpts{
		Content: content,
	}
	rCreate := objects.Create(s.SwiftClient, s.config.Container, tmpname, cOpts)
	if rCreate.Err != nil {
		return rCreate.Err
	}

	dest := fmt.Sprintf("%s%s%s", s.config.Container, Delimiter, name)
	rCopy := objects.Copy(s.SwiftClient, s.config.Container, tmpname, objects.CopyOpts{
		Destination: dest,
	})
	if rCopy.Err != nil {
		return rCopy.Err
	}

	return nil
}

func (s *Swift) Delete(name string) (err error) {
	return s.getContainer().Object(name).Delete(nil, nil)
}

func (s *Swift) Rename(oldName, newName string) (err error) {
	dest := fmt.Sprintf("%s%s%s", s.config.Container, Delimiter, newName)
	rCopy := objects.Copy(s.SwiftClient, s.config.Container, oldName, objects.CopyOpts{
		Destination: dest,
	})
	if rCopy.Err != nil {
		return rCopy.Err
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
