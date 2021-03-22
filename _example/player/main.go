package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/diamondburned/tcell-sixel/tsixel"
	"github.com/ericpauley/go-quantize/quantize"
	"github.com/gdamore/tcell/v2"
)

var (
	fps    float64
	scale  float64 = 1.0
	width  int
	height int
	colors int = 16
	dither bool
)

func init() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"Usage: %s -w x -h y -fps z -p path/to.palette [-s 1] [args...]\n\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(flag.CommandLine.Output(), "\t"+
			"The given arguments will be executed as a command.\n"+
			"The output of the command MUST be in rgba format.\n"+
			"A palette in CSV format must be given. Refer to the\n"+
			"README.\n\n")

		fmt.Fprintln(flag.CommandLine.Output(),
			"Flags:")
		flag.PrintDefaults()
	}

	flag.Float64Var(&fps, "fps", fps, "the frame rate to play at")
	flag.Float64Var(&scale, "s", scale, "relative scale factor")
	flag.IntVar(&width, "w", width, "the width of each frame")
	flag.IntVar(&height, "h", height, "the height of each frame")
	flag.IntVar(&colors, "c", colors, "number of colors to quantize to (2-254)")
	flag.BoolVar(&dither, "d", dither, "enable floyd-steinberg dithering")
	flag.Parse()

	if width == 0 || height == 0 {
		log.Fatalln("missing -w and/or -h")
	}

	if fps == 0 {
		log.Fatalln("missing -fps, invalid")
	}

	if colors < 2 || colors > 254 {
		log.Fatalln("invalid -c value out of bounds")
	}
}

func main() {
	var arg0 string
	var argv []string

	trailing := flag.Args()
	if len(trailing) < 1 {
		log.Fatalln("missing trailing command")
	}

	arg0 = trailing[0]
	if len(trailing) > 1 {
		argv = trailing[1:]
	}

	cmd := exec.Command(arg0, argv...)
	cmd.Stderr = os.Stderr

	o, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalln("failed to get cmd's stdout:", err)
	}
	defer o.Close()

	// Start (and kill - LIFO!) the process before halting the pipeline.
	if err := cmd.Start(); err != nil {
		log.Fatalln("failed to start cmd:", err)
	}
	defer cmd.Wait()
	defer cmd.Process.Kill()

	errorCh := make(chan error)

	frameCh, cancel := startPipeline(context.TODO(), pipelineProps{
		scale:  scale,
		width:  width,
		height: height,
		colors: colors,
		quantizer: quantize.MedianCutQuantizer{
			Aggregation: quantize.Mean,
		},

		reader: o,
		tickFq: time.Duration(float64(time.Second) / fps),
		nproc:  runtime.GOMAXPROCS(-1),
		errCh:  errorCh,
	})
	defer cancel()

	screen, err := tcell.NewScreen()
	if err != nil {
		log.Fatalln("failed to create screen:", err)
	}

	if err := screen.Init(); err != nil {
		log.Fatalln("failed to init screen:", err)
	}
	defer screen.Fini()

	sixels, err := tsixel.WrapInitScreen(screen)
	if err != nil {
		log.Fatalln("failed to wrap screen:", err)
	}

	dummy := newDummyImage(image.Pt(width, height))
	sixels.AddImage(dummy)

	eventCh := screenEventPipeline(screen)
	onEvent := func(ev tcell.Event) bool {
		switch ev := ev.(type) {
		case *tcell.EventKey:
			if ev.Key() == tcell.KeyEscape || ev.Rune() == 'q' {
				return true
			}
			if ev.Key() == tcell.KeyF5 {
				screen.Sync()
			}

		case *tcell.EventResize:
			screen.Show()
		}

		return false
	}

	for {
		// Process events first.
		select {
		case ev := <-eventCh:
			if onEvent(ev) {
				return
			}
		default:
		}

		select {
		case ev := <-eventCh:
			if onEvent(ev) {
				return
			}

		case frame := <-frameCh:
			dummy.SetSIXEL(frame)
			screen.Show()

		case err := <-errorCh:
			if !errors.Is(err, io.EOF) {
				log.Fatalln("stdin error occured:", err)
			}

			// EOF, we're all done.
			return
		}
	}
}

// screenEventPipeline starts a new event pipeline. The returned channel is
// closed once PollEvent returns a nil event.
func screenEventPipeline(screen tcell.Screen) <-chan tcell.Event {
	ch := make(chan tcell.Event, 1)

	go func() {
		for {
			event := screen.PollEvent()
			if event == nil {
				close(ch)
				return
			}

			ch <- event
		}
	}()

	return ch
}
