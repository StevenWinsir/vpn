// Package ipc implements the private Helper <-> Core framing: a 4-byte big-endian
// length followed by one bounded JSON document. It is not an HTTP server.
package ipc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MaxFrame is the absolute frame ceiling (2 MiB) from handoff_app.md section 5.3.
const MaxFrame = 2 << 20

var (
	ErrFrameTooLarge = errors.New("ipc: frame exceeds limit")
	ErrEmptyFrame    = errors.New("ipc: empty frame")
)

// ReadFrame reads one frame. A clean EOF before any header byte is io.EOF; a
// truncated header or body is io.ErrUnexpectedEOF. The body is never allocated
// before the declared length has been checked against limit.
func ReadFrame(r io.Reader, limit uint32) ([]byte, error) {
	if limit == 0 || limit > MaxFrame {
		limit = MaxFrame
	}
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 {
		return nil, ErrEmptyFrame
	}
	if size > limit {
		return nil, fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, size, limit)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return body, nil
}

// WriteFrame writes one frame with a single Write call so header and body cannot
// be interleaved with another writer.
func WriteFrame(w io.Writer, body []byte) error {
	if len(body) == 0 {
		return ErrEmptyFrame
	}
	if len(body) > MaxFrame {
		return ErrFrameTooLarge
	}
	buffer := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(buffer, uint32(len(body)))
	copy(buffer[4:], body)
	_, err := w.Write(buffer)
	return err
}
