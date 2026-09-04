//go:build cgo

package capture

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/store"
	"github.com/gen2brain/malgo"
)

type pcmBlock struct {
	data   []byte
	frames uint32
}

type nativeCapture struct {
	config       config.Config
	clock        *frameClock
	deviceName   string
	deviceID     string
	stopOnce     sync.Once
	stop         chan struct{}
	done         chan captureResult
	fail         chan error
	first        chan struct{}
	firstOnce    sync.Once
	free         chan *pcmBlock
	queued       chan *pcmBlock
	accepted     atomic.Uint64
	written      atomic.Uint64
	dropped      atomic.Uint64
	callbacks    atomic.Uint64
	queuedFrames atomic.Uint64
	highWater    atomic.Uint64
	started      time.Time
}

func startMiniaudioCapture(c config.Config, partPath string, logFile *os.File) (activeCapture, error) {
	if c.Capture.SampleRate <= 0 || c.Capture.Channels <= 0 {
		return nil, errors.New("capture sample rate and channels must be positive")
	}
	periodMS := c.Capture.PeriodMS
	if periodMS <= 0 {
		periodMS = 20
	}
	bufferSeconds := c.Capture.BufferSecs
	if bufferSeconds <= 0 {
		bufferSeconds = 10
	}
	blocks := bufferSeconds*1000/periodMS + 8
	maxFrames := c.Capture.SampleRate * periodMS * 4 / 1000
	if maxFrames < 4096 {
		maxFrames = 4096
	}
	bytesPerFrame := c.Capture.Channels * 2
	native := &nativeCapture{
		config: c, clock: newFrameClock(c.Capture.SampleRate), stop: make(chan struct{}), done: make(chan captureResult, 1), fail: make(chan error, 1), first: make(chan struct{}),
		free: make(chan *pcmBlock, blocks), queued: make(chan *pcmBlock, blocks), started: time.Now(),
	}
	for index := 0; index < blocks; index++ {
		native.free <- &pcmBlock{data: make([]byte, maxFrames*bytesPerFrame)}
	}

	encoderArgs := []string{"-hide_banner", "-nostdin", "-y", "-f", "s16le", "-ar", strconv.Itoa(c.Capture.SampleRate), "-ac", strconv.Itoa(c.Capture.Channels), "-i", "pipe:0", "-vn", "-c:a", "flac", "-compression_level", "5", "-f", "flac", partPath}
	encoder := exec.Command(c.FFmpeg, encoderArgs...)
	stdin, err := encoder.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open FLAC encoder input: %w", err)
	}
	encoder.Stdout, encoder.Stderr = logFile, logFile
	fmt.Fprintf(logFile, "\n[%s] starting miniaudio capture and FLAC encoder: %s\n", time.Now().Format(time.RFC3339), printableCommand(c.FFmpeg, encoderArgs))
	if err := encoder.Start(); err != nil {
		return nil, fmt.Errorf("start FLAC encoder: %w", err)
	}

	malgoContext, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		_ = stdin.Close()
		_ = encoder.Wait()
		return nil, fmt.Errorf("initialise miniaudio: %w", err)
	}
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = uint32(c.Capture.Channels)
	deviceConfig.Capture.ShareMode = malgo.Shared
	deviceConfig.SampleRate = uint32(c.Capture.SampleRate)
	deviceConfig.PeriodSizeInMilliseconds = uint32(periodMS)
	deviceConfig.PerformanceProfile = malgo.Conservative
	selected, selectionErr := selectMiniaudioDevice(malgoContext.Context, c.Capture.DeviceID, c.Capture.Device)
	if selectionErr != nil {
		_ = malgoContext.Uninit()
		malgoContext.Free()
		_ = stdin.Close()
		_ = encoder.Wait()
		return nil, selectionErr
	}
	if selected != nil {
		deviceConfig.Capture.DeviceID = selected.ID.Pointer()
		native.deviceID, native.deviceName = selected.ID.String(), selected.Name()
	} else {
		native.deviceName = "Default capture device"
	}

	callbacks := malgo.DeviceCallbacks{Data: func(_ []byte, input []byte, frameCount uint32) {
		if len(input) == 0 || frameCount == 0 {
			return
		}
		native.callbacks.Add(1)
		var block *pcmBlock
		select {
		case block = <-native.free:
		default:
			native.dropped.Add(uint64(frameCount))
			native.signalFailure(errors.New("audio capture queue overflowed; the recording was stopped to avoid hidden audio loss"))
			return
		}
		if len(input) > cap(block.data) {
			native.free <- block
			native.dropped.Add(uint64(frameCount))
			native.signalFailure(fmt.Errorf("audio callback block of %d bytes exceeds the configured buffer block", len(input)))
			return
		}
		block.data = block.data[:len(input)]
		copy(block.data, input)
		block.frames = frameCount
		native.queued <- block
		native.accepted.Add(uint64(frameCount))
		queued := native.queuedFrames.Add(uint64(frameCount))
		updateMaximum(&native.highWater, queued)
		native.clock.accept(frameCount, time.Now())
		native.firstOnce.Do(func() { close(native.first) })
	}}
	device, err := malgo.InitDevice(malgoContext.Context, deviceConfig, callbacks)
	if err != nil {
		_ = malgoContext.Uninit()
		malgoContext.Free()
		_ = stdin.Close()
		_ = encoder.Wait()
		return nil, fmt.Errorf("open miniaudio capture device: %w", err)
	}

	writerDone := make(chan error, 1)
	go native.writePCM(stdin, writerDone)
	go native.run(device, malgoContext, encoder, stdin, writerDone, partPath, logFile)
	if err := device.Start(); err != nil {
		native.signalFailure(fmt.Errorf("start miniaudio capture device: %w", err))
		<-native.done
		return nil, err
	}
	select {
	case <-native.first:
		fmt.Fprintf(logFile, "miniaudio device ready: %s, requested=%d Hz/%d channels, native=%d Hz/%d channels\n", native.deviceName, c.Capture.SampleRate, c.Capture.Channels, device.CaptureInternalSampleRate(), device.CaptureInternalChannels())
		return native, nil
	case result := <-native.done:
		if result.Error == nil {
			result.Error = errors.New("capture stopped before the first audio callback")
		}
		return nil, result.Error
	case <-time.After(5 * time.Second):
		err := errors.New("the capture device started but supplied no audio within five seconds")
		native.signalFailure(err)
		<-native.done
		return nil, err
	}
}

