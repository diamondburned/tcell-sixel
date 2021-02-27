package main

import (
	"bytes"
	"image"
	"log"
	"os"
	"time"

	"github.com/disintegration/imaging"
	"github.com/mattn/go-sixel"
	"github.com/pkg/errors"
)

func readImage(src string, sz int) (image.Image, error) {
	f, err := os.Open(src)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open file")
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode image")
	}

	return imaging.Thumbnail(img, sz, sz, imaging.Lanczos), nil
}

type SIXELContainer struct {
	Bounds image.Rectangle

	src        image.Image
	currentImg image.Image

	buf *bytes.Buffer
	enc *sixel.Encoder
}

func NewSIXELContainer(src image.Image) *SIXELContainer {
	sixbuf := bytes.Buffer{}
	sixbuf.Grow(40960)

	return &SIXELContainer{
		src: src,
		buf: &sixbuf,
		enc: sixel.NewEncoder(&sixbuf),
	}
}

func (sct *SIXELContainer) Encode() []byte {
	bounds := sct.src.Bounds().Intersect(image.Rectangle{
		Min: image.Pt(0, 0),
		Max: sct.Bounds.Size(),
	})

	log.Println("union bounds:", bounds)
	log.Println("src bounds:  ", sct.src.Bounds())

	if sct.currentImg != nil && sct.currentImg.Bounds().Eq(bounds) {
		return sct.buf.Bytes()
	}

	log.Println("re-encoding...")
	now := time.Now()
	defer func() { log.Println("took", time.Now().Sub(now)) }()

	sct.currentImg = imaging.Fit(
		sct.src,
		bounds.Dx(),
		bounds.Dy(),
		imaging.CatmullRom,
	)

	sct.buf.Reset()
	sct.enc.Encode(sct.currentImg)

	return sct.buf.Bytes()
}

func cellSz(w, h, xpx, ypx int) (cellW, cellH int) {
	return xpx / w, ypx / h
}
