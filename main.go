package main

import (
	"image"
	"log"
	"os"

	_ "image/jpeg"
	_ "image/png"

	"github.com/diamondburned/tcell-sixel/tsixel"
	"github.com/disintegration/imaging"
	"github.com/gdamore/tcell/v2"
	"github.com/pkg/errors"
)

var Greetings = []rune("Hello, world! Look at this SIXEL: ")

type Image struct {
	Path     string
	Position image.Point
	Size     image.Point
}

var images = []Image{{
	Path:     "/home/diamond/Pictures/astolfo_ava_n.png",
	Position: image.Pt(0, 1),
	Size:     tsixel.CharPt(20, 20), // or 40x20 chars or a square
}, {
	Path:     "/home/diamond/Downloads/curry1.png",
	Position: image.Pt(len(Greetings), 0),
	Size:     tsixel.CharPt(1, 1), // 2x1 chars
}}

func main() {
	var sixels = make([]*tsixel.Image, len(images))

	for i, img := range images {
		image, err := readImage(img.Path)
		if err != nil {
			log.Fatalln("failed to read image:", err)
		}

		sixel := tsixel.NewImage(image, tsixel.ImageOpts{
			Resize:    true,
			KeepRatio: true,
			Dither:    false,
			Filter:    imaging.Box,
		})
		sixel.SetSize(img.Size)
		sixel.SetPosition(img.Position)

		sixels[i] = sixel
	}

	if err := start(sixels); err != nil {
		log.Fatalln(err)
	}
}

func start(images []*tsixel.Image) error {
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

	for _, img := range images {
		sixels.AddImage(img)
	}

	screen.SetCell(0, 0, tcell.StyleDefault, Greetings...)
	screen.Show()

	// 	go func() {
	// 		for i := len(Greetings); i < len(Greetings)+25; i++ {
	// 			screen.SetCell(i, 0, tcell.StyleDefault, '.')
	// 			screen.Show()
	// 			time.Sleep(time.Second)
	// 		}
	// 	}()

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
