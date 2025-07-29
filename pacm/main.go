package pacm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/urfave/cli/v2"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

type PackageConfig struct {
	Name    string   `json:"name" yaml:"name"`
	Ver     string   `json:"ver" yaml:"ver"`
	Targets []Target `json:"targets" yaml:"targets"`
	Packets []Packet `json:"packets,omitempty" yaml:"packets,omitempty"`
}

type Target struct {
	Path    string `json:"path" yaml:"path"`
	Exclude string `json:"exclude,omitempty" yaml:"exclude,omitempty"`
}

// custom unmarshall prepare string and struct types of target
func (t *Target) UnmarshalJSON(data []byte) error {
	type t1 Target
	var tmp t1
	if data[0] == 34 {
		err := json.Unmarshal(data, &t.Path)
		if err != nil {
			return errors.New("CustomFloat64: UnmarshalJSON: " + err.Error())
		}
	} else {
		err := json.Unmarshal(data, &tmp)
		if err != nil {
			return errors.New("CustomFloat64: UnmarshalJSON: " + err.Error())
		}
		*t = Target(tmp)
	}
	return nil
}

type Packet struct {
	Name string `json:"name" yaml:"name"`
	Ver  string `json:"ver" yaml:"ver"`
}

type PackagesConfig struct {
	Packages []Packet `json:"packages" yaml:"packages"`
}

type PackageManager struct {
	sshConfig *ssh.ClientConfig
	server    string
}

func NewPackageManager(server, user, keyPath string) (*PackageManager, error) {
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read SSH key: %v", err)
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		if _, ok := err.(*ssh.PassphraseMissingError); ok {
			// missing passphrase for protected SSH key
			var passphrase []byte
			pass, ok := os.LookupEnv("PACMAN_SSH_KEY_PASS")

			if !ok {
				fmt.Print("Enter passphrase for SSH key: ")
				passphrase, err = term.ReadPassword(int(os.Stdin.Fd()))
				if err != nil {
					return nil, fmt.Errorf("failed to read passphrase: %v", err)
				}

				fmt.Println()
			} else {
				passphrase = []byte(pass)
			}
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, passphrase)
			if err != nil {
				return nil, fmt.Errorf("failed to parse SSH key with passphrase: %v", err)
			}
		} else {
			return nil, fmt.Errorf("failed to parse SSH key: %v", err)
		}
	}

	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	return &PackageManager{
		sshConfig: config,
		server:    server,
	}, nil
}

func Main() {
	err := godotenv.Load()
	if err != nil {
		fmt.Printf("file .env don't load: %v\n", err)
	}
	host, ok := os.LookupEnv("PACMAN_SSH_HOST")
	if !ok {
		host = "localhost"
	}
	port, ok := os.LookupEnv("PACMAN_SSH_PORT")
	if !ok {
		port = "22"
	}
	user := os.Getenv("PACMAN_SSH_USER")
	keyPath := os.Getenv("PACMAN_SSH_KEY")
	server := fmt.Sprintf("%s:%s", host, port)

	pm, err := NewPackageManager(server, user, keyPath)
	if err != nil {
		fmt.Printf("Failed to initialize package manager: %v\n", err)
		os.Exit(1)
	}

	if logLevel, ok := os.LookupEnv("PACMAN_LOG"); ok && logLevel == "debug" {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	TIMEOUT := 3 * time.Second

	app := &cli.App{
		Name: "pm",
		Commands: []*cli.Command{
			{
				Name:      "create",
				Usage:     "Create and upload a package",
				ArgsUsage: "[config-file.json(yaml)]",
				Action: func(c *cli.Context) error {
					ctx, cancel := context.WithTimeout(c.Context, TIMEOUT)
					defer cancel()
					if c.NArg() != 1 {
						return fmt.Errorf("config file path is required")
					}
					return pm.CreatePackage(ctx, c.Args().First())
				},
			},
			{
				Name:      "update",
				Usage:     "Download and unpack packages",
				ArgsUsage: "[config-file.json(yaml)]",
				Action: func(c *cli.Context) error {
					ctx, cancel := context.WithTimeout(c.Context, TIMEOUT)
					defer cancel()
					if c.NArg() != 1 {
						return fmt.Errorf("config file path is required")
					}
					return pm.UpdatePackages(ctx, c.Args().First())
				},
			},
		},
	}

	err = app.Run(os.Args)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
