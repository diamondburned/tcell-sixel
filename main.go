package main

import (
	"image"
	"log"
	"os"
	"path/filepath"
	"time"

	"image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/diamondburned/tcell-sixel/tsixel"
	"github.com/gdamore/tcell/v2"
	"github.com/pkg/errors"
	"golang.org/x/image/draw"
)

var Greetings = []rune("Hello, world! Look at this SIXEL: ")

type Image struct {
	Path     string
	Position image.Point
	Size     image.Point
}

var images = []Image{
	{
		Path:     "/home/diamond/Pictures/astolfo_ava_n.png",
		Position: image.Pt(0, 1),
		Size:     tsixel.CharPt(20, 20), // or 40x20 chars or a square
	},
	{
		Path:     "/home/diamond/Downloads/curry1.png",
		Position: image.Pt(len(Greetings), 0),
		Size:     tsixel.CharPt(1, 1), // 2x1 chars
	},
	{
		Path:     "/home/diamond/Downloads/emoji.gif",
		Position: tsixel.CharPt(20, 1),
		Size:     tsixel.CharPt(5, 5),
	},
}

func main() {
	sixels := make([]tsixel.Imager, len(images))
	opts := tsixel.ImageOpts{
		KeepRatio: true,
		Dither:    false,
		Scaler:    draw.BiLinear,
	}

	for i, img := range images {
		var sixel tsixel.Imager

		f, err := os.Open(img.Path)
		if err != nil {
			log.Fatalln("failed to open:", err)
		}

		if filepath.Ext(img.Path) == ".gif" {
			g, err := gif.DecodeAll(f)
			if err != nil {
				log.Fatalln("failed to decode GIF:", err)
			}

			anim := tsixel.NewAnimation(g, opts)
			anim.SetSize(img.Size)
			anim.SetPosition(img.Position)

			sixel = anim

		} else {
			src, _, err := image.Decode(f)
			if err != nil {
				log.Fatalln("failde to decode image:", err)
			}

			siximg := tsixel.NewImage(src, opts)
			siximg.SetSize(img.Size)
			siximg.SetPosition(img.Position)

			sixel = siximg
		}

		f.Close()
		sixels[i] = sixel
	}

	if err := start(sixels); err != nil {
		log.Fatalln(err)
	}
}

func start(images []tsixel.Imager) error {
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
	screen.Sync()

	go func() {
		for range time.Tick(time.Second / 15) {
			screen.Show()
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
