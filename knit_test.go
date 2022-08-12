package knit_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/zyedidia/knit"
)

type Test struct {
	Flags  knit.Flags
	Builds []Build
}

type Build struct {
	Args     []string
	Output   string
	Notbuilt []string
	Error    string
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func loadTest(dir string, t *testing.T) *Test {
	data, err := os.ReadFile(filepath.Join(dir, "test.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var test Test
	err = toml.Unmarshal(data, &test)
	if err != nil {
		t.Fatal(err)
	}
	return &test
}

func runTest(dir string, t *testing.T) {
	test := loadTest(dir, t)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(fmt.Errorf("could not get wd: %w", err))
	}

	os.Chdir(dir)
	defer os.Chdir(wd)
	for _, b := range test.Builds {
		buf := &bytes.Buffer{}
		err := knit.Run(buf, b.Args, test.Flags)
		if err != nil {
			if err.Error() == b.Error {
				continue
			}
			t.Fatal(err)
		}

		expected := strings.TrimSpace(b.Output)
		got := strings.TrimSpace(buf.String())

		if expected != got {
			t.Fatalf("expected %s, got %s", expected, got)
		}

		for _, f := range b.Notbuilt {
			if exists(f) {
				t.Fatalf("expected %s not to exist, but it does", f)
			}
		}
	}
	os.RemoveAll(".knit")
}

func TestAll(t *testing.T) {
	knit.Stderr = io.Discard

	files, err := os.ReadDir("./test")
	if err != nil {
		t.Fatal(fmt.Errorf("open test dir: %w", err))
	}

	tests := []string{}

	for _, f := range files {
		_, err := strconv.Atoi(f.Name())
		if f.IsDir() && err == nil {
			tests = append(tests, filepath.Join("test", f.Name()))
		}
	}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			runTest(tt, t)
		})
	}
}
