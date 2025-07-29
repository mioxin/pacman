package pacm

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetArch(t *testing.T) {
	slog.SetLogLoggerLevel(slog.LevelDebug)

	packName, archiveName, err := getArch(context.Background(), "./testdata/p.json")

	require.NoError(t, err)
	assert.EqualValues(t, "packet-1", packName)
	assert.EqualValues(t, "packet-1-1.10.tar.gz", archiveName)

	expectedFilesInfo := make(map[string]int64, 5)
	expectedFilesInfo["testdata/package/main.go"] = int64(70)
	expectedFilesInfo["meta-packet-1-1.10.json"] = int64(186)
	expectedFilesInfo["testdata/package1/packages.txt"] = int64(182)
	expectedFilesInfo["testdata/package1/packet.txt"] = int64(241)

	filesInfo := listTarGzContents(t, archiveName)
	assert.Equal(t, expectedFilesInfo, filesInfo)
}

// content list of  .tar.gz file
func listTarGzContents(t *testing.T, archivePath string) map[string]int64 {
	t.Helper()
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("failed open file %s: %v", archivePath, err)
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	files := make(map[string]int64, 5)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to read tar: %v", err)
		}
		if !header.FileInfo().IsDir() {
			files[header.Name] = header.Size
		}
	}
	return files
}

func TestCheckVersion(t *testing.T) {
	tests := []struct {
		name        string
		requiredVer string
		actualVer   string
		want        bool
	}{
		// empty requiredVer
		{
			name:        "empty required version",
			requiredVer: "",
			actualVer:   "1.12",
			want:        true,
		},
		{
			name:        "empty required version with empty actual",
			requiredVer: "",
			actualVer:   "",
			want:        true,
		},

		// =
		{
			name:        "equal versions",
			requiredVer: "1.12",
			actualVer:   "1.12",
			want:        true,
		},
		{
			name:        "not equal versions",
			requiredVer: "1.12",
			actualVer:   "1.13",
			want:        false,
		},
		{
			name:        "equal versions different format",
			requiredVer: "3.1",
			actualVer:   "3.1",
			want:        true,
		},

		// >=
		{
			name:        ">= same version",
			requiredVer: ">=1.12",
			actualVer:   "1.12",
			want:        true,
		},
		{
			name:        ">= higher version",
			requiredVer: ">=1.12",
			actualVer:   "1.13",
			want:        true,
		},
		{
			name:        ">= lower version",
			requiredVer: ">=1.12",
			actualVer:   "1.11",
			want:        false,
		},
		{
			name:        ">= different format",
			requiredVer: ">=3.1",
			actualVer:   "3.2",
			want:        true,
		},

		// >
		{
			name:        "> higher version",
			requiredVer: ">1.12",
			actualVer:   "1.13",
			want:        true,
		},
		{
			name:        "> same version",
			requiredVer: ">1.12",
			actualVer:   "1.12",
			want:        false,
		},
		{
			name:        "> lower version",
			requiredVer: ">1.12",
			actualVer:   "1.11",
			want:        false,
		},

		// <=
		{
			name:        "<= same version",
			requiredVer: "<=1.12",
			actualVer:   "1.12",
			want:        true,
		},
		{
			name:        "<= lower version",
			requiredVer: "<=1.12",
			actualVer:   "1.11",
			want:        true,
		},
		{
			name:        "<= higher version",
			requiredVer: "<=1.12",
			actualVer:   "1.13",
			want:        false,
		},

		// <
		{
			name:        "< lower version",
			requiredVer: "<1.12",
			actualVer:   "1.11",
			want:        true,
		},
		{
			name:        "< same version",
			requiredVer: "<1.12",
			actualVer:   "1.12",
			want:        false,
		},
		{
			name:        "< higher version",
			requiredVer: "<1.12",
			actualVer:   "1.13",
			want:        false,
		},

		// edge cases
		{
			name:        "invalid operator",
			requiredVer: "=1.12",
			actualVer:   "1.12",
			want:        false,
		},
		{
			name:        "empty actual version with operator",
			requiredVer: ">1.12",
			actualVer:   "",
			want:        false,
		},
		// {
		// 	name:        "invalid version format",
		// 	requiredVer: ">1.12-beta",
		// 	actualVer:   "1.13",
		// 	want:        false,
		// },
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkVersion(tt.requiredVer, tt.actualVer)
			if got != tt.want {
				t.Errorf("checkVersion(%q, %q) = %v, want %v", tt.requiredVer, tt.actualVer, got, tt.want)
			}
		})
	}
}
