//go:build cgo

package capture

import (
	"bytes"
	"errors"
	"testing"
)

type bufferWriteCloser struct{ bytes.Buffer }

func (*bufferWriteCloser) Close() error { return nil }

type failingWriteCloser struct{}

func (failingWriteCloser) Write([]byte) (int, error) { return 0, errors.New("disk stopped") }
func (failingWriteCloser) Close() error              { return nil }

func TestPCMWriterPreservesEveryAcceptedFrame(t *testing.T) {
	capture := &nativeCapture{queued: make(chan *pcmBlock, 2), free: make(chan *pcmBlock, 2), fail: make(chan error, 1)}
	first := &pcmBlock{data: []byte{1, 2, 3, 4}, frames: 1}
	second := &pcmBlock{data: []byte{5, 6, 7, 8, 9, 10, 11, 12}, frames: 2}
	capture.queuedFrames.Store(3)
	capture.queued <- first
	capture.queued <- second
	close(capture.queued)
	output := &bufferWriteCloser{}
	done := make(chan error, 1)
	go capture.writePCM(output, done)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if capture.written.Load() != 3 || capture.queuedFrames.Load() != 0 {
		t.Fatalf("written=%d queued=%d", capture.written.Load(), capture.queuedFrames.Load())
	}
	if got, want := output.Bytes(), []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}; !bytes.Equal(got, want) {
		t.Fatalf("PCM output %v, want %v", got, want)
	}
}

func TestPCMWriterReportsEncoderFailure(t *testing.T) {
	capture := &nativeCapture{queued: make(chan *pcmBlock, 1), free: make(chan *pcmBlock, 1), fail: make(chan error, 1)}
	capture.queuedFrames.Store(1)
	capture.queued <- &pcmBlock{data: []byte{1, 2, 3, 4}, frames: 1}
	close(capture.queued)
	done := make(chan error, 1)
	go capture.writePCM(failingWriteCloser{}, done)
	if err := <-done; err == nil {
		t.Fatal("expected writer failure")
	}
	select {
	case err := <-capture.fail:
		if err == nil || err.Error() == "" {
			t.Fatal("empty failure")
		}
	default:
		t.Fatal("writer failure was not signalled")
	}
}
