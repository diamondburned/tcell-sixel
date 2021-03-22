package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"io"
	"math"
	"sync"
	"time"

	"github.com/ericpauley/go-quantize/quantize"
	"github.com/mattn/go-sixel"
	"golang.org/x/image/draw"
)

const defaultBufferSize = 50 * 1024 // 50KB

type pipelineProps struct {
	scale     float64
	width     int
	height    int
	colors    int
	quantizer quantize.MedianCutQuantizer

	reader io.Reader
	tickFq time.Duration
	nproc  int
	errCh  chan<- error
}

type readPipelineState struct {
	props pipelineProps
	readN <-chan int
	pause <-chan bool
	ready <-chan pipelineReady
}

type pipelineState struct {
	props    pipelineProps
	readN    chan<- int
	pause    chan<- bool
	sixel    chan<- []byte
	finished <-chan workerFinished
}

type workerState struct {
	props    pipelineProps
	ready    chan<- pipelineReady
	finished chan<- workerFinished
}

// pipelineReady is sent from a worker through the job channel to the main
// pipeline to signal that it is ready to process more work.
type pipelineReady struct {
	// src is the backing image array for the main pipeline to read into.
	src *image.RGBA
	// done is sent the frame number when the job is refilled.
	done chan<- int
}

// workerFinished is sent from a worker through the job channel to the main
// pipeline to signal that it has already processed the requested pipelineReady.
type workerFinished struct {
	// sixel is the new sixel bytes. It MUST be a copy that the worker will not
	// touch.
	sixel []byte
	frame int
}

// startPipeline starts the SIXEL pipeline. The returned channel is sent slices
// of SIXEL bytes that the consumer can directly use without synchronizing. The
// channel is closed once the pipeline stops.
func startPipeline(ctx context.Context, props pipelineProps) (<-chan []byte, func()) {
	newCtx, cancel := context.WithCancel(ctx)

	jobCh := make(chan pipelineReady, props.nproc)
	finCh := make(chan workerFinished, props.nproc)
	sixCh := make(chan []byte, 1) // buffered to allow missynchronization
	readN := make(chan int, 1)    // read frames
	pause := make(chan bool)

	wg := sync.WaitGroup{}
	wg.Add(2 + props.nproc)

	go pipelineMain(newCtx, &wg, pipelineState{
		props:    props,
		pause:    pause,
		readN:    readN,
		sixel:    sixCh,
		finished: finCh,
	})

	go pipelineRead(newCtx, &wg, readPipelineState{
		props: props,
		pause: pause,
		readN: readN,
		ready: jobCh,
	})

	for i := 0; i < props.nproc; i++ {
		go pipelineWorker(newCtx, &wg, workerState{
			props:    props,
			ready:    jobCh,
			finished: finCh,
		})
	}

	return sixCh, func() {
		cancel()
		wg.Wait()
	}
}

func pipelineRead(ctx context.Context, wg *sync.WaitGroup, state readPipelineState) {
	defer wg.Done()

	// framesWritten is the number of frames written into the map. It is
	// specific to this routine.
	framesWritten := 0

	// often synced with pipelineRead.
	framesRead := 0

	// Copy the ready channel which is used to distribute new jobs. We don't
	// want to distribute more jobs when we already have too many. Start the
	// loop with this channel working, since we start with 0 jobs.
	readyCh := state.ready

	for {
		select {
		case <-ctx.Done():
			return

		case read := <-state.readN:
			framesRead = read

		case pause := <-state.pause:
			if pause {
				readyCh = nil
			} else {
				readyCh = state.ready
			}

		case job := <-readyCh:
			var read = true

			for read {
				_, err := io.ReadFull(state.props.reader, job.src.Pix)
				if err != nil {
					if err != io.EOF {
						state.props.errCh <- err
					}

					// Job done. Exit.
					return
				}

				read = framesRead > framesWritten
				framesWritten++
			}

			select {
			case <-ctx.Done():
				return
			case job.done <- framesWritten - 1:
				continue
			}
		}
	}
}

