package main

import (
	"image"
	"sync"

	"github.com/diamondburned/tcell-sixel/tsixel"
)

// dummyImage is a dummy implementation of a SIXEL image. It is mostly a wrapper
// around the SIXEL byte slice.
type dummyImage struct {
	l sync.Mutex
	p image.Point

	sixel  []byte
	update bool
}

func newDummyImage(sz image.Point) *dummyImage {
	return &dummyImage{
		p: sz,
	}
}

// SetSIXEL sets the internal SIXEL buffer. The caller must not use the given
// byte slice afterwards.
func (dummy *dummyImage) SetSIXEL(b []byte) {
	dummy.l.Lock()
	defer dummy.l.Unlock()

	dummy.sixel = b
	dummy.update = true
}

// Update returns an updated frame.
func (dummy *dummyImage) Update(state tsixel.DrawState) tsixel.Frame {
	dummy.l.Lock()
	defer dummy.l.Unlock()

	update := dummy.update
	dummy.update = false

	return tsixel.Frame{
		SIXEL: dummy.sixel,
		Bounds: image.Rectangle{
			Min: image.Pt(0, 0),
			Max: state.PtInCells(dummy.p),
		},
		MustUpdate: update,
	}
}
