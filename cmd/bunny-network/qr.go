package main

import (
	"fmt"
	"io"

	qrcode "github.com/skip2/go-qrcode"
)

// writeQR prints the invite as a scannable QR code, two module rows per text row.
//
// The colours are set explicitly (black on white) rather than left to the terminal's palette:
// a QR drawn in the terminal's own foreground colour comes out inverted on a dark theme, and
// not every phone camera reads an inverted code. Two rows of modules share one character cell
// via the half-block glyphs, which keeps a version-6 code inside 80 columns.
func writeQR(w io.Writer, content string) error {
	q, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return err
	}
	bm := q.Bitmap() // true = dark module; the quiet zone is already included
	for y := 0; y < len(bm); y += 2 {
		fmt.Fprint(w, "  \x1b[30;47m")
		for x := range bm[y] {
			top := bm[y][x]
			bottom := y+1 < len(bm) && bm[y+1][x]
			fmt.Fprint(w, halfBlock(top, bottom))
		}
		fmt.Fprint(w, "\x1b[0m\n")
	}
	return nil
}

// halfBlock is the glyph whose upper half is top and lower half is bottom, drawn in the
// foreground colour for a dark module and the background colour for a light one.
func halfBlock(top, bottom bool) string {
	switch {
	case top && bottom:
		return "█" // full block
	case top:
		return "▀" // upper half
	case bottom:
		return "▄" // lower half
	default:
		return " "
	}
}
