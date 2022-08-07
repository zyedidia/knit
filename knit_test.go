package knit_test

import (
	"bytes"
	"os"
	"path/filepath"
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
	Args   []string
	Output string
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

	os.Chdir(dir)
	for _, b := range test.Builds {
		buf := &bytes.Buffer{}
		knit.Run(buf, b.Args, test.Flags)

		expected := strings.TrimSpace(b.Output)
		got := strings.TrimSpace(buf.String())

		if expected != got {
			t.Fatalf("expected %s, got %s", expected, got)
		}
	}
	os.RemoveAll(filepath.Join(dir, ".knit"))
}

func Test1(t *testing.T) {
	runTest("test/1", t)
}
