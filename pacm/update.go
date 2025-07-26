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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	scp "github.com/bramvdbogaerde/go-scp"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

func (pm *PackageManager) UpdatePackages(ctx context.Context, configPath string) error {

	var wg sync.WaitGroup

	startTime := time.Now()
	defer func() {
		slog.Info("Finish update packages", "time", time.Since(startTime))
	}()

	defer wg.Wait()

	slog.Info("Start update packages...")

	select {
	case <-ctx.Done():
		return fmt.Errorf("Create packege canceled: %s", ctx.Err())
	default:
	}

	configData, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	var config PackagesConfig
	if strings.HasSuffix(configPath, ".yaml") || strings.HasSuffix(configPath, ".yml") {
		err = yaml.Unmarshal(configData, &config)
	} else {
		err = json.Unmarshal(configData, &config)
	}
	if err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	for _, pkg := range config.Packages {
		wg.Add(1)

		go func(pkg Packet) {
			defer wg.Done()
			lg := slog.With("package", pkg.Name, "version", pkg.Ver)
			sshClient, err := ssh.Dial("tcp", pm.server, pm.sshConfig)
			if err != nil {
				lg.Error("failed to connect to SSH server", "error", err)
				return
			}
			defer sshClient.Close()

			// Create a new SCP client, note that this function might
			// return an error, as a new SSH session is established using the existing connecton

			client, err := scp.NewClientBySSH(sshClient)
			if err != nil {
				lg.Error("Error creating new SSH session from existing connection", "error", err)
				return
			}
			defer client.Close()

			lg.Info("Update package", "name", pkg.Name, "version", pkg.Ver)

			// Get archive name
			packPath := fmt.Sprintf("%s/%s", os.Getenv("PACMAN_ROOT_DIR"), pkg.Name)
			archiveName, err := getArchiveName(ctx, lg, sshClient, packPath, pkg.Name, pkg.Ver) //
			if err != nil {
				lg.Error("Skip packet. Failed to get archive name", "packet", pkg.Name, "error", err)
				return
			}
			remotePath := fmt.Sprintf("%s/%s", packPath, archiveName)

			archiveFile, err := os.Create(archiveName)
			if err != nil {
				lg.Error("failed to create local archive", "error", err)
				return
			}
			err = client.CopyFromRemote(ctx, archiveFile, remotePath)
			if err != nil {
				archiveFile.Close()
				lg.Error("failed to download archive from server", "error", err)
				return
			}

			archiveFile, err = os.Open(archiveName)
			if err != nil {
				lg.Error("failed to open archive", "error", err)
				return
			}

			gr, err := gzip.NewReader(archiveFile)
			if err != nil {
				archiveFile.Close()
				lg.Error("failed to create gzip reader", "error", err)
				return
			}
			defer gr.Close()

			tr := tar.NewReader(gr)
			for {
				header, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					archiveFile.Close()
					lg.Error("failed to read tar", "error", err)
					return
				}

				outPath := header.Name
				if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
					archiveFile.Close()
					lg.Error("failed to create directories", "error", err)
					return
				}

				outFile, err := os.Create(outPath)
				if err != nil {
					archiveFile.Close()
					lg.Error("failed to create output file", "error", err)
					return
				}
				_, err = io.Copy(outFile, tr)
				outFile.Close()
				if err != nil {
					archiveFile.Close()
					lg.Error("failed to write output file", "error", err)
					return
				}
			}
			archiveFile.Close()
		}(pkg)
	}

	return nil
}

