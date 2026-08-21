package handler

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"

	_ "golang.org/x/image/webp"
)

// Shared by all three image uploaders: the user avatar, the branding logo, and the
// branding banner. They previously each carried their own copy of this preamble, which is
// how the avatar endpoint ended up without the dimension guard the other two got.

// maxUploadPixels bounds the DECODED size of any uploaded image.
//
// The 5 MB body limit bounds bytes on the wire, not pixels, and those are very different
// numbers: a highly compressed PNG of 30000x30000 is a few hundred KB on disk and roughly
// 3.6 GB once decoded. Checking dimensions before decoding is the only thing that stops
// that, and it matters more here than the admin-only gate suggests - the process holds the
// single SQLite connection, so an OOM takes the instance down rather than just failing the
// upload. It does not even need an attacker: a genuine 12000x8000 camera PNG is well under
// 5 MB compressed.
//
// 25 megapixels covers any camera or phone photo someone might reasonably use as a banner
// (25 MP is about 6000x4167) and costs at most ~100 MB to decode at 4 bytes per pixel. The
// result is resized to at most 1600x800 regardless, so nothing larger is ever useful.
const maxUploadPixels = 25_000_000

// decodeUploadedImage validates and decodes an uploaded image.
//
// Returns (img, "", nil) on success; (nil, msg, nil) when the upload should be rejected
// with 400 and msg shown to the admin; (nil, "", err) for an internal failure.
func decodeUploadedImage(file io.Reader, label string) (image.Image, string, error) {
	sniff := make([]byte, 512)
	// io.ReadFull rather than a bare Read: Read is permitted to return fewer bytes than
	// requested with no error, which would hand DetectContentType a short prefix and
	// misclassify a perfectly valid image. A file shorter than 512 bytes yields
	// ErrUnexpectedEOF, which is fine - n is still correct.
	n, err := io.ReadFull(file, sniff)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, "", fmt.Errorf("read %s: %w", label, err)
	}
	switch http.DetectContentType(sniff[:n]) {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		return nil, label + " must be JPEG, PNG, GIF, or WebP", nil
	}

	var buf bytes.Buffer
	buf.Write(sniff[:n])
	if _, err := buf.ReadFrom(file); err != nil {
		return nil, "", fmt.Errorf("read %s: %w", label, err)
	}
	data := buf.Bytes()

	// Header only - DecodeConfig reads the dimensions without allocating the pixel buffer,
	// which is the whole point: we learn how big the image claims to be before committing
	// the memory to find out.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "could not decode image", nil
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxUploadPixels {
		return nil, fmt.Sprintf("image is too large to process (%dx%d); please resize it below %d megapixels",
			cfg.Width, cfg.Height, maxUploadPixels/1_000_000), nil
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "could not decode image", nil
	}
	return img, "", nil
}
