package speaker

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
)

// DecodeFile decodes any ffmpeg-readable audio or video file to mono 16 kHz float32 PCM.
// This is the "point it at any video or audio file" entry point.
func DecodeFile(ffmpegPath, path string) ([]float32, error) {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	cmd := exec.Command(ffmpegPath, "-v", "error", "-nostdin",
		"-i", path, "-vn", "-f", "s16le", "-ac", "1", "-ar", "16000", "pipe:1")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("speaker: ffmpeg decode %s: %v: %s", path, err, errb.String())
	}
	return pcm16ToFloat(out.Bytes()), nil
}

// DecodeBytes decodes raw media bytes (e.g. the stored meeting audio) by staging them to
// a temp file first — robust for container formats (mp4/mov) whose index lives at the end
// and can't be read from a pipe.
func DecodeBytes(ffmpegPath string, data []byte) ([]float32, error) {
	f, err := os.CreateTemp("", "noto-vp-*.media")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()
	return DecodeFile(ffmpegPath, f.Name())
}

func pcm16ToFloat(b []byte) []float32 {
	n := len(b) / 2
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = float32(int16(binary.LittleEndian.Uint16(b[2*i:]))) / 32768.0
	}
	return out
}
