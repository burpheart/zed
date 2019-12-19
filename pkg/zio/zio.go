package zio

import (
	"io"

	"github.com/mccanne/zq/pkg/zng"
)

type Flags struct {
	UTF8       bool
	ShowTypes  bool
	ShowFields bool
	EpochDates bool
}

type Writer struct {
	zng.WriteFlusher
	io.Closer
}

func NewWriter(writer zng.WriteFlusher, closer io.Closer) *Writer {
	return &Writer{
		WriteFlusher: writer,
		Closer:       closer,
	}
}

func (w *Writer) Close() error {
	err := w.Flush()
	cerr := w.Closer.Close()
	if err == nil {
		err = cerr
	}
	return err
}

func Extension(format string) string {
	switch format {
	case "zng":
		return ".zng"
	case "zeek":
		return ".log"
	case "ndjson":
		return ".ndjson"
	case "zjson":
		return ".ndjson"
	case "text":
		return ".txt"
	case "table":
		return ".tbl"
	case "bzng":
		return ".bzng"
	default:
		return ""
	}
}
