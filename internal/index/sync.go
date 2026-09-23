package index

import (
	"log"
	"os"

	"github.com/ashankgupta/claudefinder/internal/claude"
)

type Stats struct {
	Scanned int
	Indexed int
	Removed int
}

type fileState struct {
	id    string
	size  int64
	mtime int64
}

func (d *DB) Sync(root string) (Stats, error) {
	var st Stats

	paths, err := claude.Scan(root)
	if err != nil {
		return st, err
	}
	st.Scanned = len(paths)

	known, err := d.fileStates()
	if err != nil {
		return st, err
	}

	onDisk := make(map[string]bool, len(paths))
	for _, p := range paths {
		onDisk[p] = true
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if k, ok := known[p]; ok && k.size == fi.Size() && k.mtime == fi.ModTime().Unix() {
			continue
		}
		s, err := claude.Parse(p)
		if err != nil {
			log.Printf("parse %s: %v", p, err)
			continue
		}
		if err := d.Upsert(s); err != nil {
			log.Printf("index %s: %v", p, err)
			continue
		}
		st.Indexed++
	}

	for p, k := range known {
		if !onDisk[p] {
			if err := d.Delete(k.id); err != nil {
				log.Printf("delete %s: %v", k.id, err)
				continue
			}
			st.Removed++
		}
	}
	return st, nil
}

func (d *DB) fileStates() (map[string]fileState, error) {
	rs, err := d.sql.Query(`SELECT file_path, id, file_size, file_mtime FROM sessions`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := map[string]fileState{}
	for rs.Next() {
		var p string
		var f fileState
		if err := rs.Scan(&p, &f.id, &f.size, &f.mtime); err != nil {
			return nil, err
		}
		out[p] = f
	}
	return out, rs.Err()
}

