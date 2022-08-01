package main

import (
	"compress/gzip"
	"encoding/gob"
	"io"
	"strings"

	"github.com/segmentio/fasthash/fnv1a"
)

type db struct {
	Hashes map[uint64]uint64
}

func newDb() *db {
	return &db{
		Hashes: make(map[uint64]uint64),
	}
}

// ToBytes serializes and compresses this db.
func (db *db) ToWriter(w io.Writer) error {
	fz := gzip.NewWriter(w)
	enc := gob.NewEncoder(fz)
	err := enc.Encode(db)
	fz.Close()
	return err
}

// FromBytes loads a db from a compressed and serialized object.
func newDbFromReader(r io.Reader) (*db, error) {
	var d db
	fz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	dec := gob.NewDecoder(fz)
	err = dec.Decode(&d)
	fz.Close()
	return &d, err
}

func hashSlice(s []string) uint64 {
	return fnv1a.HashString64(strings.Join(s, ""))
}

func (db *db) has(targets []string, recipe []string) bool {
	rhash := hashSlice(recipe)
	thash := hashSlice(targets)
	if h, ok := db.Hashes[thash]; ok {
		return rhash == h
	}
	return false
}

func (db *db) insert(targets []string, recipe []string) {
	rhash := hashSlice(recipe)
	thash := hashSlice(targets)
	db.Hashes[thash] = rhash
}
