package ghr

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

type renderer struct {
	builder strings.Builder
	err     error
}

func newRenderer() *renderer {
	return &renderer{}
}

func (r *renderer) Printf(format string, args ...any) {
	if r.err != nil {
		return
	}
	_, r.err = fmt.Fprintf(&r.builder, format, args...)
}

func (r *renderer) Println(args ...any) {
	if r.err != nil {
		return
	}
	_, r.err = fmt.Fprintln(&r.builder, args...)
}

func (r *renderer) Tab() *renderTabWriter {
	return &renderTabWriter{
		renderer: r,
		writer:   tabwriter.NewWriter(&r.builder, 0, 4, 2, ' ', 0),
	}
}

func (r *renderer) FlushTo(out io.Writer) error {
	if r.err != nil {
		return r.err
	}
	_, err := io.WriteString(out, r.builder.String())
	return err
}

type renderTabWriter struct {
	renderer *renderer
	writer   *tabwriter.Writer
}

func (w *renderTabWriter) Printf(format string, args ...any) {
	if w.renderer.err != nil {
		return
	}
	_, w.renderer.err = fmt.Fprintf(w.writer, format, args...)
}

func (w *renderTabWriter) Println(args ...any) {
	if w.renderer.err != nil {
		return
	}
	_, w.renderer.err = fmt.Fprintln(w.writer, args...)
}

func (w *renderTabWriter) Flush() {
	if w.renderer.err != nil {
		return
	}
	w.renderer.err = w.writer.Flush()
}
