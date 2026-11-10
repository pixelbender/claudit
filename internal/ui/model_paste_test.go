package ui

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/pixelbender/claudit/internal/protocol"
)

// modelPastePNG writes a w×h PNG to dir/name and returns its path.
func modelPastePNG(t *testing.T, dir, name string, w, h int) string {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// modelPasteRun pastes s and feeds the model what the paste's command brings.
func modelPasteRun(m *Model, s string) {
	_, cmd := m.Update(tea.PasteMsg{Content: s})
	if cmd != nil {
		m.Update(cmd())
	}
}

func TestPasteLongTextStandsAsPlaceholder(t *testing.T) {
	m, sent := modelNew(t, false)
	long := "a\nb\nc\nd"
	modelPress(m, inputTyped("See ")...)
	modelPasteRun(m, long)
	if got := string(m.Input.Text); got != "See [Pasted text #1 +3 lines]" {
		t.Fatalf("prompt %q", got)
	}
	modelPasteRun(m, strings.Repeat("x", pasteChars+1))
	if got := string(m.Input.Text); !strings.HasSuffix(got, "[Pasted text #2]") {
		t.Fatalf("a long line: prompt %q", got)
	}
	modelPress(m, inputKey("enter"))
	if len(*sent) != 1 || modelUserText((*sent)[0]) != "See "+long+strings.Repeat("x", pasteChars+1) {
		t.Fatalf("sent %#v", *sent)
	}
	if got := m.Chat[len(m.Chat)-1].Text; got != "See "+long+strings.Repeat("x", pasteChars+1) {
		t.Errorf("chat %q, want the pasted texts", got)
	}
}

func TestPasteQueuedAndRecalledKeepTheirText(t *testing.T) {
	m, sent := modelNew(t, false)
	m.Status.Busy = true
	modelPasteRun(m, "1\n2\n3\n4")
	modelPress(m, inputKey("enter"))
	modelPress(m, inputKey("up")) // back to edit, then again
	modelPress(m, inputKey("enter"))
	modelEvents(m, protocol.Result{})
	if len(*sent) != 1 || modelUserText((*sent)[0]) != "1\n2\n3\n4" {
		t.Fatalf("sent %#v", *sent)
	}
	modelPress(m, inputKey("up"), inputKey("enter")) // from the history
	modelEvents(m, protocol.Result{})
	if len(*sent) != 2 || modelUserText((*sent)[1]) != "1\n2\n3\n4" {
		t.Fatalf("sent %#v", *sent)
	}
	for _, e := range m.Chat {
		if e.Kind == UserEntry && e.Text != "1\n2\n3\n4" {
			t.Errorf("chat %q, want the pasted text", e.Text)
		}
	}
	// Queued behind a turn, a prompt shows its text too; a picture keeps its placeholder.
	m.Status.Busy = true
	picture := m.attach(attachment{image: &protocol.Image{Media: "image/png", Data: []byte{0}}})
	m.pending = []string{m.attach(attachment{text: "5\n6"}) + " " + picture}
	entries := m.chatEntries()
	if e := entries[len(entries)-1]; !e.Queued || e.Text != "5\n6 "+picture {
		t.Errorf("queued %#v", e)
	}
}

func TestPasteBackspaceDeletesPlaceholder(t *testing.T) {
	m, _ := modelNew(t, false)
	modelPress(m, inputTyped("x")...)
	modelPasteRun(m, "1\n2\n3\n4")
	modelPress(m, inputKey("backspace"))
	if got := string(m.Input.Text); got != "x" || m.Input.Cursor != 1 {
		t.Fatalf("prompt %q cursor %d", got, m.Input.Cursor)
	}
	modelPress(m, inputKey("backspace"))
	if len(m.Input.Text) != 0 {
		t.Fatalf("prompt %q", string(m.Input.Text))
	}
}

func TestPasteDroppedPicturesAttach(t *testing.T) {
	m, sent := modelNew(t, false)
	dir := t.TempDir()
	a := modelPastePNG(t, dir, "shot one.png", 4, 3)
	b := modelPastePNG(t, dir, "b.PNG", 2, 2)
	modelPress(m, inputTyped("Look ")...)
	modelPasteRun(m, strings.ReplaceAll(filepath.ToSlash(a), " ", `\ `)+" '"+b+"' ")
	if got := string(m.Input.Text); got != "Look [Image #1] [Image #2] " {
		t.Fatalf("prompt %q", got)
	}
	modelPress(m, inputKey("enter"))
	u := (*sent)[0].(protocol.UserMessage)
	want, _ := os.ReadFile(a)
	if u.Text != "Look [Image #1] [Image #2]" || len(u.Images) != 2 || u.Images[0].Media != "image/png" || !bytes.Equal(u.Images[0].Data, want) {
		t.Fatalf("sent %q with %d images", u.Text, len(u.Images))
	}
}

// A terminal that pastes a picture as base64, wrapped or as a data URL, gives a picture; base64
// of anything else stays text.
func TestPasteBase64PictureAttaches(t *testing.T) {
	data, _ := os.ReadFile(modelPastePNG(t, t.TempDir(), "a.png", 3, 2))
	enc := base64.StdEncoding.EncodeToString(data)
	wrapped := enc[:20] + "\n" + enc[20:]
	for i, s := range []string{wrapped, "data:image/png;base64," + enc} {
		m, sent := modelNew(t, false)
		modelPasteRun(m, s)
		if got := string(m.Input.Text); got != "[Image #1] " {
			t.Fatalf("paste %d: prompt %q", i, got)
		}
		modelPress(m, inputKey("enter"))
		if u := (*sent)[0].(protocol.UserMessage); len(u.Images) != 1 || !bytes.Equal(u.Images[0].Data, data) {
			t.Fatalf("paste %d: sent %d images", i, len(u.Images))
		}
	}
	m, _ := modelNew(t, false)
	modelPasteRun(m, "aGVsbG8=") // "hello"
	if got := string(m.Input.Text); got != "aGVsbG8=" {
		t.Errorf("base64 text: prompt %q", got)
	}
}

func TestPastePathsThatAreNotPicturesStayText(t *testing.T) {
	m, _ := modelNew(t, false)
	dir := t.TempDir()
	txt := filepath.Join(dir, "notes.txt")
	os.WriteFile(txt, []byte("hi"), 0o644)
	for _, s := range []string{txt, filepath.Join(dir, "gone.png"), modelPastePNG(t, dir, "a.png", 1, 1) + " " + txt} {
		m.Input = Input{}
		modelPasteRun(m, s)
		if got := string(m.Input.Text); got != s {
			t.Errorf("paste %q: prompt %q", s, got)
		}
	}
}

func TestPasteShellWords(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want []string
	}{
		{`/a\ b.png /c.png`, []string{"/a b.png", "/c.png"}},
		{`'/a b.png' "/c d.png"`, []string{"/a b.png", "/c d.png"}},
		{`'/a b.png`, nil},
		{`C:\Users\me\shot.png`, []string{`C:\Users\me\shot.png`}},
		{`"C:\Users\Иван\shot one.png" "D:\two.png"`, []string{`C:\Users\Иван\shot one.png`, `D:\two.png`}},
		{`\\server\share\shot.png`, []string{`\\server\share\shot.png`}},
		{`"\\server\share\shot one.png" /a\ b.png`, []string{`\\server\share\shot one.png`, "/a b.png"}},
		{"  ", nil},
	} {
		if got := shellWords(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("shellWords(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPasteWindowsShellWords(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want []string
	}{
		{`C:/shots/one\ two.png`, []string{`C:/shots/one two.png`}},
		{`"C:\shots\one two.png"`, []string{`C:\shots\one two.png`}},
		{`"\\server\shots\one two.png"`, []string{`\\server\shots\one two.png`}},
		{`shots\one.png`, []string{`shots\one.png`}},
		{`"shots\one two.png"`, []string{`shots\one two.png`}},
		{`/shots/one\ two.png`, []string{`/shots/one two.png`}},
	} {
		if got := splitPastePaths(tt.in, true); !slices.Equal(got, tt.want) {
			t.Errorf("Windows paths %q = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPasteLargePictureShrinks(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // Resizing must work without external programs.
	dir := t.TempDir()
	small, err := loadImage(modelPastePNG(t, dir, "small.png", 300, 200))
	if err != nil || small.Media != "image/png" {
		t.Fatalf("small: %v %q", err, small.Media)
	}
	big, err := loadImage(modelPastePNG(t, dir, "big.png", 3000, 1000))
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(big.Data))
	if err != nil || big.Media != "image/jpeg" || cfg.Width != 2000 || cfg.Height != 666 || len(big.Data) > imageBytes {
		t.Fatalf("big: %v %q %dx%d", err, big.Media, cfg.Width, cfg.Height)
	}
}

func TestPasteOversizedFormatsShrinkWithoutPrograms(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, format := range []string{"jpeg", "gif"} {
		t.Run(format, func(t *testing.T) {
			var b bytes.Buffer
			im := image.NewPaletted(image.Rect(0, 0, 100, 2500), color.Palette{color.Black, color.White})
			var err error
			if format == "gif" {
				err = gif.Encode(&b, im, nil)
			} else {
				err = jpeg.Encode(&b, im, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "image."+format)
			if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := loadImage(path)
			if err != nil {
				t.Fatal(err)
			}
			cfg, _, err := image.DecodeConfig(bytes.NewReader(got.Data))
			if err != nil || cfg.Width != 80 || cfg.Height != 2000 || len(got.Data) > imageBytes {
				t.Fatalf("resized: %v %dx%d, %d bytes", err, cfg.Width, cfg.Height, len(got.Data))
			}
		})
	}
}

func TestPasteByteLimitReducesQualityWithoutUpscaling(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	im := image.NewRGBA(image.Rect(0, 0, 2000, 2000))
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < len(im.Pix); i += 4 {
		im.Pix[i], im.Pix[i+1], im.Pix[i+2], im.Pix[i+3] = byte(rng.Uint32()), byte(rng.Uint32()), byte(rng.Uint32()), 255
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, im, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	if b.Len() <= imageBytes {
		t.Fatal("fixture must exceed the upload limit")
	}
	path := filepath.Join(t.TempDir(), "noise.jpg")
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadImage(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(got.Data))
	if err != nil || cfg.Width != 2000 || cfg.Height != 2000 || len(got.Data) > imageBytes {
		t.Fatalf("resized: %v %dx%d, %d bytes", err, cfg.Width, cfg.Height, len(got.Data))
	}
}

func TestPasteWebPDimensionsAreLimited(t *testing.T) {
	// Solid orange, lossless WebP fixtures: 1x1 and 3000x1000.
	for _, tt := range []struct {
		data   string
		width  int
		height int
		media  string
	}{
		{"UklGRh4AAABXRUJQVlA4TBEAAAAvAAAAAAdQwMIXpP+BiOh/AAA=", 1, 1, "image/webp"},
		{"UklGRqIAAABXRUJQVlA4TJUAAAAvt8v5AAdQwMIXpP8BAUnS//9kRP8z/vOf//znP//5z3/+85///Oc///nPf/7zn//85z//+c9//vOf//znP//5z3/+85///Oc///nPf/7zn//85z//+c9//vOf//znP//5z3/+85///Oc///nPf/7zn//85z//+c9//vOf//znP//5z3/+85///Oc///nPf/7zn//HBAA=", 2000, 666, "image/jpeg"},
	} {
		data, err := base64.StdEncoding.DecodeString(tt.data)
		if err != nil {
			t.Fatal(err)
		}
		got := loadImageData(data).(imagesMsg)
		if got.err != nil || len(got.images) != 1 {
			t.Fatalf("WebP: %v, %d images", got.err, len(got.images))
		}
		im := got.images[0]
		cfg, _, err := image.DecodeConfig(bytes.NewReader(im.Data))
		if err != nil || cfg.Width != tt.width || cfg.Height != tt.height || im.Media != tt.media || len(im.Data) > imageBytes {
			t.Fatalf("WebP: %v %dx%d %s, %d bytes", err, cfg.Width, cfg.Height, im.Media, len(im.Data))
		}
	}
}

func TestPasteInvalidImageDoesNotAttach(t *testing.T) {
	got := loadImageData([]byte("\x89PNG\r\n\x1a\ninvalid image")).(imagesMsg)
	if got.err == nil || len(got.images) != 0 {
		t.Fatalf("invalid image: err=%v, images=%d", got.err, len(got.images))
	}
}

// A ! command gets the pasted texts in place of their placeholders; a picture's, and one claudit
// does not know, stay.
func TestPastedTexts(t *testing.T) {
	m, _ := modelNew(t, false)
	text := m.attach(attachment{text: "one\ntwo"})
	picture := m.attach(attachment{image: &protocol.Image{Media: "image/png", Data: []byte{0}}})
	got := m.pastedTexts("cat " + text + " " + picture + " [Pasted text #9]")
	if want := "cat one\ntwo [Image #2] [Pasted text #9]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A goal and a side question carry the pasted text too: the goal is shown as claude is told it,
// and /btw asks what was pasted, not its placeholder.
func TestPasteInGoalAndBtw(t *testing.T) {
	for _, busy := range []bool{false, true} {
		m, sent := modelNew(t, false)
		m.addCommands([]protocol.Command{{Name: "goal"}})
		m.Status.Busy = busy
		modelPress(m, inputTyped("/goal ")...)
		modelPasteRun(m, "fix\nthe\ntests\nnow")
		modelPress(m, inputKey("enter"))
		var shown string
		for _, e := range m.chatEntries() {
			if e.Kind == CommandEntry {
				shown = e.Text
			}
		}
		if want := "Goal set: " + tidy("fix\nthe\ntests\nnow"); shown != want {
			t.Errorf("busy=%v: goal %q, want %q", busy, shown, want)
		}
		if busy {
			modelEvents(m, protocol.Result{})
		}
		if len(*sent) != 1 || modelUserText((*sent)[0]) != "/goal fix\nthe\ntests\nnow" {
			t.Errorf("busy=%v: sent %#v", busy, *sent)
		}
	}
	m, sent := modelLocalNew(t)
	m.Status.Busy = true
	modelPress(m, inputTyped("/btw ")...)
	modelPasteRun(m, "a\nb\nc\nd")
	modelPress(m, inputKey("enter"))
	if len(*sent) != 1 {
		t.Fatalf("btw sent %#v", *sent)
	}
	if q, ok := (*sent)[0].(protocol.SideQuestion); !ok || q.Question != "a\nb\nc\nd" {
		t.Errorf("btw sent %#v", *sent)
	}
	if p, ok := m.Popup.(*BtwPopup); !ok || p.Question != "a\nb\nc\nd" {
		t.Errorf("btw popup %#v", m.Popup)
	}
	// A paste of nothing but blank lines asks nothing: /btw says how it is used.
	m.Popup = nil
	modelPress(m, inputTyped("/btw ")...)
	modelPasteRun(m, "\n \n\n\n")
	modelPress(m, inputKey("enter"))
	if len(*sent) != 1 || m.Popup != nil {
		t.Errorf("btw of blank lines: sent %#v popup %#v", *sent, m.Popup)
	}
}
