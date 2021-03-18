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

type pipelineState struct {
	props    pipelineProps
	sixel    chan<- []byte
	ready    <-chan pipelineReady
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

	wg := sync.WaitGroup{}
	wg.Add(1 + props.nproc)

	go pipelineMain(newCtx, &wg, pipelineState{
		props:    props,
		sixel:    sixCh,
		ready:    jobCh,
		finished: finCh,
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

func pipelineMain(ctx context.Context, wg *sync.WaitGroup, state pipelineState) {
	defer wg.Done()
	// Signal the consumer that main is exiting on return.
	defer close(state.sixel)

	// Copy the SIXEL channel so we can pause it in the loop. Start the loop
	// with the channel paused for now.
	var sixelCh chan<- []byte
	var sixel []byte // frame to be distributed to sixelCh

	// Also copy the ready channel which is used to distribute new jobs. We
	// don't want to distribute more jobs when we already have too many. Start
	// the loop with this channel working, since we start with 0 jobs.
	readyCh := state.ready

	// Define the threshold until which we must hold back distribution.
	const jobThreshold = 512

	// framesRead is the number of frames that were written through
	// props.write().
	framesRead := 0
	// framesWritten is the number of frames written into the map.
	framesWritten := 0
	// frames contains the undrawn frames. Drawn frames are immediately
	// deleted from the map, and the buffer is reused.
	frames := make(map[int][]byte, jobThreshold)

	frameTicker := time.NewTicker(state.props.tickFq)
	defer frameTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case job := <-readyCh:
			// TODO: split this into its own pipeline.
			_, err := io.ReadFull(state.props.reader, job.src.Pix)
			if err != nil {
				state.props.errCh <- err
				return
			}

			select {
			case <-ctx.Done():
				return
			case job.done <- framesWritten:
				framesWritten++
			}

		case finished := <-state.finished:
			frames[finished.frame] = finished.sixel

			// Do we have too many jobs? Pause distribution if yes.
			if len(frames) > jobThreshold {
				readyCh = nil
			}

		case <-frameTicker.C:
			// Confirm that we have the frame that the consumer wants. Skip if
			// not.
			if frame, ok := frames[framesRead]; ok {
				// Schedule to send the frame over.
				sixel = frame
				sixelCh = state.sixel

				// Delete the frame off the map, so we don't set the above
				// variables too many times.
				delete(frames, framesRead)
			}

		case sixelCh <- sixel:
			// Mark the frame as read.
			framesRead++

			// Pause distribution and reset the frame; we'll reset it elsewhere
			// when we have more results.
			sixel = nil
			sixelCh = nil

			// We're probably below the job threshold now, so restore the ready
			// channel if it's not restored.
			readyCh = state.ready
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
			draw.FloydSteinberg.Draw(paletted, srcImage.Bounds(), srcImage, image.Point{})
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
