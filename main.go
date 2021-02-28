package main

import (
	"image"
	"log"
	"os"
	"time"

	_ "image/jpeg"
	_ "image/png"

	"github.com/diamondburned/tcell-sixel/tsixel"
	"github.com/disintegration/imaging"
	"github.com/gdamore/tcell/v2"
	"github.com/pkg/errors"
)

func main() {
	img, err := readImage("/home/diamond/Pictures/astolfo_ava_n.png")
	if err != nil {
		log.Fatalln("failed to read image:", err)
	}

	if err := start(img); err != nil {
		log.Fatalln(err)
	}
}

func start(img image.Image) error {
	screen, err := tcell.NewScreen()
	if err != nil {
		return errors.Wrap(err, "failed to create screen")
	}

	if err := screen.Init(); err != nil {
		return errors.Wrap(err, "failed to init screen")
	}
	defer screen.Fini()

	sixels, err := tsixel.WrapInitScreen(screen)
	if err != nil {
		return errors.Wrap(err, "failed to wrap screen")
	}

	sixel := sixels.AddAnyImage(img, tsixel.ImageOpts{
		Resize:    true,
		KeepRatio: true,
		Dither:    false,
		Filter:    imaging.Box,
	})

	sixel.SetSize(tsixel.CharPt(20, 20)) // or 40x20 chars or a square
	sixel.SetPosition(image.Pt(0, 1))

	runes := []rune("Hello, world! Look at this SIXEL:")
	screen.SetCell(0, 0, tcell.StyleDefault, runes...)

	screen.Sync()

	go func() {
		for i := len(runes); i < len(runes)+25; i++ {
			screen.SetCell(i, 0, tcell.StyleDefault, '.')
			screen.Sync()
			time.Sleep(time.Second)
		}
	}()

	for {
		switch ev := screen.PollEvent().(type) {
		case *tcell.EventKey:
			// Exit on Esc.
			if ev.Key() == tcell.KeyEscape {
				return nil
			}
		}
	}
}

func readImage(src string) (image.Image, error) {
	f, err := os.Open(src)
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
