package imaging

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInspectReadsTheHeader(t *testing.T) {
	h, err := Inspect(encodePNG(t, 30, 20), 8000)
	if err != nil {
		t.Fatal(err)
	}
	if h != (Header{Format: "png", Width: 30, Height: 20}) {
		t.Errorf("header = %+v", h)
	}
	if h, err := Inspect(encodeJPEG(t, 8000, 1), 8000); err != nil || h.Format != "jpeg" {
		t.Errorf("at the limit: %+v, %v", h, err)
	}
}

func TestInspectRejects(t *testing.T) {
	for name, data := range map[string][]byte{
		"not an image": []byte("<svg xmlns='http://www.w3.org/2000/svg'/>"),
		"empty":        nil,
		"too wide":     encodeJPEG(t, 8001, 1),
		"too tall":     encodeJPEG(t, 1, 8001),
		"cut short":    encodePNG(t, 10, 10)[:12],
	} {
		if _, err := Inspect(data, 8000); !errors.Is(err, ErrRejected) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func riffChunk(name string, body []byte) []byte {
	out := append([]byte(name), binary.LittleEndian.AppendUint32(nil, uint32(len(body)))...) //nolint:gosec // short test data
	out = append(out, body...)
	if len(body)%2 == 1 {
		out = append(out, 0)
	}
	return out
}

func riff(chunks ...[]byte) []byte {
	body := []byte("WEBP")
	for _, c := range chunks {
		body = append(body, c...)
	}
	return append(append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(len(body)))...), body...) //nolint:gosec // short test data
}

func TestDropWebPMetadata(t *testing.T) {
	vp8x := make([]byte, 10)
	vp8x[0] = webpFlagEXIF | webpFlagXMP | 0x20 // and an ICC profile, which stays
	in := riff(riffChunk("VP8X", vp8x), riffChunk("ICCP", []byte("icc")), riffChunk("VP8 ", []byte("pixels")),
		riffChunk("EXIF", []byte("GPS-odd")), riffChunk("XMP ", []byte("xmp!")))
	got, err := dropWebPMetadata(in)
	if err != nil {
		t.Fatal(err)
	}
	cleared := make([]byte, 10)
	cleared[0] = 0x20
	want := riff(riffChunk("VP8X", cleared), riffChunk("ICCP", []byte("icc")), riffChunk("VP8 ", []byte("pixels")))
	if !bytes.Equal(got, want) {
		t.Errorf("got  %q, want %q", got, want)
	}
	// A file with nothing to drop stays as it is.
	plain := riff(riffChunk("VP8 ", []byte("pixels")))
	if got, err := dropWebPMetadata(plain); err != nil || !bytes.Equal(got, plain) {
		t.Errorf("plain: %q, %v", got, err)
	}
}

func TestDropWebPMetadataRefusesBrokenFiles(t *testing.T) {
	cut := riff(riffChunk("VP8 ", []byte("pixels")))
	for name, data := range map[string][]byte{
		"not webp":     []byte("RIFFxxxxWAVEfmt "),
		"too short":    []byte("RIFF"),
		"body cut":     cut[:len(cut)-2],
		"header cut":   cut[:15],
		"size too big": riff(append([]byte("VP8 "), 0xff, 0xff, 0xff, 0x7f)),
	} {
		if _, err := dropWebPMetadata(data); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
