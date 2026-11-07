// Package imaging turns an uploaded file into the photos Maki stores. Inspect
// reads only a file's header, in Go. The conversion needs libvips through
// govips, and so cgo: it builds only with the vips build tag, in the worker's
// image. Without the tag Available is false and Convert refuses. cmd/api
// never imports this package, and stays static.
package imaging

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"

	// The formats a photo may come in, for DecodeConfig.
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

// ErrRejected is a file that isn't an image Maki takes: trying it again
// won't help.
var ErrRejected = errors.New("not an acceptable image")

// ErrUnavailable is a binary built without libvips.
var ErrUnavailable = errors.New("built without libvips: use the worker's image")

// Size is the box a photo is cut to.
type Size struct {
	Width, Height int
}

// Header is what a file's header says it is.
type Header struct {
	Format        string // jpeg, png or webp
	Width, Height int
}

// Inspect reads the file's header, never the pixels, and rejects anything
// but a JPG, PNG or WebP of at most maxSide pixels a side. A small file can
// hold a giant image that would take all the memory to open, so this comes
// before any decoding.
func Inspect(data []byte, maxSide int) (Header, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Header{}, fmt.Errorf("%w: %w", ErrRejected, err)
	}
	if cfg.Width > maxSide || cfg.Height > maxSide {
		return Header{}, fmt.Errorf("%w: %d×%d pixels is over %d a side", ErrRejected, cfg.Width, cfg.Height, maxSide)
	}
	return Header{Format: format, Width: cfg.Width, Height: cfg.Height}, nil
}

// The flags of a WebP's VP8X chunk that announce metadata.
const (
	webpFlagEXIF = 0x08
	webpFlagXMP  = 0x04
)

// dropWebPMetadata returns the WebP file without its EXIF and XMP chunks, and
// with its header no longer announcing them. A chunk's name, size and padded
// body are all RIFF asks for, so the pixels aren't touched.
func dropWebPMetadata(data []byte) ([]byte, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return nil, errors.New("not a webp file")
	}
	out := append(make([]byte, 0, len(data)), data[:12]...)
	for at := 12; at < len(data); {
		if at+8 > len(data) {
			return nil, errors.New("webp chunk header cut short")
		}
		name := string(data[at : at+4])
		size := int(binary.LittleEndian.Uint32(data[at+4:]))
		end := at + 8 + size + size%2
		if end > len(data) || end < at {
			return nil, errors.New("webp chunk cut short")
		}
		if name != "EXIF" && name != "XMP " {
			start := len(out)
			out = append(out, data[at:end]...)
			if name == "VP8X" && size > 0 {
				out[start+8] &^= webpFlagEXIF | webpFlagXMP
			}
		}
		at = end
	}
	binary.LittleEndian.PutUint32(out[4:], uint32(len(out)-8)) //nolint:gosec // at most the size of data, an uploaded photo
	return out, nil
}
