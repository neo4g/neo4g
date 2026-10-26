package server

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// appliedLSNFile is the last contiguously applied primary LSN.
// It is not the local WAL's NextLSN: a checkpoint truncates that log and a
// restart of an empty file numbers new records from 1 again.
const appliedLSNFile = "applied.lsn"

func appliedLSNPath(dir string) string {
	return filepath.Join(dir, appliedLSNFile)
}

func readAppliedLSN(dir string) uint64 {
	if dir == "" {
		return 0
	}
	b, err := os.ReadFile(appliedLSNPath(dir))
	if err != nil {
		return 0
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func writeAppliedLSN(dir string, lsn uint64) error {
	if dir == "" {
		return os.ErrInvalid
	}
	path := appliedLSNPath(dir)
	tmp := path + ".tmp"
	body := []byte(strconv.FormatUint(lsn, 10) + "\n")
	if err := os.WriteFile(tmp, body, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
