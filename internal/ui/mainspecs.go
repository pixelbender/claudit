package ui

import (
	"io"
	"os"
	"strings"
	"time"
)

// mainSpecMax is how much of a main spec is read: a spec is text, a few hundred lines.
const mainSpecMax = 1 << 20

// mainSpecs keeps the main specs that the deltas shown structured modify. A frame draws them
// from here and reads a file only the first time it asks for it; the tick looks at the files and
// reads one again when its size or time changed, so that an edit shows.
type mainSpecs struct {
	files  map[string]*mainSpec
	polled time.Time
	stat   func(path string) (os.FileInfo, error)
	read   func(path string) ([]byte, error)
}

type mainSpec struct {
	lines []string // nil while there is no such file
	size  int64
	mod   time.Time
	used  bool // a frame drew it since the files were last looked at
}

func newMainSpecs() *mainSpecs {
	return &mainSpecs{files: map[string]*mainSpec{}, stat: os.Stat, read: readHead}
}

// readHead is the head of the file at path, mainSpecMax bytes at most.
func readHead(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, mainSpecMax))
}

// lines is the text of the main spec at path; nil for none.
func (c *mainSpecs) lines(path string) []string {
	f := c.files[path]
	if f == nil {
		f = c.load(path)
		c.files[path] = f
	}
	f.used = true
	return f.lines
}

// load reads the file at path.
func (c *mainSpecs) load(path string) *mainSpec {
	fi, err := c.stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return &mainSpec{}
	}
	data, err := c.read(path)
	if err != nil {
		return &mainSpec{}
	}
	return &mainSpec{lines: strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"), size: fi.Size(), mod: fi.ModTime()}
}

// poll looks at the files every pollEvery: it reads again those whose size or time changed, and
// forgets those no frame drew since the last look.
func (c *mainSpecs) poll(now time.Time) {
	if now.Sub(c.polled) < pollEvery {
		return
	}
	c.polled = now
	for path, f := range c.files {
		if !f.used {
			delete(c.files, path)
			continue
		}
		f.used = false
		fi, err := c.stat(path)
		switch {
		case err != nil || !fi.Mode().IsRegular():
			*f = mainSpec{used: f.used}
		case f.lines == nil || fi.Size() != f.size || !fi.ModTime().Equal(f.mod):
			*f = *c.load(path)
		}
	}
}