func (n *nativeCapture) writePCM(stdin io.WriteCloser, done chan<- error) {
	for block := range n.queued {
		_, err := writeAll(stdin, block.data)
		if err == nil {
			n.written.Add(uint64(block.frames))
		}
		n.queuedFrames.Add(^uint64(block.frames - 1))
		block.data = block.data[:cap(block.data)]
		n.free <- block
		if err != nil {
			n.signalFailure(fmt.Errorf("write PCM to FLAC encoder: %w", err))
			done <- err
			return
		}
	}
	done <- nil
}

func (n *nativeCapture) run(device *malgo.Device, malgoContext *malgo.AllocatedContext, encoder *exec.Cmd, stdin io.WriteCloser, writerDone <-chan error, partPath string, logFile *os.File) {
	var requestedErr error
	select {
	case <-n.stop:
	case requestedErr = <-n.fail:
	}
	_ = device.Stop()
	device.Uninit()
	_ = malgoContext.Uninit()
	malgoContext.Free()
	close(n.queued)
	writerErr := <-writerDone
	closeErr := stdin.Close()
	encoderErr := encoder.Wait()
	resultErr := errors.Join(requestedErr, writerErr, closeErr, encoderErr)
	info := n.Info()
	info.WallDuration = time.Since(n.started).Seconds()
	info.AudioDuration = float64(info.TotalFrames) / float64(info.SampleRate)
	if info.WallDuration > 0 {
		info.ClockDriftPPM = (info.AudioDuration - info.WallDuration) / info.WallDuration * 1_000_000
	}
	if resultErr == nil && info.TotalFrames != info.WrittenFrames {
		resultErr = fmt.Errorf("capture accepted %d frames but the encoder received %d", info.TotalFrames, info.WrittenFrames)
	}
	if resultErr == nil {
		duration, probeErr := probeDuration(n.config.FFprobe, partPath)
		encodedFrames := uint64(math.Round(duration * float64(info.SampleRate)))
		if probeErr != nil {
			resultErr = fmt.Errorf("verify FLAC duration: %w", probeErr)
		} else if frameDifference(encodedFrames, info.WrittenFrames) > 1 {
			resultErr = fmt.Errorf("FLAC contains %d frames but the encoder received %d", encodedFrames, info.WrittenFrames)
		}
	}
	fmt.Fprintf(logFile, "capture finished: accepted=%d written=%d dropped=%d high-water=%d drift=%.1f ppm error=%v\n", info.TotalFrames, info.WrittenFrames, info.DroppedFrames, info.QueueHighWaterFrames, info.ClockDriftPPM, resultErr)
	n.done <- captureResult{Info: info, PartPath: partPath, Error: resultErr}
	close(n.done)
}

