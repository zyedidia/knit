package rules

import (
	"compress/gzip"
	"encoding/gob"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/segmentio/fasthash/fnv1a"
)

func hashSlice(s []string) uint64 {
	return fnv1a.HashString64(strings.Join(s, ""))
}

func hashSliceAndString(s []string, str string) uint64 {
	return fnv1a.HashString64(strings.Join(s, "") + str)
}

func hashFile(path string) uint64 {
	var hash uint64
	filepath.WalkDir(path, func(path string, info fs.DirEntry, err error) error {
		if !info.IsDir() {
			data, _ := os.ReadFile(path)
			hash = fnv1a.AddBytes64(hash, data)
			hash = fnv1a.AddString64(hash, path)
		}
		return nil
	})
	return hash
}

const dataFile = "data"

type Database struct {
	*data
	location string
}

func NewDatabase(dir string) *Database {
	var d *data
	var err error
	var f *os.File
	if f, err = os.Open(filepath.Join(dir, dataFile)); err == nil {
		d, err = loadData(f)
		f.Close()
	}
	// error opening or loading recipes file
	if err != nil {
		d = newData()
	}

	return &Database{
		location: dir,
		data:     d,
	}
}

func NewCacheDatabase(dir, wd string) *Database {
	return NewDatabase(filepath.Join(dir, url.PathEscape(wd)))
}

func (db *Database) Save() error {
	if err := os.MkdirAll(db.location, os.ModePerm); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(db.location, dataFile))
	if err != nil {
		return err
	}
	defer f.Close()
	return db.data.WriteBytesTo(f)
}

type data struct {
	Recipes Recipes
	Files   Files
}

func newData() *data {
	return &data{
		Recipes: Recipes{
			Hashes: make(map[uint64]uint64),
		},
		Files: Files{
			Data: make(map[string]File),
		},
	}
}

func (d *data) WriteBytesTo(w io.Writer) error {
	fz := gzip.NewWriter(w)
	enc := gob.NewEncoder(fz)
	err := enc.Encode(d)
	fz.Close()
	return err
}

func loadData(d io.Reader) (*data, error) {
	var dat data
	fz, err := gzip.NewReader(d)
	if err != nil {
		return nil, err
	}
	dec := gob.NewDecoder(fz)
	err = dec.Decode(&dat)
	fz.Close()
	return &dat, err
}

type Recipes struct {
	Hashes map[uint64]uint64
}

func (r *Recipes) has(targets, recipe []string, dir string) bool {
	rhash := hashSlice(recipe)
	thash := hashSliceAndString(targets, dir)
	if h, ok := r.Hashes[thash]; ok {
		return rhash == h
	}
	return false
}

func (r *Recipes) insert(targets, recipe []string, dir string) {
	rhash := hashSlice(recipe)
	thash := hashSliceAndString(targets, dir)
	r.Hashes[thash] = rhash
}

type Files struct {
	Data map[string]File
}

func (f *Files) insert(path string) {
	file, ok := NewFile(path)
	if !ok {
		return
	}
	f.Data[path] = file
}

func (f *Files) matches(path string) bool {
	if file, ok := f.Data[path]; ok {
		return file.Equals(path)
	}
	return false
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

	return File{
		ModTime: info.ModTime(),
		Size:    info.Size(),
		Full:    hashFile(path),
	}, true
}

func (f File) Equals(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !info.IsDir() && info.ModTime() == f.ModTime {
		return true
	}
	if info.Size() != f.Size {
		return false
	}
	return hashFile(path) == f.Full
}
