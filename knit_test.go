package knit_test

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/zyedidia/knit"
)

type Test struct {
	Name    string
	Disable bool
	Flags   knit.Flags
	Builds  []Build
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
	test.Flags.Shell = "sh"
	return &test
}

func runTest(dir string, t *testing.T) {
	test := loadTest(dir, t)
	if test.Disable {
		fmt.Printf("%s disabled\n", dir)
		return
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(fmt.Errorf("could not get wd: %w", err))
	}

	os.Chdir(dir)
	defer os.Chdir(wd)
	for i, b := range test.Builds {
		buf := &bytes.Buffer{}
		_, err := knit.Run(buf, b.Args, test.Flags)
		if err != nil {
			if err.Error() == b.Error {
				continue
			}
			t.Fatalf("%d: %v", i, err)
		}

		expected := strings.TrimSpace(b.Output)
		got := strings.TrimSpace(buf.String())

		if expected != got {
			t.Fatalf("%d: expected %s, got %s", i, expected, got)
		}

		for _, f := range b.Notbuilt {
			if exists(f) {
				t.Fatalf("%d: expected %s not to exist, but it does", i, f)
			}
		}
	}
	os.RemoveAll(".knit")
}

func TestAll(t *testing.T) {
	log.SetOutput(io.Discard)

	files, err := os.ReadDir("./test")
	if err != nil {
		t.Fatal(fmt.Errorf("open test dir: %w", err))
	}

	tests := []string{}

	for _, f := range files {
		if f.IsDir() && f.Name() != "scratch" {
			tests = append(tests, filepath.Join("test", f.Name()))
		}
	}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			runTest(tt, t)
		})
	}
}
