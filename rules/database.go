package rules

import (
	"compress/gzip"
	"encoding/gob"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/segmentio/fasthash/fnv1a"
)

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
}

func newData() *data {
	return &data{
		Recipes: Recipes{
			Hashes: make(map[uint64]uint64),
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

func hashSlice(s []string) uint64 {
	return fnv1a.HashString64(strings.Join(s, ""))
}

func hashSliceAndString(s []string, str string) uint64 {
	return fnv1a.HashString64(strings.Join(s, "") + str)
}
