package pacm

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	scp "github.com/bramvdbogaerde/go-scp"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

func (pm *PackageManager) CreatePackage(ctx context.Context, configPath string) error {
	slog.Info("Start create package...")
	startTime := time.Now()

	select {
	case <-ctx.Done():
		return fmt.Errorf("Create package canceled: %w", ctx.Err())
	default:
	}

	// create .tar.gz file
	packName, archiveName, err := getArch(ctx, configPath)
	if err != nil {
		return fmt.Errorf("failed to create archive for upload: %w", err)
	}

	// Upload to server
	sshClient, err := ssh.Dial("tcp", pm.server, pm.sshConfig)
	if err != nil {
		return fmt.Errorf("failed to connect to SSH server: %w", err)
	}
	defer sshClient.Close()

	// Create remote directory
	remoteDir := fmt.Sprintf("%s/%s", os.Getenv("PACMAN_ROOT_DIR"), packName)

	session, err := sshClient.NewSession()
	if err != nil {
		return fmt.Errorf("can't create SSH session: %w", err)
	}
	defer session.Close()

	err = session.Run(fmt.Sprintf("mkdir -p %s", remoteDir))
	if err != nil {
		return fmt.Errorf("can't create remote dir %s on server: %w", remoteDir, err)
	}

	// Create a new SCP client, note that this function might
	// return an error, as a new SSH session is established using the existing connecton

	client, err := scp.NewClientBySSH(sshClient)
	if err != nil {
		slog.Error("Error creating new SSH session from existing connection", "error", err)
		return fmt.Errorf("error creating new SSH session from existing connection: %w", err)
	}
	defer client.Close()

	archiveData, err := os.Open(archiveName)
	if err != nil {
		return fmt.Errorf("failed to open archive for upload: %w", err)
	}
	defer archiveData.Close()

	remotePath := fmt.Sprintf("%s/%s", remoteDir, archiveName)

	err = client.CopyFromFile(ctx, *archiveData, remotePath, "0655")

	if err != nil {
		slog.Error("Error while copying file ", "error", err)
	}
	slog.Info("Finish create package", "time", time.Since(startTime))

	return nil
}

// Create compressed package file
func getArch(ctx context.Context, configPath string) (packName, archiveName string, err error) {

	configData, err := os.ReadFile(configPath)
	if err != nil {
		err = fmt.Errorf("failed to read config: %w", err)
		return
	}

	var config PackageConfig
	if strings.HasSuffix(configPath, ".yaml") || strings.HasSuffix(configPath, ".yml") {
		err = yaml.Unmarshal(configData, &config)
	} else {
		err = json.Unmarshal(configData, &config)
	}
	if err != nil {
		err = fmt.Errorf("failed to parse config: %w", err)
		return
	}

	packName = config.Name
	archiveName = fmt.Sprintf("%s-%s.tar.gz", config.Name, config.Ver)

	archiveFile, err := os.Create(archiveName)
	if err != nil {
		err = fmt.Errorf("failed to create archive: %w", err)
		return
	}
	defer archiveFile.Close()

	gw := gzip.NewWriter(archiveFile)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	for _, target := range config.Targets {
		select {
		case <-ctx.Done():
			err = fmt.Errorf("Create package canceled: %w", ctx.Err())
			return
		default:
		}

		root := filepath.Dir(target.Path)
		mask := filepath.Base(target.Path)

		err = filepath.Walk(root, func(filePath string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}

			for _, exclude := range strings.Split(target.Exclude, ",") {
				ok, err := filepath.Match(exclude, filepath.Base(filePath))
				if ok {
					return nil
				}
				if err != nil {
					return fmt.Errorf("error match exclude: %w", err)
				}
			}

			ok, err := filepath.Match(mask, filepath.Base(filePath))
			if !ok {
				return nil
			}
			if err != nil {
				return fmt.Errorf("error match exclude: %w", err)
			}

			return addFileToTar(tw, filePath, info)
		})
		if err != nil {
			err = fmt.Errorf("failed to add files to archive: %w", err)
			return
		}
	}
	metaPath := fmt.Sprintf("meta-%s-%s.json", config.Name, config.Ver)

	// copy config file to meta file
	err = os.WriteFile(metaPath, configData, 0644)
	if err != nil {
		slog.Error("Error creating meta file", "file", metaPath, "error", err)
		return
	}

	metaInfo, err := os.Stat(metaPath)
	if err != nil {
		err = fmt.Errorf("failed to get file info for meta file %s: %w", metaPath, err)
		return
	}
	err = addFileToTar(tw, metaPath, metaInfo)

	return
}

func addFileToTar(tw *tar.Writer, filePath string, info os.FileInfo) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %v", filePath, err)
	}
	defer file.Close()

	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return fmt.Errorf("failed to create tar header for %s: %v", filePath, err)
	}
	header.Name = filePath

	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("failed to write tar header for %s: %v", filePath, err)
	}

	size, err := io.Copy(tw, file)
	slog.Debug("add file", "size", size, "name", filePath)
	return err
}
