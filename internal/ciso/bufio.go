package ciso

import (
	"bufio"
	"io"
)

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// bufWriter is a buffered writer that keeps the first error.
type bufWriter struct {
	w   *bufio.Writer
	err error
}

func newBufWriter(w io.Writer) *bufWriter { return &bufWriter{w: bufio.NewWriterSize(w, 4<<20)} }

func (b *bufWriter) Write(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	n, err := b.w.Write(p)
	b.err = err
	return n, err
}

func (b *bufWriter) Flush() error {
	if b.err != nil {
		return b.err
	}
	return b.w.Flush()
}
