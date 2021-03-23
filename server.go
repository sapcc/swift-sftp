package main

import (
	"bufio"
	"bytes"
	"crypto/subtle"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
)

func StartServer(conf Config) error {
	// Prepare server config and client
	sConf, err := initServer(conf)
	if err != nil {
		return err
	}

	// swift
	swift := NewSwift(conf)
	if err = swift.Init(); err != nil {
		return err
	}
	log.Infof("Use Swift backend '%s'", swift.SchwiftClient.Backend().EndpointURL())

	// Start server
	listener, err := net.Listen("tcp", conf.BindAddress)
	if err != nil {
		return err
	}
	log.Infof("Listen: %s", conf.BindAddress)

	for {
		nConn, err := listener.Accept()
		if err != nil {
			return err
		}

		var addr string
		var port string
		tmp := strings.Split(nConn.RemoteAddr().String(), ":")
		if len(tmp) == 2 {
			// IPv4
			addr = tmp[0]
			port = tmp[1]
		} else {
			// IPv6?
			tmp := strings.Split(nConn.RemoteAddr().String(), "]:")
			if len(tmp) == 2 {
				// IPv4
				addr = tmp[0][1:]
				port = tmp[1]
			} else {
				addr = nConn.RemoteAddr().String()
				port = "-"
			}
		}

		log.Infof("Connect from %s port %s", addr, port)
		go func() {
			defer func() {
				log.Infof("Disconnect from %s port %s", addr, port)
			}()

			err := handleClient(conf, sConf, swift, nConn)
			if err == nil || err == io.EOF {
				return
			}

			serr, ok := err.(*ssh.ServerAuthError)
			if !ok {
				log.Warnf("Auth: %s from %s port %s", err, addr, port)
				return
			}

			for _, err = range serr.Errors {
				log.Warnf("Auth: %s from %s port %s", err, addr, port)
			}
		}()
	}
}

func initServer(conf Config) (sConf *ssh.ServerConfig, err error) {
	sConf = &ssh.ServerConfig{
		PublicKeyCallback: authPkey(conf),
	}

	if os.Getenv("USERNAME") != "" && os.Getenv("PASSWORD") != "" {
		sConf.PasswordCallback = envAuthPassword()
	}
	// Add password authentication method if password file exists
	s, err := os.Stat(conf.PasswordFilePath)
	if err == nil && !s.IsDir() {
		sConf.PasswordCallback = authPassword(conf)
	}

	// host private key
	pkeyBytes, err := ioutil.ReadFile(conf.ServerKeyPath)
	if err != nil {
		return nil, err
	}

	pkey, err := ssh.ParsePrivateKey(pkeyBytes)
	if err != nil {
		return nil, err
	}
	sConf.AddHostKey(pkey)

	return sConf, nil
}

func authPkey(conf Config) func(c ssh.ConnMetadata, pkey ssh.PublicKey) (*ssh.Permissions, error) {
	return func(c ssh.ConnMetadata, pkey ssh.PublicKey) (*ssh.Permissions, error) {
		authorizedKeysBytes, err := ioutil.ReadFile(conf.AuthorizedKeysPath)
		if err != nil {
			return nil, err
		}

		authorizedKeysMap := map[string]bool{}
		for len(authorizedKeysBytes) > 0 {
			pubKey, _, _, rest, err := ssh.ParseAuthorizedKey(authorizedKeysBytes)
			if err != nil {
				return nil, err
			}

			authorizedKeysMap[string(pubKey.Marshal())] = true
			authorizedKeysBytes = rest
		}

		if authorizedKeysMap[string(pkey.Marshal())] {
			return &ssh.Permissions{
				// Record the public key used for authentication.
				Extensions: map[string]string{
					"pubkey-fp": ssh.FingerprintSHA256(pkey),
				},
			}, nil
		}
		return nil, fmt.Errorf("unknown public key for %q", c.User())
	}
}

