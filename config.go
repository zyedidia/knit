package knit

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
)

type Flags struct {
	Knitfile string
	Ncpu     int
	Graph    string
	DryRun   bool
	RunDir   string
	Always   bool
	Quiet    bool
	Clean    bool
	Style    string
	CacheDir string
	Hash     bool
	Commands bool
	Updated  []string
	Tool     string
	ToolArgs []string
}

var ErrBuildFileNotFound = errors.New("build file not found")

const defaultFile = "knitfile.def"

var configDirs = []string{
	filepath.Join(xdg.ConfigHome, "knit"),
}

func init() {
	for _, dir := range xdg.DataDirs {
		configDirs = append(configDirs, filepath.Join(dir, "knit"))
	}
}
func DefaultBuildFile() (string, bool) {
	for _, dir := range configDirs {
		if exists(filepath.Join(dir, defaultFile)) {
			return filepath.Join(defaultFile), true
		} else if exists(filepath.Join(dir, title(defaultFile))) {
			return filepath.Join(dir, title(defaultFile)), true
		}
	}
	return "", false
}

func FindBuildFile(name string) (string, string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	dirs := []string{wd}
	path := wd
	for path != "/" {
		if exists(filepath.Join(path, name)) {
			p, e := filepath.Rel(wd, path)
			return name, p, e
		}
		if exists(filepath.Join(path, title(name))) {
			p, e := filepath.Rel(wd, path)
			return title(name), p, e
		}
		path = filepath.Dir(path)
		dirs = append(dirs, path)
	}
	return "", "", nil
}
