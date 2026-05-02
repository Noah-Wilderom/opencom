// internal/cli/tui/qr.go
package tui

import (
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// renderASCIIQR returns a UTF-8 block-character QR code suitable for
// rendering in a monospace terminal. Uses upper- and lower-half
// blocks (▀ ▄ █) so each terminal cell encodes two QR modules
// vertically — halves the visible rendered height. Quiet zone of 1
// module is included by go-qrcode by default.
//
// Errors only when the payload exceeds QR's max capacity (~2.9 KiB at
// medium error correction).
func renderASCIIQR(payload string) (string, error) {
	q, err := qrcode.New(payload, qrcode.Medium)
	if err != nil {
		return "", err
	}
	bm := q.Bitmap()
	var b strings.Builder
	for r := 0; r < len(bm); r += 2 {
		for c := 0; c < len(bm[r]); c++ {
			top := bm[r][c]
			bot := false
			if r+1 < len(bm) {
				bot = bm[r+1][c]
			}
			switch {
			case top && bot:
				b.WriteRune('█')
			case top:
				b.WriteRune('▀')
			case bot:
				b.WriteRune('▄')
			default:
				b.WriteRune(' ')
			}
		}
		b.WriteRune('\n')
	}
	return b.String(), nil
}
