# Launch video (9:16, 26 s)

`scene.html` is the whole video as one deterministic function of time, `setT(t)`: no CSS animations, so every frame is reproducible. `render.py` drives headless Chrome over the DevTools protocol, screenshots 780 frames at 1080x1920, and ffmpeg encodes them.

```bash
python3 -m venv /tmp/v && /tmp/v/bin/pip install imageio-ffmpeg websocket-client
cd marketing/video && /tmp/v/bin/python render.py            # writes frames/f_0000.png ...
FF=$(/tmp/v/bin/python -c "import imageio_ffmpeg;print(imageio_ffmpeg.get_ffmpeg_exe())")
$FF -framerate 30 -i frames/f_%04d.png -c:v libx264 -crf 17 -pix_fmt yuv420p -movflags +faststart out.mp4
```

Needs Google Chrome at the macOS path in `render.py` (edit `CH` for other platforms). The incident is fictional and the video says so on screen ("SIMULATED INCIDENT"); the commands shown are real wbi commands, with simplified output. The finished media (`wbi-twitter-launch.mp4`, `-thumbnail.png`) are not committed.
