package tsixel

import (
	"bytes"
	"image"
	"image/gif"
	"time"

	"github.com/mattn/go-sixel"
	"golang.org/x/image/draw"
)

type Animation struct {
	gif *gif.GIF
	enc *sixel.Encoder
	buf *bytes.Buffer

	frames   []animationFrame
	lastTime time.Time // last drawn time

	imageState

	frameIx int // frame index
	loopedN int // number of times looped
}

type animationFrame struct {
	sixel []byte
	size  image.Point
}

func NewAnimation(gif *gif.GIF, opts ImageOpts) *Animation {
	buf := bytes.Buffer{}
	buf.Grow(SIXELBufferSize)

	enc := sixel.NewEncoder(&buf)
	enc.Dither = opts.Dither

	return &Animation{
		gif: gif,
		enc: enc,
		buf: &buf,

		frames:     make([]animationFrame, len(gif.Image)),
		imageState: newImageState(image.Pt(gif.Config.Width, gif.Config.Height), opts),
	}
}

// seekFrames seeks until we're at the current frame.
func (anim *Animation) seekFrames(now time.Time) {
	// Don't do anything if we're already over the draw limit.
	if anim.gif.LoopCount != 0 && anim.loopedN > anim.gif.LoopCount {
		return
	}

	// If this is the first time we're drawing the GIF, then we draw at frame 0.
	if anim.lastTime.IsZero() {
		anim.lastTime = now
	}

	// TODO: optimize this to be in constant time rather than linear.
	for {
		delay := anim.gif.Delay[anim.frameIx] // 100ths of a second

		// Accumulate the delay and the index.
		next := anim.lastTime.Add(gifDelayDuration(delay))
		// Stop accumulating once we've added enough.
		if next.After(now) {
			break
		}

		anim.frameIx++

		// Check if the frame index is out. If it is, reset it.
		if anim.frameIx >= len(anim.gif.Image) {
			anim.frameIx = 0

			// If we're not looping forever, then keep track of the loop.
			if anim.gif.LoopCount != 0 {
				if anim.loopedN++; anim.loopedN > anim.gif.LoopCount {
					break
				}
			}
		}

		anim.lastTime = next
	}
}

// gifDelayDuration converts delay in the unit of 100ths of a second to a
// duration.
func gifDelayDuration(delay int) time.Duration {
	return time.Second / 100 * time.Duration(delay)
}

func (anim *Animation) Update(state ScreenState, sync bool, now time.Time) Frame {
	anim.l.Lock()
	defer anim.l.Unlock()

	lastFrame := anim.frameIx
	anim.seekFrames(now)

	anim.updateSize(state, sync)

	sixelFrame := anim.frames[anim.frameIx]
	if sixelFrame.sixel == nil || sixelFrame.size != anim.imgPixels {
		// Mark redraw.
		sync = true

		// Update the size.
		sixelFrame.size = anim.imgPixels

		frame := anim.gif.Image[anim.frameIx]
		resizedImg := image.NewRGBA(image.Rectangle{Max: anim.imgPixels})
		anim.opts.Scaler.Scale(resizedImg, resizedImg.Rect, frame, frame.Rect, draw.Over, nil)

		anim.buf.Reset()
		anim.enc.Encode(resizedImg)

		// Reallocate a completely new slice if we don't have enough space.
		// Otherwise, reuse it.
		if cap(sixelFrame.sixel) < anim.buf.Len() {
			sixelFrame.sixel = make([]byte, 0, anim.buf.Len())
		} else {
			sixelFrame.sixel = sixelFrame.sixel[:0]
		}

		// Copy the shared buffer so we can reuse it.
		sixelFrame.sixel = append(sixelFrame.sixel, anim.buf.Bytes()...)

		// Save the frame into the slice.
		anim.frames[anim.frameIx] = sixelFrame
	}

	// Ensure that sync is true if the frame is a different one.
	if !sync && lastFrame != anim.frameIx {
		sync = true
	}

	return Frame{
		Bounds:     anim.imageBounds(),
		SIXEL:      sixelFrame.sixel,
		MustUpdate: sync,
	}
}