func frameDifference(a, b uint64) uint64 {
	if a > b {
		return a - b
	}
	return b - a
}

func (n *nativeCapture) signalFailure(err error) {
	if err == nil {
		return
	}
	select {
	case n.fail <- err:
	default:
	}
}

func (n *nativeCapture) PositionAt(at time.Time) Position {
	return n.clock.positionAt(at, 250*time.Millisecond)
}
func (n *nativeCapture) Latest() Position           { return n.clock.latest() }
func (n *nativeCapture) Stop()                      { n.stopOnce.Do(func() { close(n.stop) }) }
func (n *nativeCapture) Done() <-chan captureResult { return n.done }

func (n *nativeCapture) Info() store.CaptureInfo {
	return store.CaptureInfo{
		Backend: "miniaudio", DeviceID: n.deviceID, DeviceName: n.deviceName, SampleRate: n.config.Capture.SampleRate, Channels: n.config.Capture.Channels, SampleFormat: "s16le",
		TotalFrames: n.accepted.Load(), WrittenFrames: n.written.Load(), DroppedFrames: n.dropped.Load(), CallbackCount: n.callbacks.Load(), QueueHighWaterFrames: n.highWater.Load(),
	}
}

func selectMiniaudioDevice(context malgo.Context, requestedID, requestedName string) (*malgo.DeviceInfo, error) {
	requestedID, requestedName = strings.TrimSpace(requestedID), strings.TrimSpace(requestedName)
	devices, err := context.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("list miniaudio capture devices: %w", err)
	}
	for index := range devices {
		if requestedID == "" && (requestedName == "" || strings.EqualFold(requestedName, "default")) && devices[index].IsDefault != 0 {
			return &devices[index], nil
		}
		if requestedID != "" && strings.EqualFold(devices[index].ID.String(), requestedID) {
			return &devices[index], nil
		}
		if requestedID == "" && strings.EqualFold(devices[index].Name(), requestedName) {
			return &devices[index], nil
		}
	}
	if requestedID == "" && (requestedName == "" || strings.EqualFold(requestedName, "default")) {
		return nil, nil
	}
	if requestedID != "" {
		return nil, fmt.Errorf("the saved capture device was not found; choose one in the OBS dock (device ID %q)", requestedID)
	}
	return nil, fmt.Errorf("capture device %q was not found; choose one in the OBS dock", requestedName)
}

func writeAll(writer io.Writer, data []byte) (int, error) {
	total := 0
	for len(data) > 0 {
		written, err := writer.Write(data)
		total += written
		data = data[written:]
		if err != nil {
			return total, err
		}
		if written == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func updateMaximum(value *atomic.Uint64, candidate uint64) {
	for current := value.Load(); candidate > current; current = value.Load() {
		if value.CompareAndSwap(current, candidate) {
			return
		}
	}
}
