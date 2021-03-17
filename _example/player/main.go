package main

import (
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/diamondburned/tcell-sixel/tsixel"
	"github.com/ericpauley/go-quantize/quantize"
	"github.com/gdamore/tcell/v2"
	"golang.org/x/image/draw"
)

var (
	fps    float64
	width  int
	height int
	scale  int = 1
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
	flag.IntVar(&width, "w", width, "the width of each frame")
	flag.IntVar(&height, "h", height, "the height of each frame")
	flag.IntVar(&scale, "s", scale, "integer scale factor")
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

	// Preallocate what we need.
	paletteds := [3]*image.Paletted{
		// dithering
		image.NewPaletted(image.Rect(0, 0, width, height), newEmptyPalette(colors)),
		// image
		image.NewPaletted(image.Rect(0, 0, width*scale, height*scale), newEmptyPalette(colors)),
		// scaling
		image.NewPaletted(image.Rect(0, 0, width*scale, height*scale), newEmptyPalette(colors)),
	}

	sixel := tsixel.NewStaticImage(paletteds[1])
	sixel.SetPosition(image.Pt(0, 0))

	sixels.AddImage(sixel)

	quantizer := quantize.MedianCutQuantizer{
		Aggregation: quantize.Mean,
	}

	drawCh := make(chan struct{}, 1)
	eventCh := screenEventPipeline(screen)

	var xchgMu sync.Mutex
	readRGBA := image.NewRGBA(image.Rect(0, 0, width, height))
	xchgPBuf := make([]byte, len(readRGBA.Pix))

	errCh := cmdRGBAPipeline(flag.Args(), width, height, fps, func(img *image.RGBA) {
		xchgMu.Lock()
		xchgPBuf, img.Pix = img.Pix, xchgPBuf
		xchgMu.Unlock()

		select {
		case drawCh <- struct{}{}:
		default:
		}
	})

	for {
		select {
		case ev := <-eventCh:
			switch ev := ev.(type) {
			case *tcell.EventKey:
				if ev.Key() == tcell.KeyEscape || ev.Rune() == 'q' {
					return
				}
				if ev.Key() == tcell.KeyF5 {
					screen.Sync()
				}

			case *tcell.EventResize:
				screen.Show()
			}

		case <-drawCh:
			// Acquire and swap.
			xchgMu.Lock()
			readRGBA.Pix, xchgPBuf = xchgPBuf, readRGBA.Pix
			xchgMu.Unlock()

			// Re-quantize the palette for a new one each frame.
			paletteds[0].Palette = quantizer.Quantize(paletteds[0].Palette[:0], readRGBA)

			// Dither the image with the new palette.
			if !dither {
				draw.Draw(paletteds[0], paletteds[0].Bounds(), readRGBA, image.Point{}, draw.Src)
			} else {
				draw.FloydSteinberg.Draw(paletteds[0], readRGBA.Bounds(), readRGBA, image.Point{})
			}

			// Optionally rescale.
			if scale > 1 {
				paletteds[2].Palette = paletteds[0].Palette
				nearestNeighbor(paletteds[2], paletteds[0], scale)
				// Swap the internal image that we just scaled on with the previous
				// ones.
				paletteds[1], paletteds[2] = paletteds[2], paletteds[1]
			} else {
				// Swap the internal image with the previous one.
				paletteds[1], paletteds[0] = paletteds[0], paletteds[1]
			}

			sixel.SetImage(paletteds[1])
			screen.Show()

		case err := <-errCh:
			if !errors.Is(err, io.EOF) {
				log.Fatalln("stdin error occured:", err)
			}

			// EOF, we're all done.
			return
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

// nearestNeighbor scales the given pix slice of color indices onto the dst
// slice according to the given scale. The given stride should be of the source.
func nearestNeighbor(dst, src *image.Paletted, scale int) {
	// http://tech-algorithm.com/articles/nearest-neighbor-image-scaling/
	for y := 0; y < dst.Rect.Dy(); y++ {
		ysrc := y / scale * src.Stride
		ydst := y * dst.Stride

		for x := 0; x < dst.Stride; x++ {
			xsrc := x / scale
			dst.Pix[ydst+x] = src.Pix[ysrc+xsrc]
		}
	}
}

// cmdRGBAPipeline starts a new reader pipeline. If reader fails, then the error
// is sent into the pipe. The callback MUST have completely swapped out its
// iamge with the new one by the time it returns.
func cmdRGBAPipeline(
	argv []string, w, h int, fps float64, draw func(*image.RGBA)) <-chan error {

	errCh := make(chan error, 1)

	go func() {
		var args []string
		if len(argv) > 1 {
			args = argv[1:]
		}

		cmd := exec.Command(argv[0], args...)
		cmd.Stderr = os.Stderr

		o, err := cmd.StdoutPipe()
		if err != nil {
			errCh <- err
			return
		}
		if err := cmd.Start(); err != nil {
			o.Close()
			errCh <- err
			return
		}

		ours := image.NewRGBA(image.Rect(0, 0, w, h))

		ticker := time.NewTicker(time.Duration(float64(time.Second) / fps))
		defer ticker.Stop()

		for range ticker.C {
			// Try to read a full frame.
			if _, err = io.ReadFull(o, ours.Pix); err != nil {
				break
			}

			draw(ours)
		}

		// Start cleaning up.
		cmd.Process.Kill()
		cmd.Wait()
		// Send the error and exit.
		errCh <- err
	}()

	return errCh
}

// screenEventPipeline starts a new event pipeline. The returned channel is
// closed once PollEvent returns a nil event.
func screenEventPipeline(screen tcell.Screen) <-chan tcell.Event {
	ch := make(chan tcell.Event)

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
