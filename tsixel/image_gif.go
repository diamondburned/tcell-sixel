package tsixel

import (
	"image"
	"image/gif"
	"time"
)

type Animation struct {
	gif      *gif.GIF
	frames   []animationFrame
	lastTime time.Time // last drawn time

	imageState

	redraw  bool
	frameIx int // frame index
	loopedN int // number of times looped
}

type animationFrame struct {
	sixel []byte
	size  image.Point
}

func NewAnimation(gif *gif.GIF, opts ImageOpts) *Animation {
	return &Animation{
		gif:        gif,
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

func (anim *Animation) Update(state DrawState) Frame {
	anim.l.Lock()
	defer anim.l.Unlock()

	lastFrame := anim.frameIx
	anim.seekFrames(state.Time)

	redraw := anim.redraw
	anim.redraw = false

	// update redraw state.
	if !redraw {
		redraw = lastFrame != anim.frameIx
	}

	frameSIXEL := &anim.frames[anim.frameIx]

	anim.updateSize(state)

	if frameSIXEL.sixel == nil || frameSIXEL.size != anim.imgPixels {
		// Mark redraw.
		redraw = true
		// Clear out the old SIXEL.
		frameSIXEL.sixel = nil

		// Update the size directly.
		frameSIXEL.size = anim.imgPixels

		resizerMain.QueueJob(ResizerJob{
			SrcImg:  anim.gif.Image[anim.frameIx],
			Options: anim.opts,
			NewSize: frameSIXEL.size,

			Done: func(job ResizerJob, out []byte) {
				anim.l.Lock()

				// Ensure this is the latest geometry.
				if job.NewSize != frameSIXEL.size {
					anim.l.Unlock()
					return
				}

				// Update the internal SIXEL directly and mark for redrawing.
				frameSIXEL.sixel = out
				anim.redraw = true

				anim.l.Unlock()

				state.Delegate()
			},
		})
	}

	return Frame{
		Bounds:     anim.imageBounds(),
		SIXEL:      frameSIXEL.sixel,
		MustUpdate: redraw,
	}
}
