package knit

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	"github.com/pelletier/go-toml/v2"
)

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

const configFile = ".knit.toml"

func UserDefaults() (UserFlags, error) {
	var flags UserFlags

	loadFile := func(dir string) error {
		configPath := filepath.Join(dir, configFile)
		if exists(configPath) {
			data, err := os.ReadFile(configPath)
			if err != nil {
				return err
			}
			err = toml.Unmarshal(data, &flags)
			if err != nil {
				return err
			}
		}
		return nil
	}

	wd, err := os.Getwd()
	if err != nil {
		return UserFlags{}, err
	}
	dirs := []string{wd}
	path := wd
	for path != "/" {
		path = filepath.Dir(path)
		dirs = append(dirs, path)
	}
	dirs = append(dirs, configDirs...)
	for i := len(dirs) - 1; i >= 0; i-- {
		dir := dirs[i]
		err := loadFile(dir)
		if err != nil {
			return UserFlags{}, err
		}
	}
	return flags, nil
}
