package admin

import (
	"errors"
	"io"
)

var ErrHostFile = errors.New("cannot read private admin-code file (regular file owned by this user, mode 0600 or private Windows ACL required)")

// ReadAdminCode opens without following a target link and never exposes paths or file contents in errors.
func ReadAdminCode(path string) (string, error) {
	f, err := openHostFile(path)
	if err != nil {
		return "", ErrHostFile
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > 16<<10 {
		return "", ErrHostFile
	}
	b, err := io.ReadAll(io.LimitReader(f, (16<<10)+1))
	if err != nil || len(b) > 16<<10 {
		return "", ErrHostFile
	}
	code := string(b)
	if _, _, err = ParseAdminCode(code); err != nil {
		return "", err
	}
	return code, nil
}
