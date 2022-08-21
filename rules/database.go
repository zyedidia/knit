package rules

import (
	"compress/gzip"
	"encoding/gob"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrg/xdg"
	"github.com/segmentio/fasthash/fnv1a"
)

const recipesFile = "recipes"

type Database struct {
	location string
	recipes  *recipes
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

	return &Database{
		location: dir,
		recipes:  r,
	}
}

func NewCacheDatabase(wd string) *Database {
	return NewDatabase(filepath.Join(xdg.CacheHome, url.PathEscape(wd)))
}

func (db *Database) HasRecipe(targets, recipe []string, dir string) bool {
	return db.recipes.has(targets, recipe, dir)
}

func (db *Database) InsertRecipe(targets, recipe []string, dir string) {
	db.recipes.insert(targets, recipe, dir)
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
	return db.recipes.WriteBytesTo(f)
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
