package waveform

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

func TestReadPeaks(t *testing.T) {
	samples := []int16{0, 1000, -2000, 500, 32767, -100, 0}
	var pcm bytes.Buffer
	for _, sample := range samples {
		if err := binary.Write(&pcm, binary.LittleEndian, sample); err != nil {
			t.Fatal(err)
		}
	}
	peaks, count, err := readPeaks(&pcm, 3)
	if err != nil {
		t.Fatal(err)
	}
	if count != int64(len(samples)) {
		t.Fatalf("count=%d", count)
	}
	if len(peaks) != 3 {
		t.Fatalf("peaks=%v", peaks)
	}
	if peaks[0] < 15 || peaks[0] > 16 || peaks[1] != 255 || peaks[2] != 0 {
		t.Fatalf("unexpected peaks: %v", peaks)
	}
}

func TestReadPeaksHandlesSplitSamples(t *testing.T) {
	reader := &oneByteReader{data: []byte{0xff, 0x7f, 0x00, 0x80}}
	peaks, count, err := readPeaks(reader, 1)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || len(peaks) != 2 || peaks[0] != 255 || peaks[1] != 255 {
		t.Fatalf("unexpected result: %v, %d", peaks, count)
	}
}

type oneByteReader struct{ data []byte }

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	p[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}
