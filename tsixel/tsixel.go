// Package tsixel provides abstractions to work with SIXEL images in tcell.
package tsixel

import (
	"errors"
	"image"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
)

// CharPt returns a new point with twice the given columns. It's a convenient
// function to properly scale images by making the assumption that 2 cells make
// a square.
func CharPt(cols, rows int) image.Point {
	return image.Pt(cols*2, rows)
}

// TODO: implement MaxResizeTime.

// MaxResizeTime is the duration to wait since the last resize to try resizing
// images again. It is only useful for images with resizing enabled.
const MaxResizeTime = 500 * time.Millisecond

// SIXELBufferSize is the size of the pre-allocated SIXEL buffer.
const SIXELBufferSize = 40960 // 40KB

// Errors returned if the tcell screen does not have the capabilities for SIXEL.
var (
	ErrNoDrawInterceptor = errors.New("screen does not support draw interceptors")
	ErrNoPixelDimensions = errors.New("screen does not support pixel dimensions")
	ErrNoDirectDrawer    = errors.New("screen does not support direct drawer")

	// ErrNoExplicitSync is returned if a screen does not implement sync.Locker.
	// This is needed to explicitly sync our own internal state with the screen.
	ErrNoExplicitSync = errors.New("screen does not allow explicit syncing")
)

// Screen wraps around a tcell screen to manage and draw visible SIXEL images.
type Screen struct {
	s tcell.Screen
	l sync.Locker

	images map[Imager]*drawnImage
	sstate ScreenState
}

// Imager represents an image interface.
type Imager interface {
	// UpdateSize updates the image's sizes. After this method is called, the
	// image must be synchronized using the given state. If Update returns true,
	// then the screen will redraw the SIXEL.
	Update(state ScreenState, sync bool, now time.Time) Frame
}

// Frame is a representation of the image frame after an update.
type Frame struct {
	// SIXEL is the byte slice to the raw SIXEL data of the image. The slice
	// must only be changed when Update is called.
	SIXEL []byte
	// Bounds is the current image size and position on the screen in units of
	// cells.
	Bounds image.Rectangle
	// MustUpdate, if true, will force the screen to redraw the SIXEL. The
	// screen may still redraw the SIXEL if this is false.
	MustUpdate bool
}

// drawnImage is a stateful image wrapper for damage tracking.
type drawnImage struct {
	Imager
	frame Frame
}

// WrapInitScreen wraps around an initialized tcell screen to create a new
// screen with an internal SIXEL state. It returns an error if the screen is not
// capable of outputting SIXEL. Note that this does not check if the terminal
// can draw SIXEL images. This behavior may change in the future.
func WrapInitScreen(s tcell.Screen) (*Screen, error) {
	if _, ok := s.(tcell.DirectDrawer); !ok {
		return nil, ErrNoDirectDrawer
	}

	iceptAdder, ok := s.(tcell.DrawInterceptAdder)
	if !ok {
		return nil, ErrNoDrawInterceptor
	}

	locker, ok := s.(sync.Locker)
	if !ok {
		return nil, ErrNoExplicitSync
	}

	pxsz, ok := s.(tcell.PixelSizer)
	if !ok {
		return nil, ErrNoPixelDimensions
	}

	sstate := ScreenState{
		Cells:  image.Pt(s.Size()),
		Pixels: image.Pt(pxsz.PixelSize()),
	}

	// Confirm that the screen actually supports pixel sizes.
	if sstate.Pixels == image.Pt(0, 0) {
		return nil, ErrNoPixelDimensions
	}

	screen := Screen{
		s:      s,
		l:      locker,
		sstate: sstate,
		images: map[Imager]*drawnImage{},
	}

	iceptAdder.AddDrawIntercept(screen.beforeDraw)
	iceptAdder.AddDrawInterceptAfter(screen.afterDraw)
	return &screen, nil
}

// beforeDraw is responsible for damage tracking.
func (s *Screen) beforeDraw(screen tcell.Screen, sync bool) {
	now := time.Now()

	// Assume sstate's locker is acquired by the caller.

	// Update the screen size, always.
	s.sstate.update(screen)

	viewer, hasCellBuffer := screen.(tcell.CellBufferViewer)

	for _, img := range s.images {
		// TODO: resize before locking to reduce contention. This doesn't really
		// matter.
		img.frame = img.Update(s.sstate, sync, now)

		if sync {
			img.frame.MustUpdate = true
			continue
		}

		// We only check if we need to redraw if we haven't resized. We ALWAYS
		// have to redraw if the image has been resized.
		if !img.frame.MustUpdate && hasCellBuffer {
			r := img.frame.Bounds

			viewer.ViewCellBuffer(func(cb *tcell.CellBuffer) {
				img.frame.MustUpdate = cb.DirtyRegion(r.Min.X, r.Min.Y, r.Max.X, r.Max.Y)
			})
		}
	}
}

