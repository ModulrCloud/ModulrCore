package tests

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	ensureVersionFile()
	ensureChainDataPath()
	os.Exit(m.Run())
}

func ensureVersionFile() {
	if _, err := os.Stat("version.txt"); err == nil {
		return
	}

	rootPath, err := findVersionFile()
	if err != nil {
		return
	}

	src := filepath.Join(rootPath, "version.txt")
	_ = copyFile(src, "version.txt")
}

func findVersionFile() (string, error) {
	dirs := []string{".", "..", "../..", "../../.."}
	for _, dir := range dirs {
		candidate := filepath.Join(dir, "version.txt")
		if _, err := os.Stat(candidate); err == nil {
			return dir, nil
		}
	}
	return "", os.ErrNotExist
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func ensureChainDataPath() {
	if os.Getenv("CHAINDATA_PATH") != "" {
		return
	}

	path, err := os.MkdirTemp("", "chaindata")
	if err != nil {
		return
	}

	_ = os.Setenv("CHAINDATA_PATH", path)
}
