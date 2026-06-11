// Package tables loads the compressed parse-table blobs that
// internal/grammars/gen splits out of vendored Tree-sitter grammars.
package tables

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
)

// Sections gunzips a tables.bin.gz blob and returns its sections. The blob
// is a build artifact embedded at compile time, so any failure means the
// binary itself is corrupt and no caller can recover; Sections panics rather
// than returning an error (gzip's CRC32 catches bit rot in the embedded data).
func Sections(gz []byte, want int) [][]byte {
	out, err := sections(gz, want)
	if err != nil {
		panic(fmt.Sprintf("grammar tables: %v", err)) // lint:ignore panic-in-library compile-time-embedded data, unrecoverable
	}
	return out
}

func sections(gz []byte, want int) ([][]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	if err := zr.Close(); err != nil {
		return nil, err
	}

	if len(raw) < 8 || string(raw[:4]) != "LTB1" {
		return nil, fmt.Errorf("bad blob header")
	}
	n := int(binary.LittleEndian.Uint32(raw[4:]))
	if n != want {
		return nil, fmt.Errorf("blob has %d sections, want %d", n, want)
	}
	off := 8 + 8*n
	if len(raw) < off {
		return nil, fmt.Errorf("truncated blob header")
	}
	out := make([][]byte, n)
	pos := off
	for i := 0; i < n; i++ {
		size := int(binary.LittleEndian.Uint64(raw[8+8*i:]))
		if pos+size > len(raw) {
			return nil, fmt.Errorf("truncated blob section")
		}
		out[i] = raw[pos : pos+size]
		pos += size
	}
	if pos != len(raw) {
		return nil, fmt.Errorf("%d trailing bytes in blob", len(raw)-pos)
	}
	return out, nil
}
