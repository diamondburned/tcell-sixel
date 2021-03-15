package tsixel

import (
	"bytes"
	"image"
	"sync"

	"github.com/mattn/go-sixel"
	"golang.org/x/image/draw"
)

// ImageOpts represents the options of a SIXEL image. It is meant to be constant
// to each image.
type ImageOpts struct {
	// Scaler determines the scaler to use when scaling. Most of the time,
	// ApproxBiLinear should be used.
	//
	// If Scaler is nil, then the image is never resized.
	Scaler draw.Scaler
	// KeepRatio, if true, will maintain the aspect ratio of the image when it's
	// scaled down to fit the size. The image will be anchored on the top left.
	KeepRatio bool
	// Dither, if true, will apply dithering onto the image.
	Dither bool
	// NoRounding disables SIXEL rounding. This is useful if the image sizes
	// are dynamically calculated manually and are expected to be consistent.
	NoRounding bool
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

	sstate DrawState // screen state
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
	size := img.bounds.Size()
	img.bounds.Min = pos
	img.bounds.Max = img.bounds.Min.Add(size)
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

// RequestedBounds calculates the bounds similarly to Bounds(), except the
// returned size is the one requested, not the one that's rescaled. This is
// useful for calculations relative to the corners of the screen.
func (img *imageState) RequestedBounds() image.Rectangle {
	img.l.Lock()
	defer img.l.Unlock()

	return img.bounds
}

// BoundsPx returns the Bounds but in pixels instead of cells.
func (img *imageState) BoundsPx() image.Rectangle {
	img.l.Lock()
	defer img.l.Unlock()

	return img.sstate.RectInPixels(img.imageBounds(), !img.opts.NoRounding)
}

// maxBounds returns the bounds for the maximum region.
func (img *imageState) maxBounds() image.Rectangle {
	// Don't draw the image touching the screen border to prevent weird
	// wrapping if we're rounding for SIXEL. Most applications that need SIXEL
	// rounding would also require strict positioning, and that means no
	// wrapping over, so we use that condition.
	var offset image.Point
	if !img.opts.NoRounding {
		offset = image.Pt(4, 2)
	}

	return img.bounds.Intersect(image.Rectangle{
		Max: img.sstate.Cells.Sub(offset),
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
func (img *imageState) updateSize(state DrawState) bool {
	img.sstate = state

	// Recalculate the new image size in pixels.
	newImgRtPx := state.RectInPixels(img.maxBounds(), !img.opts.NoRounding)

	if img.opts.KeepRatio {
		newImgRtPx.Max = newImgRtPx.Min.Add(maxSize(img.srcSize, newImgRtPx.Size()))
	}

	// Check if we had the same size as before. Since we try to keep the aspect
	// ratio, we could check if both points have a common equal size. Don't
	// bother resizing if yes.
	if ptOverlapOneSide(img.imgPixels, newImgRtPx.Size()) {
		return false
	}

	// Update the image size.
	img.imgPixels = newImgRtPx.Size()
	img.imgCells = state.RectInCells(newImgRtPx).Size()

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
	dst image.Image // post-resize
	buf []byte

	imageState
	currentSize image.Point

	// use for drawing after async resize
	updated bool
}

// NewImage creates a new SIXEL image from the given image.
func NewImage(img image.Image, opts ImageOpts) *Image {
	buf := bytes.Buffer{}
	buf.Grow(SIXELBufferSize)

	enc := sixel.NewEncoder(&buf)
	enc.Dither = opts.Dither

	return &Image{
		src:        img,
		imageState: newImageState(img.Bounds().Size(), opts),
	}
}

// Update updates the image's state to the given screen, resizes the src image,
// and updates the internal buffer. It implements the Imager interface.
func (img *Image) Update(state DrawState) Frame {
	img.l.Lock()
	defer img.l.Unlock()

	updated := img.updated
	img.updated = false

	frame := Frame{
		Bounds:     img.imageBounds(),
		SIXEL:      img.buf,
		MustUpdate: state.Sync || updated,
	}

	if !img.updateSize(state) {
		return frame
	}

	resizerMain.QueueJob(ResizerJob{
		SrcImg:  img.src,
		Options: img.opts,
		NewSize: img.imgPixels,

		Done: func(job ResizerJob, out []byte) {
			img.l.Lock()

			// Ensure this is the latest geometry.
			if job.NewSize != img.imgPixels {
				img.l.Unlock()
				return
			}

			img.buf = out
			img.updated = true

			img.l.Unlock()

			state.Delegate()
		},
	})

	return frame
}

// ptOverlapOneSide returns true if one side of p1 equals to p2.
func ptOverlapOneSide(p, bound image.Point) bool {
	return (p.X == bound.X && p.Y <= bound.Y) || (p.Y == bound.Y && p.X <= bound.X)
}

// maxSize returns the maximum size that can fit within the given max width and
// height. Aspect ratio is preserved.
func maxSize(size, max image.Point) image.Point {
	original := size

	// Code ported from https://stackoverflow.com/a/10245583.

	if original.X > max.X {
		size.X = max.X
		size.Y = original.Y * size.X / original.X
	}
	if size.Y > max.Y {
		size.Y = max.Y
		size.X = original.X * size.Y / original.Y
	}

	return size
}

// ceilDiv performs the division operation such that a is divided by b. The
// result is rounded up (ceiling) instead of rounded down (floor).
func ceilDiv(a, b int) int {
	return (a + b - 1) / b
}
