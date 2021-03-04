package tsixel

import (
	"bytes"
	"image"
	"sync"
	"time"

	"github.com/mattn/go-sixel"
	"golang.org/x/image/draw"
)

// ImageOpts represents the options of a SIXEL image. It is meant to be constant
// to each image.
type ImageOpts struct {
	// KeepRatio, if true, will maintain the aspect ratio of the image when it's
	// scaled down to fit the size. The image will be anchored on the top left.
	KeepRatio bool
	// Dither, if true, will apply dithering onto the image.
	Dither bool
	// Scaler determines the scaler to use when scaling. The default is
	// ApproxBiLinear, which is rough but fast. Most of the time, BiLinear
	// should be used.
	Scaler draw.Scaler
}

// imageState is a container for common image properties and synchronizations.
type imageState struct {
	l sync.Mutex

	srcBounds image.Rectangle
	bounds    image.Rectangle // requested region
	currentSz image.Point     // current image size

	sstate ScreenState // screen state

	opts ImageOpts
}

func newImageState(bounds image.Rectangle, opts ImageOpts) imageState {
	return imageState{
		srcBounds: bounds,
		opts:      opts,
	}
}

// SetSize sets the size of the image in units of cells. In other words, it sets
// the bottom-right corner of the image relatively to the top-left corner of the
// image. Note that this merely sets a hint; the actual image will never be
// larger than the screen OR the source image.
func (img *imageState) SetSize(size image.Point) {
	img.l.Lock()
	defer img.l.Unlock()

	img.setSize(size)
}

func (img *imageState) setSize(size image.Point) {
	img.bounds.Max = img.bounds.Min.Add(size)
}

// SetPosition sets the top-left corner of the image in units of cells.
func (img *imageState) SetPosition(pos image.Point) {
	img.l.Lock()
	defer img.l.Unlock()

	img.setPosition(pos)
}

func (img *imageState) setPosition(pos image.Point) {
	old := img.bounds.Min
	img.bounds.Min = pos

	// Recalculate the size for the bounds.
	img.setSize(img.bounds.Max.Sub(old))
}

// Bounds returns the bounds of the image relative to the top-left corner of the
// screen in units of cells. It is capped to the dimensions of the screen and
// may not be the actual bounds set. The function will return a zero-sized
// rectangle if the image is not yet initialized.
func (img *imageState) Bounds() image.Rectangle {
	img.l.Lock()
	defer img.l.Unlock()

	return img.imageBounds()
}

// BoundsPx returns the Bounds but in pixels instead of cells.
func (img *imageState) BoundsPx() image.Rectangle {
	img.l.Lock()
	defer img.l.Unlock()

	return img.sstate.RectInPixels(img.imageBounds())
}

// maxBounds returns the bounds for the maximum region.
func (img *imageState) maxBounds() image.Rectangle {
	return img.bounds.Intersect(image.Rectangle{Max: img.sstate.Cells})
}

// imageBounds returns the bounds for the current image.
func (img *imageState) imageBounds() image.Rectangle {
	bounds := img.maxBounds()
	bounds = bounds.Intersect(image.Rectangle{
		Min: bounds.Min,
		Max: bounds.Min.Add(img.currentSz),
	})

	return bounds
}

// updateSize updates the internal size. An empty rectangle is returned if the
// size is unchanged.
func (img *imageState) updateSize(state ScreenState, sync bool) (image.Rectangle, bool) {
	img.sstate = state

	// Recalculate the new image size in cells.
	imgSize := img.maxBounds().Size()

	if img.opts.KeepRatio {
		// This does not translate over as they're in different units, but the
		// lowest common denominator is what we're using, so it's fine.
		sizeCs := state.RectInCells(img.srcBounds)
		imgSize = maxSize(sizeCs.Size(), imgSize)
	}

	// Check if we had the same size as before. Don't bother resizing if
	// yes.
	// TODO: this treats the image as having the same ratio as the region
	// set, which is incorrect!
	if !sync && imgSize == img.currentSz {
		return img.imageBounds(), false
	}

	// Update the image size.
	img.currentSz = imgSize
	imgBounds := img.imageBounds()

	return imgBounds, true
}

// Image represents a SIXEL image. This image holds the source image and resizes
// it as needed. Each image has its own buffer and its associated encoder. To
// set its boundaries, use the SetBounds method. Note that the setter methods
// don't update the screen; the caller must manually synchronize it.
//
// An image is not thread-safe, so it is not safe to share it across multiple
// screens, even with the same dimensions. This is because the synchronization
// of an image entirely depends on the screen it is on.
type Image struct {
	imageState
	currentSize image.Point

	src image.Image
	enc *sixel.Encoder
	buf *bytes.Buffer
}

// NewImage creates a new SIXEL image from the given image.
func NewImage(img image.Image, opts ImageOpts) *Image {
	buf := bytes.Buffer{}
	buf.Grow(SIXELBufferSize)

	enc := sixel.NewEncoder(&buf)
	enc.Dither = opts.Dither

	return &Image{
		src: img,
		enc: enc,
		buf: &buf,

		imageState: newImageState(img.Bounds(), opts),
	}
}

// Update updates the image's state to the given screen, resizes the src image,
// and updates the internal buffer. It implements the Imager interface.
func (img *Image) Update(state ScreenState, sync bool, now time.Time) Frame {
	img.l.Lock()
	defer img.l.Unlock()

	rect, redraw := img.updateSize(state, sync)
	if !redraw {
		return Frame{
			Bounds: img.imageBounds(),
			SIXEL:  img.buf.Bytes(),
		}
	}

	rectPx := img.sstate.RectInPixels(rect)
	sizePx := rectPx.Size()

	resizedRect := image.Rectangle{Max: sizePx}

	resizedImg := image.NewRGBA(resizedRect)
	img.opts.Scaler.Scale(resizedImg, resizedRect, img.src, img.srcBounds, draw.Over, nil)

	img.buf.Reset()
	img.enc.Encode(resizedImg)

	return Frame{
		Bounds:     rect,
		SIXEL:      img.buf.Bytes(),
		MustUpdate: true,
	}
}
