package main

import (
	"fmt"
	"os"
	"path/filepath"

	minidb "github.com/engineers-hub-ltd-in-house-project/minidb"
)

func main() {
	dir, err := os.MkdirTemp("", "minidb-demo")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "demo.db")
	disk, err := minidb.OpenDisk(path)
	if err != nil {
		panic(err)
	}
	defer disk.Close()

	heap := minidb.NewHeapFile(disk)

	const n = 1000
	for i := 0; i < n; i++ {
		rec := []byte(fmt.Sprintf("record-%04d", i))
		if _, err := heap.Insert(rec); err != nil {
			panic(err)
		}
	}

	count := 0
	err = heap.Scan(func(rid minidb.RecordID, rec []byte) error {
		count++
		return nil
	})
	if err != nil {
		panic(err)
	}

	pages, _ := disk.NumPages()
	fmt.Printf("inserted %d records across %d pages, scan returned %d\n", n, pages, count)
}
