package main

import (
	"image"
	"log"
	"os"
	"time"

	"github.com/diamondburned/tcell-sixel/tsixel"
	"github.com/gdamore/tcell/v2"
	"github.com/pkg/errors"
	"golang.org/x/image/draw"

	_ "image/jpeg"
	_ "image/png"
)

// Some of the movement code were rewritten from
// https://github.com/ameyrk99/dvdts/blob/d11bbc4184bdcda720f4febf2ca181773cb7c203/update_text.go#L36

const (
	imagePath   = "/home/diamond/Downloads/500px-DVD_logo.svg.png"
	imageWidth  = 5
	imageHeight = 5
)

func main() {
	img, err := decodeImage(imagePath)
	if err != nil {
		log.Fatalln("failed to decode image:", err)
	}

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

	// Calculate the SIXEL size and not rely on the size returned from Bounds,
	// as that size specifically accounts for the cropped/scaled size and would
	// yield incorrect results when checking for bounds.
	sixSize := tsixel.CharPt(imageWidth, imageHeight)

	siximg := sixels.AddAnyImage(img, tsixel.ImageOpts{
		Scaler:     draw.CatmullRom,
		KeepRatio:  true,
		NoRounding: true,
	})
	siximg.SetSize(sixSize)
	siximg.SetPosition(image.Pt(0, 0))

	frameTicker := time.NewTicker(75 * time.Millisecond)
	defer frameTicker.Stop()

	eventCh := screenEvents(screen)

	// Image bouncing states.
	addX := true
	addY := true
	screenX, screenY := screen.Size()

	for {
		select {
		case ev := <-eventCh:
			switch ev := ev.(type) {
			case *tcell.EventKey:
				switch ev.Key() {
				// Exit on Esc.
				case tcell.KeyEscape:
					return

				// Force redraw on F5 for debugging.
				case tcell.KeyF5:
					screen.Sync()
				}

				switch ev.Rune() {
				// Exit on Q.
				case 'q':
					return
				}

			case *tcell.EventResize:
				screenX, screenY = screen.Size()
			}

		case <-frameTicker.C:
			bounds := siximg.RequestedBounds()
			// imgBounds := siximg.Bounds()
			// bounds := image.Rectangle{
			// 	Min: imgBounds.Min,
			// 	Max: imgBounds.Min.Add(sixSize),
			// }

			// Check if the text hit the left/right side of the terminal. We
			// account for the width/height difference by multiplying by 2.
			switch {
			case bounds.Min.X <= 0:
				addX = true
			case bounds.Max.X >= screenX:
				addX = false
			}

			// Check if the text hit the bottom/top side of the terminal.
			switch {
			case bounds.Min.Y <= 0:
				addY = true
			case bounds.Max.Y >= screenY:
				addY = false
			}

			if addX {
				bounds.Min.X += 2
			} else {
				bounds.Min.X -= 2
			}

			if addY {
				bounds.Min.Y++
			} else {
				bounds.Min.Y--
			}

			siximg.SetPosition(bounds.Min)
			screen.Sync()
		}
	}
}

func screenEvents(screen tcell.Screen) <-chan tcell.Event {
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

func decodeImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open file")
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode image")
	}

	return img, nil
}
