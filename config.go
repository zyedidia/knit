package knit

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

func DefaultConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return xdg
	}
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("error finding user home dir: %v", err)
		return ""
	}
	return filepath.Join(home, ".config", "knit")
}

const defaultFile = "knitfile.def"

func DefaultBuildFile() string {
	dir := DefaultConfigDir()
	if exists(filepath.Join(dir, defaultFile)) {
		return filepath.Join(dir, defaultFile)
	}
	return filepath.Join(DefaultConfigDir(), title(defaultFile))
}

type UserFlags struct {
	Knitfile *string
	Ncpu     *int
	Graph    *string
	DryRun   *bool
	RunDir   *string
	Always   *bool
	Quiet    *bool
	Clean    *bool
	Style    *string
	CacheDir *string
}

const configFile = ".knit.toml"

func UserDefaults() (UserFlags, error) {
	path := "."
	for {
		if !exists(path) {
			return UserFlags{}, nil
		}
		if exists(filepath.Join(path, configFile)) {
			path = filepath.Join(path, configFile)
			break
		}
		path = filepath.Join("..", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return UserFlags{}, err
	}
	var flags UserFlags
	err = toml.Unmarshal(data, &flags)
	if err != nil {
		return flags, fmt.Errorf("%s: %w", path, err)
	}
	return flags, nil
}
