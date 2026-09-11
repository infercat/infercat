package supervise

import (
	"io"
	"os"
)

func NewLog(f *os.File) io.Writer { return &limitedLog{file: f} }

type limitedLog struct {
	file *os.File
	n    int
}

func (l *limitedLog) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > 1<<20 {
		p = p[len(p)-(1<<20):]
	}
	if l.n+len(p) > 1<<20 {
		if err := l.file.Truncate(0); err != nil {
			return 0, err
		}
		if _, err := l.file.Seek(0, 0); err != nil {
			return 0, err
		}
		l.n = 0
	}
	wrote, err := l.file.Write(p)
	l.n += wrote
	if err != nil {
		return 0, err
	}
	return n, nil
}
