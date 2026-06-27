package logging

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Handler struct {
	state *state
	attrs []slog.Attr
	group string
}

type state struct {
	mu     sync.Mutex
	level  slog.Level
	dir    string
	date   string
	index  int
	file   *os.File
	stdout io.Writer
}

func New(levelText, dir string, stdout io.Writer) (*slog.Logger, io.Closer, error) {
	level := parseLevel(levelText)
	h := &Handler{state: &state{
		level:  level,
		dir:    dir,
		stdout: stdout,
	}}
	if err := h.rotate(time.Now()); err != nil {
		return nil, nil, err
	}
	return slog.New(h), h, nil
}

func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.state.level
}

func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()

	if err := h.rotate(r.Time); err != nil {
		return err
	}

	var b bytes.Buffer
	b.WriteByte('[')
	b.WriteString(r.Time.Format("2006-01-02 15:04:05.000"))
	b.WriteString("][")
	b.WriteString(goroutineID())
	b.WriteByte('/')
	b.WriteString(strings.ToUpper(r.Level.String()))
	b.WriteString("] ")
	b.WriteString(r.Message)

	writeAttr := func(a slog.Attr) bool {
		if a.Equal(slog.Attr{}) {
			return true
		}
		if h.group != "" {
			b.WriteByte(' ')
			b.WriteString(h.group)
			b.WriteByte('.')
		} else {
			b.WriteByte(' ')
		}
		b.WriteString(a.Key)
		b.WriteByte('=')
		b.WriteString(attrValue(a.Value))
		return true
	}
	for _, a := range h.attrs {
		writeAttr(a)
	}
	r.Attrs(writeAttr)
	b.WriteByte('\n')

	line := b.Bytes()
	if h.state.stdout != nil {
		_, _ = h.state.stdout.Write(line)
	}
	if h.state.file != nil {
		_, _ = h.state.file.Write(line)
	}
	return nil
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{
		state: h.state,
		attrs: append(append([]slog.Attr{}, h.attrs...), attrs...),
		group: h.group,
	}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	cp := *h
	if cp.group == "" {
		cp.group = name
	} else {
		cp.group += "." + name
	}
	return &cp
}

func (h *Handler) Close() error {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	if h.state.file == nil {
		return nil
	}
	err := h.state.file.Close()
	h.state.file = nil
	return err
}

func (h *Handler) rotate(t time.Time) error {
	s := h.state
	date := t.Format("2006-01-02")
	if s.file != nil && s.date == date {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
	index, path := nextLogPath(s.dir, date)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	s.date = date
	s.index = index
	s.file = file
	return nil
}

func nextLogPath(dir, date string) (int, string) {
	for i := 1; ; i++ {
		path := filepath.Join(dir, fmt.Sprintf("%s-%d.log", date, i))
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return i, path
		}
	}
}

func parseLevel(text string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func attrValue(v slog.Value) string {
	if v.Kind() == slog.KindString {
		return strconv.Quote(v.String())
	}
	return fmt.Sprint(v.Any())
}

func goroutineID() string {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	fields := strings.Fields(string(buf[:n]))
	if len(fields) >= 2 {
		return "g" + fields[1]
	}
	return "g?"
}
