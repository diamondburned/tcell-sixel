package tsixel

import (
	"bytes"
	"image"

	"github.com/disintegration/imaging"
	"github.com/mattn/go-sixel"
)

// Image represents a SIXEL image. This image holds the source image and resizes
// it as needed. Each image has its own buffer and its associated encoder. To
// set its boundaries, use the SetBounds method. Note that the setter methods
// don't update the screen; the caller must manually synchronize it.
//
// An image is not thread-safe, so it is not safe to share it across multiple
// screens, even with the same dimensions.
type Image struct {
	src       image.Image
	bounds    image.Rectangle // requested region
	currentSz image.Point     // current image size

	sstate *screenState // screen state

	enc *sixel.Encoder
	buf *bytes.Buffer

	opts ImageOpts
}

// ImageOpts represents the options of a SIXEL image. It is meant to be constant
// to each image.
type ImageOpts struct {
	// Resize, if true, will enable resizing for the image if it ever goes over
	// the size set or the screen.
	Resize bool
	// KeepRatio, if true, will maintain the aspect ratio of the image when it's
	// scaled down to fit the size. The image will be anchored on the top left.
	KeepRatio bool
	// Dither, if true, will apply dithering onto the image.
	Dither bool
	// Filter determines the resample filter to use when scaling. The default is
	// NearestNeighbor, which is very rough but fast.
	Filter imaging.ResampleFilter
}

// NewImage creates a new SIXEL image from the given image.
func NewImage(img image.Image, opts ImageOpts) *Image {
	buf := bytes.Buffer{}
	buf.Grow(SIXELBufferSize)

	enc := sixel.NewEncoder(&buf)
	enc.Dither = opts.Dither

	return &Image{
		src:  img,
		enc:  enc,
		buf:  &buf,
		opts: opts,
	}
}

// SetMaxBounds sets the size of the image in units of cells. In other words, it
// sets the bottom-right corner of the image relatively to the top-left corner
// of the image. Note that this merely sets a hint; the actual image will never
// be larger than the screen OR the source image. The function will also not do
// much if resizing is not enabled.
func (img *Image) SetSize(size image.Point) {
	if img.sstate != nil {
		img.sstate.Lock()
		defer img.sstate.Unlock()
	}

	img.setSize(size)
}

func (img *Image) setSize(size image.Point) {
	img.bounds.Max = img.bounds.Min.Add(size)
}

// SetPosition sets the top-left corner of the image in units of cells.
func (img *Image) SetPosition(pos image.Point) {
	if img.sstate != nil {
		img.sstate.Lock()
		defer img.sstate.Unlock()
	}

	img.setPosition(pos)
}

func (img *Image) setPosition(pos image.Point) {
	old := img.bounds.Min
	img.bounds.Min = pos

	// Recalculate the size for the bounds.
	img.setSize(img.bounds.Max.Sub(old))
}

// Bounds returns the bounds of the image relative to the top-left corner of the
// screen in units of cells. It is capped to the dimensions of the screen and
// may not be the actual bounds set. The function will return a zero-sized
// rectangle if the image is not yet initialized.
func (img *Image) Bounds() image.Rectangle {
	if img.sstate != nil {
		img.sstate.Lock()
		defer img.sstate.Unlock()
	}

	return img.imageBounds()
}

// BoundsPx returns the Bounds but in pixels instead of cells.
func (img *Image) BoundsPx() image.Rectangle {
	if img.sstate != nil {
		img.sstate.Lock()
		defer img.sstate.Unlock()
	}

	return img.sstate.rectInPixels(img.imageBounds())
}

// maxBounds returns the bounds for the maximum region.
func (img *Image) maxBounds() image.Rectangle {
	return img.bounds.Intersect(image.Rectangle{Max: img.sstate.cells})
}

// imageBounds returns the bounds for the current image.
func (img *Image) imageBounds() image.Rectangle {
	bounds := img.maxBounds()
	bounds = bounds.Intersect(image.Rectangle{
		Min: bounds.Min,
		Max: bounds.Min.Add(img.currentSz),
	})

	return bounds
}

// resizeImage resizes the src image and updates the internal buffer. It returns
// false if the buffer is not updated.
func (img *Image) resizeImage() bool {
	var resizedImg = img.src

	if img.opts.Resize {
		maxBounds := img.maxBounds()

		// Check if we had the same size as before. Don't bother resizing if
		// yes.
		// TODO: this treats the image as having the same ratio as the region
		// set, which is incorrect!
		if maxBounds.Size() == img.currentSz {
			return false
		}

		img.currentSz = maxBounds.Size()

		rect := img.sstate.rectInPixels(maxBounds)
		size := rect.Size()

		if img.opts.KeepRatio {
			if size.X > size.Y {
				size.X = 0
			} else {
				size.Y = 0
			}
		}

		resizedImg = imaging.Resize(img.src, size.X, size.Y, img.opts.Filter)
	}

	// TODO: optimize for when we draw outside the screen.

	img.buf.Reset()
	img.enc.Encode(resizedImg)

	return true
}

// maxSize returns the maximum size that can fit within the given max width and
// height. Aspect ratio is preserved.
func maxSize(size, max image.Point) image.Point {
	if size.X < max.X && size.Y < max.Y {
		return size
	}

	if size.X > size.Y {
		size.Y = size.Y * (max.X / size.X)
		size.X = max.X
	} else {
		size.X = size.X * (max.Y / size.Y)
		size.Y = max.Y
	}

	return size
}