func pipelineMain(ctx context.Context, wg *sync.WaitGroup, state pipelineState) {
	defer wg.Done()
	// Signal the consumer that main is exiting on return.
	defer close(state.sixel)

	// Copy the SIXEL channel so we can pause it in the loop. Start the loop
	// with the channel paused for now.
	var sixelCh chan<- []byte
	var sixel []byte // frame to be distributed to sixelCh

	// bufferDuration controls the length of the frame buffer in time.
	const bufferDuration = 10 * time.Second

	// Define the threshold until which we must hold back distribution.
	bufferLength := int(bufferDuration / state.props.tickFq)

	// framesRead is the number of frames that were written through
	// props.write().
	framesRead := 0

	// readSyncCh periodically attempts to synchronize the IO routine for missed
	// frames.
	var readSyncCh chan<- int

	// frames contains the undrawn frames. Drawn frames are immediately
	// deleted from the map, and the buffer is reused.
	frames := make(map[int][]byte, bufferLength)

	// pausingCh keeps track if a pausable pause channel (oh boy).
	var pausingCh chan<- bool
	var paused bool // state of above

	frameTicker := time.NewTicker(state.props.tickFq)
	defer frameTicker.Stop()

	// TODO: this pipeline falls behind very easily. Old frames should be kept
	// and displayed on the screen, even if it's behind the ticker.

	for {
		select {
		case <-ctx.Done():
			return

		case pausingCh <- paused:
			pausingCh = nil

		case readSyncCh <- framesRead:
			readSyncCh = nil

		case finished := <-state.finished:
			// Drop the frame if it's too late to be drawn.
			if finished.frame < framesRead {
				continue
			}

			// If the consumer isn't busy being sent a different frame (i.e.
			// it's free), and we have the frame that it wants and it's missing
			// frames, then send it.
			if finished.frame == framesRead {
				sixel = finished.sixel
				sixelCh = state.sixel
			} else {
				// Store the frame for later.
				frames[finished.frame] = finished.sixel
			}

			// Do we have too many jobs? Pause distribution if yes, but only if
			// we have a frame still waiting to be sent, otherwise everything
			// will deadlock.
			if len(frames) >= bufferLength {
				paused = true
				pausingCh = state.pause
			}

		case <-frameTicker.C:
			// Don't bother if we don't have any frames, because we want the
			// workers to catch up. Pause incrementing at all and sacrifice
			// timer accuracy.
			//
			// TODO: ideally, we'd able to guess the frames we need to start
			// ahead to process in time.
			if len(frames) == 0 {
				continue
			}

			// Pretend we've read a frame.
			framesRead++

			// Queue the IO routine to synchronize the number.
			readSyncCh = state.readN

			// Mark this as the frame to be sent if we have it. Delegate it
			// later otherwise (see above routine).
			frame, ok := frames[framesRead]
			if !ok {
				continue
			}

			sixel = frame
			sixelCh = state.sixel
			delete(frames, framesRead)

			// We're probably below the job threshold now, so restore the ready
			// channel if it's not restored.
			if paused {
				paused = false
				pausingCh = state.pause
			}

		case sixelCh <- sixel:
			// Pretend we're done.
			sixel = nil
			sixelCh = nil
		}
	}
}

func pipelineWorker(ctx context.Context, wg *sync.WaitGroup, state workerState) {
	defer wg.Done()

	signal := make(chan int)
	ticket := pipelineReady{
		src:  image.NewRGBA(image.Rect(0, 0, state.props.width, state.props.height)),
		done: signal,
	}

	sixBuf := bytes.Buffer{}
	sixBuf.Grow(defaultBufferSize)

	sixEnc := sixel.NewEncoder(&sixBuf)
	sixEnc.Dither = false

	scaledRt := image.Rect(
		0, 0,
		int(math.Round(state.props.scale*float64(state.props.width))),
		int(math.Round(state.props.scale*float64(state.props.height))),
	)

	paletted := image.NewPaletted(scaledRt, newEmptyPalette(state.props.colors))

	var scaled *image.RGBA
	if state.props.scale != 1 {
		scaled = image.NewRGBA(scaledRt)
	}

	var finished workerFinished

	for {
		select {
		case <-ctx.Done():
			return
		case state.ready <- ticket:
			select {
			case <-ctx.Done():
				return
			case finished.frame = <-signal:

			}
		}

		srcImage := ticket.src

		// Quantize the palette before scaling.
		paletted.Palette = state.props.quantizer.Quantize(paletted.Palette[:0], srcImage)

		if scaled != nil {
			draw.ApproxBiLinear.Scale(
				scaled, scaled.Bounds(),
				srcImage, srcImage.Bounds(), draw.Src, nil,
			)

			srcImage = scaled
		}

		// Dither the image with the new palette.
		if !dither {
			draw.Draw(paletted, paletted.Bounds(), srcImage, image.Point{}, draw.Src)
		} else {
			draw.FloydSteinberg.Draw(paletted, paletted.Bounds(), srcImage, image.Point{})
		}

		sixBuf.Reset()
		sixEnc.Encode(paletted)

		finished.sixel = append([]byte(nil), sixBuf.Bytes()...)

		select {
		case <-ctx.Done():
			return
		case state.finished <- finished:
			finished.sixel = nil
		}
	}
}

func newEmptyPalette(colors int) color.Palette {
	alpha := color.Alpha{}
	palet := make(color.Palette, colors)
	for i := range palet {
		palet[i] = alpha
	}
	return palet
}
