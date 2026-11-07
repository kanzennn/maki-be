//go:build vips

package imaging

import (
	"fmt"

	"github.com/davidbyttow/govips/v2/vips"
)

// webpQuality keeps a menu photo small with no loss a customer would see.
const webpQuality = 80

// Available reports whether this binary can convert images.
const Available = true

// Start prepares libvips, once, before the first conversion. It is quiet but
// for warnings, keeps no cache, since no file is ever opened twice, and uses
// one thread a conversion: the worker runs jobs side by side already.
func Start() {
	vips.LoggingSettings(nil, vips.LogLevelWarning)
	// The zero limits are what keep the cache off: dropping it after startup
	// crashes libvips.
	vips.Startup(&vips.Config{ConcurrencyLevel: 1})
}

// Stop releases libvips.
func Stop() {
	vips.Shutdown()
}

// Converter converts uploads with libvips.
type Converter struct{}

// Convert returns the file as a WebP for each size: turned the way its EXIF
// says, cut from the centre to fill the box and never enlarged, with every
// bit of metadata, the camera's GPS position among it, gone. A file libvips
// can't read is ErrRejected.
func (Converter) Convert(data []byte, sizes []Size) ([][]byte, error) {
	out := make([][]byte, len(sizes))
	for i, size := range sizes {
		webp, err := convert(data, size)
		if err != nil {
			return nil, err
		}
		out[i] = webp
	}
	return out, nil
}

func convert(data []byte, size Size) ([]byte, error) {
	// Loading through the thumbnail shrinks while it decodes, which spares
	// the memory of the whole image, and applies the EXIF orientation first.
	img, err := vips.NewThumbnailWithSizeFromBuffer(data, size.Width, size.Height, vips.InterestingCentre, vips.SizeDown)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRejected, err)
	}
	defer img.Close()
	webp, _, err := img.ExportWebp(&vips.WebpExportParams{Quality: webpQuality, StripMetadata: true})
	if err != nil {
		return nil, fmt.Errorf("export webp: %w", err)
	}
	// libvips 8.14 writes the EXIF, GPS position included, despite the option
	// (TestConvertTurnsAndStrips), so the guarantee is made here.
	return dropWebPMetadata(webp)
}
