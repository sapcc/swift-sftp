package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"syscall"

	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"golang.org/x/crypto/ssh/terminal"
)

func main() {
	app := cli.NewApp()
	app.Name = "conoha transfer for SFTP"
	app.Commands = []cli.Command{
		cli.Command{
			Name:      "server",
			ShortName: "s",
			Usage:     "Start sftp server",
			Flags: []cli.Flag{
				cli.BoolFlag{
					Name:  "debug,d",
					Usage: "Enable debug output",
				},
				cli.StringFlag{
					Name:  "container,c",
					Usage: "Container name",
				},
				cli.StringFlag{
					Name:  "source-address,a",
					Usage: "Source address of connection",
					Value: "127.0.0.1",
				},
				cli.IntFlag{
					Name:  "port,p",
					Usage: "Port to listen",
					Value: 10022,
				},
				cli.StringFlag{
					Name:  "password-file",
					Usage: "Path of password-file. If provided, password authentication is enabled",
					Value: "",
				},
				cli.StringFlag{
					Name:  "authorized-keys,k",
					Usage: "Path of authorized_keys file",
					Value: "~/.ssh/authorized_keys",
				},
			},
			Action: server,
		},

		cli.Command{
			Name:      "gen-password",
			ShortName: "p",
			Usage:     "Generate password",
			Action:    genPassword,
			ArgsUsage: "[username]",
			Flags: []cli.Flag{
				cli.BoolFlag{
					Name:  "format,f",
					Usage: "Output in password-file format. (If not provided, print only password)",
				},
			},
		},

		cli.Command{
			Name:   "test",
			Usage:  "test run",
			Action: test,
		},
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		os.Exit(1)
	}
}

func server(c *cli.Context) (err error) {
	if c.Bool("debug") {
		enableDebugTransport()
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetFormatter(&OriginalFormatter{})
	}

	conf := Config{}
	if err = conf.Init(c); err != nil {
		return err
	}
	conf.Container = c.String("container")

	log.Infof("Starting SFTP server...")

	return StartServer(conf)
}

func genPassword(c *cli.Context) (err error) {
	if c.NArg() != 1 {
		return errors.New("Parameter 'username' required")
	}
	username := c.Args()[0]

	fmt.Fprintf(os.Stdout, "Password: ")
	password, err := terminal.ReadPassword(int(syscall.Stdin))
	if err != nil {
		return err
	} else if len(password) == 0 {
		return nil
	}
	fmt.Println()

	hashed := GenerateHashedPassword(username, password)
	if c.Bool("format") {
		fmt.Fprintf(os.Stdout, "%s:%s", username, hashed)
	} else {
		fmt.Fprintf(os.Stdout, "%s", hashed)
	}
	fmt.Println()
	return nil
}

func test(c *cli.Context) (err error) {
	b := sha256.Sum256([]byte("hiro123"))
	hashed := make([]byte, len(b)*2)
	n := hex.Encode(hashed, b[:])

	return nil
}
