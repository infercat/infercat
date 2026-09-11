package supervise

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLogBoundsOversizeAndWrap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	w := NewLog(f)
	if n, e := w.Write(bytes.Repeat([]byte("a"), 2<<20)); e != nil || n != 2<<20 {
		t.Fatal(n, e)
	}
	st, _ := f.Stat()
	if st.Size() != 1<<20 {
		t.Fatal(st.Size())
	}
	w.Write([]byte("last batch"))
	b, _ := os.ReadFile(path)
	if string(b) != "last batch" {
		t.Fatal("log did not wrap")
	}
}