func envAuthPassword() func(c ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {

	return func(c ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		listUser := os.Getenv("USERNAME")
		listPass := os.Getenv("PASSWORD")
		listContainer := os.Getenv("CONTAINER")

		if listContainer == "" {
			return nil, fmt.Errorf("$CONTAINER env not specificed")
		}
		userMatch := strings.Compare(listUser, c.User())
		if subtle.ConstantTimeCompare([]byte(listPass), password) == 1 && userMatch == 0 {
			// authorized
			return &ssh.Permissions{Extensions: map[string]string{"swift-sftp-container": listContainer}}, nil
		}

		return nil, fmt.Errorf("password rejected for %q", c.User())
	}
}

func authPassword(conf Config) func(c ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {

	return func(c ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {

		f, err := os.Open(conf.PasswordFilePath)
		if err != nil {
			return nil, err
		}
		defer f.Close()

		r := bufio.NewReader(f)

		for {
			line, _, err := r.ReadLine()
			if err == io.EOF {
				break
			} else if err != nil {
				return nil, err
			}


			parts := bytes.SplitN(line, []byte(":"), 3)
			if len(parts) != 3 {
				return nil, fmt.Errorf("failed parsing config file")
			}
			listUser := parts[0]
			listContainer := parts[1]
			listPass := parts[2]

			pwMatch := subtle.ConstantTimeCompare(listPass, password)
			if subtle.ConstantTimeCompare(listUser, []byte(c.User())) == 1 && pwMatch == 1 {
				// authorized
				return &ssh.Permissions{Extensions: map[string]string{"swift-sftp-container": string(listContainer)}}, nil
			}
		}

		return nil, fmt.Errorf("password rejected for %q", c.User())
	}
}

func handleClient(conf Config, sConf *ssh.ServerConfig, swift *Swift, nConn net.Conn) error {
	conn, chans, reqs, err := ssh.NewServerConn(nConn, sConf)
	if err != nil {
		return err
	}

	// create client
	client := &Client{
		SessionID:  fmt.Sprintf("%x", conn.SessionID()),
		Username:   conn.User(),
		RemoteAddr: conn.RemoteAddr(),
		StartedAt:  time.Now(),
	}

	// logger with client
	clog := log.WithFields(logrus.Fields{
		"client": client,
	})

	container := conn.Permissions.Extensions["swift-sftp-container"]

	swift.setContainer(container)
	exists, err := swift.ExistsContainer()
	if err != nil {
		return err
	}

	if !exists {
		if conf.CreateContainerIfNotExists {
			if err = swift.CreateContainer(); err != nil {
				return fmt.Errorf("Couldn't create container. [%s]", err)
			}
			log.Infof("Create container '%s'", container)

		} else {
			return fmt.Errorf("Container '%s' does not exist.", container)
		}
	}

	clog.Infof("Session %s@%s opened for %s%s", client.Username, client.RemoteAddr,
		swift.SchwiftClient.Backend().EndpointURL(), container)

	go ssh.DiscardRequests(reqs)

	for nchan := range chans {
		if nchan.ChannelType() != "session" {
			msg := fmt.Sprintf("The request was rejected because of unknown channel type. [%s]", nchan.ChannelType())
			clog.Warn(msg)
			nchan.Reject(ssh.UnknownChannelType, msg)
			continue
		}
		clog.Debugf("Channel is accepted[type=%s]", nchan.ChannelType())

		channel, requests, err := nchan.Accept()
		if err != nil {
			return err
		}

		go func(in <-chan *ssh.Request) {
			for req := range in {
				clog.Debugf("Handling request [type=%s]", req.Type)

				// We only handle the request that has type of "subsystem".
				ok := false
				if req.Type == "subsystem" && string(req.Payload[4:]) == "sftp" {
					ok = true
				}
				req.Reply(ok, nil)
			}
		}(requests)

		// sftp
		if err = StartSftpSession(swift, channel, client); err != nil {
			return err
		}
	}

	clog.Infof("Session closed for %s@%s", client.Username, client.RemoteAddr)

	return nil
}