// afterDraw is responsible for putting SIXEL images on the screen.
func (s *Screen) afterDraw(screen tcell.Screen, sync bool) {
	drawer, _ := screen.(tcell.DirectDrawer)

	for _, img := range s.images {
		if img.frame.MustUpdate {
			screen.ShowCursor(img.frame.Bounds.Min.X, img.frame.Bounds.Min.Y)
			drawer.DrawDirectly(img.frame.SIXEL)
		}
	}

	screen.HideCursor()
	drawer.DrawDirectly(nil)
}

// AddImage adds a SIXEL image onto the screen. This method will not redraw, so
// the caller should call Sync on the screen.
func (s *Screen) AddImage(img Imager) {
	s.l.Lock()
	defer s.l.Unlock()

	img.Update(s.sstate, false, time.Now())
	s.images[img] = &drawnImage{Imager: img}
}

// AddAnyImage adds any image type onto the screen. It is a convenient wrapper
// around NewImage and AddImage.
func (s *Screen) AddAnyImage(img image.Image, opts ImageOpts) *Image {
	sixel := NewImage(img, opts)
	s.AddImage(sixel)
	return sixel
}

// RemoveImage removes an image from the screen. It does not redraw.
func (s *Screen) RemoveImage(img Imager) {
	s.l.Lock()
	defer s.l.Unlock()

	delete(s.images, img)
}

// ScreenState stores the screen size in two units: cells and pixels.
type ScreenState struct {
	Cells  image.Point
	Pixels image.Point
}

func (sz *ScreenState) update(screen tcell.Screen) {
	sz.Cells.X, sz.Cells.Y = screen.Size()

	pxsz, _ := screen.(tcell.PixelSizer)
	sz.Pixels.X, sz.Pixels.Y = pxsz.PixelSize()
}

// CellSize returns the size of each cell in pixels.
func (sz ScreenState) CellSize() image.Point {
	return image.Point{
		X: sz.Pixels.X / sz.Cells.X,
		Y: sz.Pixels.Y / sz.Cells.Y,
	}
}

// SIXELHeight is the height of a single SIXEL strip.
//
// According to Wikipedia, the free encyclopedia:
//
//    Sixel encodes images by breaking up the bitmap into a series of 6-pixel
//    high horizontal strips.
//
// This suggests that a SIXEL image's height can only be in multiples of 6. We
// must account this fact into consideration when resizing an image to not
// overflow a line when a cell's height is not in multiples of 6.
const SIXELHeight = 6 // px

// PtInPixels converts a point which unit is in cells to pixels.
func (sz ScreenState) PtInPixels(pt image.Point) image.Point {
	cell := sz.CellSize()

	pt.X *= cell.X
	pt.Y *= cell.Y

	return pt
}

// PtInPixelsRounded converts a point which unit is in cells to pixels and
// rounds it to be within the SIXEL multiples.
func (sz ScreenState) PtInPixelsRounded(pt image.Point) image.Point {
	cell := sz.CellSize()

	pt.X *= cell.X
	pt.Y *= cell.Y

	// Round the image down to the proper SIXEL sizes.
	excess := pt.Y % SIXELHeight
	pt.Y -= excess

	// Account for this loss in the width.
	pt.X -= (excess * cell.X) / cell.Y

	return pt
}

// RectInPixels converts a rectangle which unit is in cells into one in pixels.
// It accounts for the cell margins. The returned rectangle is guaranteed to
// have roughly the same aspect ratio.
func (sz ScreenState) RectInPixels(rect image.Rectangle) image.Rectangle {
	rect.Min = sz.PtInPixels(rect.Min)
	rect.Max = sz.PtInPixelsRounded(rect.Max)
	return rect
}

// RectInCells converts a rectangle which unit is in pixels into one in cells.
func (sz ScreenState) RectInCells(rect image.Rectangle) image.Rectangle {
	cell := sz.CellSize()

	rect.Min.X /= cell.X
	rect.Min.Y /= cell.Y

	rect.Max.X /= cell.X
	rect.Max.Y /= cell.Y

	return rect
}