func getArchiveName(ctx context.Context, log *slog.Logger, sshClient *ssh.Client, packPath, packName, ver string) (archName string, err error) {
	slog.Debug("Get archive name", "path", packPath, "ver", ver)

	session, err := sshClient.NewSession()
	if err != nil {
		log.Error("Failed to create SSH session", "error", err)
		return
	}
	defer session.Close()

	// Use a command to list the archive files in the package path
	cmd := fmt.Sprintf("ls %s/%s*.tar.gz", packPath, packName)
	output, err := session.CombinedOutput(cmd)
	if err != nil {
		log.Error("Failed to execute command", "cmd", cmd, "error", err, "output", string(output))
		return
	}

	archNames := strings.TrimSpace(string(output))
	if archNames == "" {
		err = fmt.Errorf("no archive found for package %s version %s", packName, ver)
		log.Error("No archive found", "error", err)
		return
	}

	// Split the output to get the archive name
	archNamesSlice := strings.Split(archNames, "\n")

	slices.SortFunc(archNamesSlice, func(a, b string) int {
		v1, _ := getVersionFromArchiveName(a, packName)
		v2, _ := getVersionFromArchiveName(b, packName)
		return compareVersions(v1, v2)
	})
	log.Debug("Sorted archive names", "names", archNamesSlice)
	//archName = filepath.Base(archNamesSlice[len(archNamesSlice)-1])

	// check version
	found := false
	for _, arch := range archNamesSlice {
		arch = filepath.Base(arch)
		actualVer, err := getVersionFromArchiveName(arch, packName)
		if err != nil {
			err = fmt.Errorf("archive name %s does not contain valid version: %w", arch, err)
			log.Error("Archive name does not contain valid version", "name", arch, "error", err)
			archName = ""
			continue
		}
		if checkVersion(ver, actualVer) {
			archName = arch
			found = true
			log.Debug("Found matching archive name", "name", archName, "version", ver)
		} else {
			log.Debug("Archive name does not match version", "name", arch, "version", ver)
			if found {
				// If we already found a matching archive, we can stop checking further
				break
			}
		}
	}
	return
}

func compareVersions(v1, v2 string) int {
	v1Parts := strings.Split(v1, ".")
	v2Parts := strings.Split(v2, ".")

	maxLen := len(v1Parts)
	if len(v2Parts) > maxLen {
		maxLen = len(v2Parts)
	}

	for i := 0; i < maxLen; i++ {
		var (
			p1, p2 int
			err    error
		)
		if i < len(v1Parts) && v1Parts[i] != "" {
			p1, err = strconv.Atoi(v1Parts[i])
			if err != nil {
				slog.Warn("compareVersions: can't parse str to int", "string", v1Parts[i], "error", err)
				p1 = 0
			}
		}
		if i < len(v2Parts) && v2Parts[i] != "" {
			p2, err = strconv.Atoi(v2Parts[i])
			if err != nil {
				slog.Warn("compareVersions: can't parse str to int", "string", v2Parts[i], "error", err)
				p2 = 0
			}
		}
		if p1 < p2 {
			return -1
		}
		if p1 > p2 {
			return 1
		}
	}
	return 0
}

func getVersionFromArchiveName(archiveName, packName string) (string, error) {
	// Example archive name: /packages/packet-1/packet-1-1.10.tar.gz
	archiveName = filepath.Base(archiveName)
	version := strings.TrimPrefix(archiveName, fmt.Sprintf("%s-", packName))
	version = strings.TrimSuffix(version, ".tar.gz")
	if version == "" {
		return "", fmt.Errorf("archive name %s does not contain version", archiveName)
	}
	return version, nil
}

func checkVersion(requiredVer, actualVer string) bool {
	if requiredVer == "" {
		return true // no need version check
	}

	// equal version
	if !strings.HasPrefix(requiredVer, ">") && !strings.HasPrefix(requiredVer, "<") {
		return requiredVer == actualVer
	}

	op := ""
	verStr := requiredVer
	switch {
	case strings.HasPrefix(requiredVer, ">="):
		op = ">="
		verStr = strings.TrimPrefix(requiredVer, ">=")
	case strings.HasPrefix(requiredVer, "<="):
		op = "<="
		verStr = strings.TrimPrefix(requiredVer, "<=")
	case strings.HasPrefix(requiredVer, ">"):
		op = ">"
		verStr = strings.TrimPrefix(requiredVer, ">")
	case strings.HasPrefix(requiredVer, "<"):
		op = "<"
		verStr = strings.TrimPrefix(requiredVer, "<")
	}

	switch op {
	case ">=":
		return compareVersions(actualVer, verStr) >= 0
	case "<=":
		return compareVersions(actualVer, verStr) <= 0
	case ">":
		return compareVersions(actualVer, verStr) > 0
	case "<":
		return compareVersions(actualVer, verStr) < 0
	default:
		return false // should not happen
	}
}
