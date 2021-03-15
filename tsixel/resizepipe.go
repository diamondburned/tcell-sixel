package tsixel

import (
	"bytes"
	"context"
	"image"
	"runtime"
	"sync"
	"time"

	"github.com/mattn/go-sixel"
	"golang.org/x/image/draw"
)

var (
	resizerOnce sync.Once
	resizerMain ResizePipeline
)

func init() {
	resizerMain = *NewResizePipeline()
	resizerMain.Start()
}

type ResizePipeline struct {
	// state
	queue   []*ResizerJob
	pool    *encoderPool
	workers int

	// BatchDuration is the duration from the first image (after the last batch)
	// to accumulate before refreshing screen.
	//
	// The default is 15th of a second.
	batchDuration time.Duration

	// MaxWorkers is the maximum number of workers to spawn.
	//
	// The default is GOMAXPROCS.
	maxWorkers int

	// channels
	dieCh     chan struct{} // worker death signals
	msgCh     chan resizePipelineMessage
	jobCh     chan *ResizerJob // job queue
	finishCh  chan *ResizerJob
	distribCh chan *ResizerJob // job distribute

	// clean up bits
	sctx context.Context
	stop context.CancelFunc
	done sync.WaitGroup
}

// ResizerJob describes a resizing job. The resize pipeline will batch up jobs,
// resize them asynchronously, and call the screen once it's done.
type ResizerJob struct {
	Done func(ResizerJob, []byte)

	SrcImg image.Image

	Options ImageOpts
	NewSize image.Point
}

// resizePipelineMessage is an arbitrary message for the resize pipeline.
type resizePipelineMessage struct {
	BatchDuration time.Duration
	MaxWorkers    int
}

func NewResizePipeline() *ResizePipeline {
	return NewResizePipelineContext(context.Background())
}

// NewResizePipelineContext creates a new resize pipeline with the given
// context. Once the context is canceled
func NewResizePipelineContext(ctx context.Context) *ResizePipeline {
	ctx, cancel := context.WithCancel(ctx)

	return &ResizePipeline{
		batchDuration: time.Second / 15,
		maxWorkers:    runtime.GOMAXPROCS(-1),

		dieCh:     make(chan struct{}),
		msgCh:     make(chan resizePipelineMessage),
		jobCh:     make(chan *ResizerJob),
		distribCh: make(chan *ResizerJob),

		pool: newEncoderPool(),
		sctx: ctx,
		stop: cancel,
	}
}

func MainResizePipeline() *ResizePipeline {
	return &resizerMain
}

// Start starts the pipeline. It does nothing if the pipeline is already
// stopped.
func (pipeline *ResizePipeline) Start() {
	select {
	case <-pipeline.sctx.Done():
		return
	default:
		pipeline.done.Add(1)
		go pipeline.start()
	}
}

// Stop stops the pipeline. It does nothing if the pipeline is already stopped.
func (pipeline *ResizePipeline) Stop() {
	pipeline.stop()
	pipeline.done.Wait()
}

func (pipeline *ResizePipeline) start() {
	// TODO: batch and optimize

	var distributeJob *ResizerJob
	var distributeCh chan *ResizerJob

	for {
		select {
		case <-pipeline.sctx.Done():
			return

		case <-pipeline.dieCh:
			pipeline.workers--
			if pipeline.workers < 0 {
				panic("negative pipeline.workers")
			}

		case msg := <-pipeline.msgCh:
			if msg.MaxWorkers > 0 {
				pipeline.maxWorkers = msg.MaxWorkers
			}
			if msg.BatchDuration > 0 {
				pipeline.batchDuration = msg.BatchDuration
			}

		case job := <-pipeline.jobCh:
			distributeCh = pipeline.distribCh

			// Append into an unbounded queue if we already have a job.
			// Otherwise, use it immediately.
			if distributeJob != nil {
				pipeline.queue = append(pipeline.queue, job)
			} else {
				distributeJob = job
			}

			if pipeline.workers < pipeline.maxWorkers {
				pipeline.workers++

				go resizeWorker(pipeline.sctx, worker{
					pool:    pipeline.pool,
					distrib: pipeline.distribCh,
					die:     pipeline.dieCh,
				})
			}

		case distributeCh <- distributeJob:
			// Mark job as empty.
			distributeJob = nil

			// Stop sending jobs if we're out of them.
			if len(pipeline.queue) == 0 {
				distributeCh = nil
				continue
			}

			// Rotate to the next job in FIFO order.
			distributeJob = pipeline.queue[0]

			// Pop the rotated job off.
			copy(pipeline.queue, pipeline.queue[1:])                // shift leftwards
			pipeline.queue[len(pipeline.queue)-1] = nil             // invalidate last
			pipeline.queue = pipeline.queue[:len(pipeline.queue)-1] // pop last
		}
	}
}

// QueueJob queues a resizing job. If a job with the same Imager is already
// queued, then its size is updated and the callback is preserved.
func (pipeline *ResizePipeline) QueueJob(job ResizerJob) {
	select {
	case <-pipeline.sctx.Done():
		// failed
	case pipeline.jobCh <- &job:
		// succeeded
	}
}

type worker struct {
	pool *encoderPool

	distrib chan *ResizerJob
	die     chan struct{}
}

func resizeWorker(ctx context.Context, w worker) {
EventLoop:
	for {
		select {
		case <-ctx.Done():
			return

		case job := <-w.distrib:
			dstImg := image.NewRGBA(image.Rectangle{Max: job.NewSize})

			// Clip the new image if we don't scale. Otherwise, scale the image
			// onto the new one as usual.
			if job.Options.Scaler == nil {
				draw.Draw(
					dstImg, dstImg.Bounds(),
					job.SrcImg, image.Pt(0, 0), draw.Over,
				)
			} else {
				job.Options.Scaler.Scale(
					dstImg, dstImg.Bounds(),
					job.SrcImg, job.SrcImg.Bounds(), draw.Over, nil,
				)
			}

			enc := w.pool.take()

			enc.Dither = job.Options.Dither
			enc.Encode(dstImg)

			bytes := enc.Bytes()

			w.pool.put(enc)

			job.Done(*job, bytes)

		default:
			break EventLoop
		}
	}

	// signal the worker's death and bail
	select {
	case <-ctx.Done(): // beware of expiry
	case w.die <- struct{}{}:
	}

	return
}

type encoderPool sync.Pool

type pooledEncoder struct {
	*sixel.Encoder
	buf *bytes.Buffer
}

func (enc pooledEncoder) Bytes() []byte {
	return append([]byte(nil), enc.buf.Bytes()...)
}

func newEncoderPool() *encoderPool {
	return (*encoderPool)(&sync.Pool{
		New: func() interface{} {
			buf := bytes.Buffer{}
			buf.Grow(50 * 1024) // 50KB

			return pooledEncoder{
				buf:     &buf,
				Encoder: sixel.NewEncoder(&buf),
			}
		},
	})
}

func (encp *encoderPool) take() pooledEncoder {
	return (*sync.Pool)(encp).Get().(pooledEncoder)
}

func (encp *encoderPool) put(enc pooledEncoder) {
	enc.buf.Reset()
	(*sync.Pool)(encp).Put(enc)
}
