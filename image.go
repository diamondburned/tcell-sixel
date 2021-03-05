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
	// Scaler determines the scaler to use when scaling. The default is
	// ApproxBiLinear, which is rough but fast. Most of the time, BiLinear
	// should be used.
	Scaler draw.Scaler
	// KeepRatio, if true, will maintain the aspect ratio of the image when it's
	// scaled down to fit the size. The image will be anchored on the top left.
	KeepRatio bool
	// Dither, if true, will apply dithering onto the image.
	Dither bool
}

// imageState is a container for common image properties and synchronizations.
type imageState struct {
	opts ImageOpts
	l    sync.Mutex

	bounds  image.Rectangle // requested region
	srcSize image.Point     // source image size in pixels

	// current image sizes. Pixels are most accurate, and cells are only
	// converted in the last stage.
	imgCells  image.Point
	imgPixels image.Point

	sstate ScreenState // screen state
}

func newImageState(srcSize image.Point, opts ImageOpts) imageState {
	return imageState{
		srcSize: srcSize,
		opts:    opts,
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
	// Don't draw the image touching the screen border to prevent weird
	// wrapping.
	return img.bounds.Intersect(image.Rectangle{
		Max: img.sstate.Cells.Sub(image.Pt(4, 2)),
	})
}

// imageBounds returns the bounds for the current image.
func (img *imageState) imageBounds() image.Rectangle {
	return image.Rectangle{
		Min: img.bounds.Min,
		Max: img.bounds.Min.Add(img.imgCells),
	}
}

// updateSize updates the internal size. An empty rectangle is returned if the
// size is unchanged.
func (img *imageState) updateSize(state ScreenState, sync bool) (redraw bool) {
	img.sstate = state

	// Recalculate the new image size in cells.
	newImgRect := img.maxBounds()
	// Convert cells into pixels so we have more accuracy.
	newImgRtPx := state.RectInPixels(newImgRect)

	if img.opts.KeepRatio {
		newImgRtPx.Max = newImgRtPx.Min.Add(maxSize(img.srcSize, newImgRtPx.Size()))
		newImgRect = state.RectInCells(newImgRtPx)
	}

	// Check if we had the same size as before. Since we try to keep the aspect
	// ratio, we could check if both points have a common equal size. Don't
	// bother resizing if yes.
	if !sync && ptOverlapOneSide(newImgRtPx.Size(), img.imgPixels) {
		return false
	}

	// Update the image size.
	img.imgCells = newImgRect.Size()
	img.imgPixels = newImgRtPx.Size()

	return true
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
	src image.Image
	enc *sixel.Encoder
	buf *bytes.Buffer

	imageState
	currentSize image.Point
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

		imageState: newImageState(img.Bounds().Size(), opts),
	}
}

// Update updates the image's state to the given screen, resizes the src image,
// and updates the internal buffer. It implements the Imager interface.
func (img *Image) Update(state ScreenState, sync bool, now time.Time) Frame {
	img.l.Lock()
	defer img.l.Unlock()

	if !img.updateSize(state, sync) {
		return Frame{
			Bounds: img.imageBounds(),
			SIXEL:  img.buf.Bytes(),
		}
	}

	resizedRect := image.Rectangle{Max: img.imgPixels}

	resizedImg := image.NewRGBA(resizedRect)
	img.opts.Scaler.Scale(resizedImg, resizedRect, img.src, img.src.Bounds(), draw.Over, nil)

	img.buf.Reset()
	img.enc.Encode(resizedImg)

	return Frame{
		Bounds:     img.imageBounds(),
		SIXEL:      img.buf.Bytes(),
		MustUpdate: true,
	}
}

// ptOverlapOneSide returns true if one side of p1 equals to p2.
func ptOverlapOneSide(p1, p2 image.Point) bool {
	return p1.X == p2.X || p1.Y == p2.Y
}

// maxSize returns the maximum size that can fit within the given max width and
// height. Aspect ratio is preserved.
func maxSize(size, max image.Point) image.Point {
	if size.X < size.Y {
		size.Y = ceilDiv(size.Y*max.X, size.X)
		size.X = max.X
	} else {
		size.X = ceilDiv(size.X*max.Y, size.Y)
		size.Y = max.Y
	}

	return size
}

// ceilDiv performs the division operation such that a is divided by b. The
// result is rounded up (ceiling) instead of rounded down (floor).
func ceilDiv(a, b int) int {
	return (a + b - 1) / b
}
