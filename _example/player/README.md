# player example

This example provides an application that reads from stdin for an RGBA stream.

## Usage

### 1. Query for size

```sh
ffprobe /path/to/video.mkv
```

Note the video stream line:

```
Stream #0:0: Video: h264 (High 10), yuv420p10le(progressive), 640x360 [SAR 1:1 DAR 16:9], 23.98 fps, 23.98 tbr, 1k tbn, 47.95 tbc (default)
```

This line tells us the resolution (640x360) and the frame-per-second (23.98).

### 2. Running

This command runs `player` to read from the command `ffmpeg` for frames that are
640x360 large, quantize them to 16 colors, 1x scale, with dithering, at
23.98fps.

The `ffmpeg` command re-encodes the given video through `-i` in RGBA format,
which is the only format that `player` accepts.

```sh
go run . -w 640 -h 360 -c 16 -s 1 -d -fps 23.98 -- \
	ffmpeg -hide_banner -loglevel error -i /tmp/apocrypha-op.mkv -f rawvideo -pix_fmt rgba -
```
