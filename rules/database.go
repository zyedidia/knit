package rules

import (
	"compress/gzip"
	"encoding/gob"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/segmentio/fasthash/fnv1"
	"github.com/segmentio/fasthash/fnv1a"
)

const recipesFile = "recipes"
const filesFile = "files"

type Database struct {
	location string
	recipes  *recipes
	files    *files
}

func NewDatabase(dir string) *Database {
	var r *recipes
	var err error
	var f *os.File
	if f, err = os.Open(filepath.Join(dir, recipesFile)); err == nil {
		r, err = loadRecipes(f)
		f.Close()
	}
	// error opening or loading recipes file
	if err != nil {
		r = &recipes{Hashes: make(map[uint64]uint64)}
	}

	var fi *files
	if f, err = os.Open(filepath.Join(dir, recipesFile)); err == nil {
		fi, err = loadFiles(f)
		f.Close()
	}
	// error opening or loading recipes file
	if err != nil {
		fi = &files{Data: make(map[string]File)}
	}

	return &Database{
		location: dir,
		recipes:  r,
		files:    fi,
	}
}

func NewCacheDatabase(dir, wd string) *Database {
	return NewDatabase(filepath.Join(dir, url.PathEscape(wd)))
}

func (db *Database) Save() error {
	if err := os.MkdirAll(db.location, os.ModePerm); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(db.location, recipesFile))
	if err != nil {
		return err
	}
	defer f.Close()
	db.recipes.WriteBytesTo(f)
	db.files.WriteBytesTo(f)
	return nil
}

type recipes struct {
	Hashes map[uint64]uint64
}

func (r *recipes) WriteBytesTo(w io.Writer) error {
	fz := gzip.NewWriter(w)
	enc := gob.NewEncoder(fz)
	err := enc.Encode(r)
	fz.Close()
	return err
}

func loadRecipes(r io.Reader) (*recipes, error) {
	var recipes recipes
	fz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	dec := gob.NewDecoder(fz)
	err = dec.Decode(&recipes)
	fz.Close()
	return &recipes, err
}

func hashSlice(s []string) uint64 {
	return fnv1a.HashString64(strings.Join(s, ""))
}

func hashSliceAndString(s []string, str string) uint64 {
	return fnv1a.HashString64(strings.Join(s, "") + str)
}

func (r *recipes) has(targets, recipe []string, dir string) bool {
	rhash := hashSlice(recipe)
	thash := hashSliceAndString(targets, dir)
	if h, ok := r.Hashes[thash]; ok {
		return rhash == h
	}
	return false
}

func (r *recipes) insert(targets, recipe []string, dir string) {
	rhash := hashSlice(recipe)
	thash := hashSliceAndString(targets, dir)
	r.Hashes[thash] = rhash
}

type files struct {
	Data map[string]File
}

type File struct {
	ModTime time.Time
	Size    int64
	// TODO: optimize with a short hash
	Full uint64 // hash of the full file
}

func NewFile(path string) (File, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return File{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, false
	}
	return File{
		ModTime: info.ModTime(),
		Size:    info.Size(),
		Full:    fnv1a.HashBytes64(data),
	}, true
}

func (f File) Equals(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.ModTime() == f.ModTime {
		return true
	}
	if info.Size() != f.Size {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return fnv1.HashBytes64(data) == f.Full
}

func (f *files) WriteBytesTo(w io.Writer) error {
	fz := gzip.NewWriter(w)
	enc := gob.NewEncoder(fz)
	err := enc.Encode(f)
	fz.Close()
	return err
}

func loadFiles(r io.Reader) (*files, error) {
	var files files
	fz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	dec := gob.NewDecoder(fz)
	err = dec.Decode(&files)
	fz.Close()
	return &files, err
}

func (f *files) insert(path string) {
	file, ok := NewFile(path)
	if !ok {
		return
	}
	f.Data[path] = file
}

func (f *files) matches(path string) bool {
	if file, ok := f.Data[path]; ok {
		return file.Equals(path)
	}
	f.insert(path)
	return false
}
