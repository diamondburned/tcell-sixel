package tsixel

import (
	"image"
	"sync"
)

// StaticImage provides the most simple implementation to draw a SIXEL image. It
// provides no resizing.
type StaticImage struct {
	l sync.Mutex

	src image.Image
	buf []byte
	upd bool // used to trigger redraw, not re-render SIXEL

	// use our own encoder to save a copy
	encBuf pooledEncoder

	imgPos image.Point
	cellSz image.Point
}

// NewStaticImage creates a new static image from the given image.
func NewStaticImage(src image.Image) *StaticImage {
	return NewStaticImageCustom(src, false, 0)
}

// NewStaticImageCustom creates a new static image with custom encoder
// parameters. Colors can be in-between 2 and 255.
func NewStaticImageCustom(src image.Image, dither bool, colors int) *StaticImage {
	static := StaticImage{
		src:    src,
		encBuf: newPooledEncoder(50 * 1024), // 50KB
	}

	static.encBuf.Encoder.Colors = colors
	static.encBuf.Encoder.Dither = dither

	return &static
}

// SetImage sets a new image. The image is automatically resized in the method,
// but a redraw will not be triggered.
func (static *StaticImage) SetImage(src image.Image) {
	static.l.Lock()
	defer static.l.Unlock()

	static.setImage(src)
}

func (static *StaticImage) setImage(src image.Image) {
	static.src = src

	// Render right here if we have the screen state.
	if static.cellSz != (image.Point{}) {
		static.updateSIXEL()
	}
}

func (static *StaticImage) updateSIXEL() {
	static.encBuf.buf.Reset()
	static.encBuf.Encode(static.src)
	static.buf = static.encBuf.buf.Bytes()
	static.upd = true
}

// SetPosition sets the image position.
func (static *StaticImage) SetPosition(pt image.Point) {
	static.l.Lock()
	defer static.l.Unlock()

	static.imgPos = pt
	static.upd = true
}

// Bounds returns the current bounds of the static image in cells. It works
// similarly to Image's Bounds.
func (static *StaticImage) Bounds() image.Rectangle {
	static.l.Lock()
	defer static.l.Unlock()

	return static.bounds()
}

func (static *StaticImage) bounds() image.Rectangle {
	return image.Rectangle{
		Min: static.imgPos,
		Max: static.imgPos.Add(ptInCells(static.cellSz, static.src.Bounds().Size())),
	}
}

// Update returns the current SIXEL data. It
func (static *StaticImage) Update(state DrawState) Frame {
	static.l.Lock()
	defer static.l.Unlock()

	newCell := state.CellSize()
	changed := static.cellSz != newCell || static.buf == nil

	if changed {
		static.cellSz = newCell
		static.updateSIXEL()
	}

	// Only account for static.upd after we update the SIXEL, since upd is not
	// used for redrawing SIXELs.
	changed = changed || static.upd

	return Frame{
		SIXEL:      static.buf,
		Bounds:     static.bounds(),
		MustUpdate: changed,
	}
}
