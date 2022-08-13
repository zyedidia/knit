package knit

import (
	"log"
	"os"
	"path/filepath"
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
