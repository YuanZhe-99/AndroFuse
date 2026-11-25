package avbridge

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/companion"
)

// UplinkStats summarizes PC-to-Android microphone/camera bridge traffic over Companion TLS.
type UplinkStats struct {
	Active      bool   `json:"active"`
	AudioFrames uint64 `json:"audioFrames"`
	VideoFrames uint64 `json:"videoFrames"`
	BytesSent   uint64 `json:"bytesSent"`
	LastError   string `json:"lastError,omitempty"`
}

// PCMReader reads captured signed 16-bit PCM bytes (satisfied by core.MicCapturer).
type PCMReader interface {
	ReadPCM(dst []byte) (int, error)
	Close()
}

// UplinkBridge forwards PC mic PCM and camera JPEG frames to Android Companion
// when explicitly permitted (`Permissions.MediaBridge`).
type UplinkBridge struct {
	mu     sync.Mutex
	client *companion.Client
	active bool
	stats  UplinkStats
}

// NewUplinkBridge creates an UplinkBridge targeting a connected Companion client.
func NewUplinkBridge(client *companion.Client) *UplinkBridge {
	return &UplinkBridge{client: client}
}

// Start marks the uplink active so PushMicPCM / PushCameraJPEG can deliver frames.
func (u *UplinkBridge) Start() {
	u.mu.Lock()
	u.active = true
	u.stats.Active = true
	u.stats.LastError = ""
	u.mu.Unlock()
}

// Stop disables further PC->Android media uplink pushes.
func (u *UplinkBridge) Stop() {
	u.mu.Lock()
	u.active = false
	u.stats.Active = false
	u.mu.Unlock()
}

// Stats returns a thread-safe snapshot of sent frame counters.
func (u *UplinkBridge) Stats() UplinkStats {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.stats
}

// PushMicPCM sends one bounded raw s16le PCM audio chunk to the Android Companion.
func (u *UplinkBridge) PushMicPCM(ctx context.Context, sampleRate, channels int, pcm []byte) error {
	if len(pcm) == 0 || len(pcm) > companion.MaxMediaFrameBytes {
		return errors.New("avbridge: pcm payload empty or exceeds limit")
	}
	return u.push(ctx, companion.MediaFrameRequest{
		Kind:       "mic_pcm",
		SampleRate: sampleRate,
		Channels:   channels,
		DataBase64: base64.StdEncoding.EncodeToString(pcm),
		Timestamp:  time.Now().UnixMilli(),
	}, len(pcm), true)
}

// PushCameraJPEG sends one bounded JPEG webcam frame to the Android Companion.
func (u *UplinkBridge) PushCameraJPEG(ctx context.Context, width, height int, jpegBytes []byte) error {
	if len(jpegBytes) == 0 || len(jpegBytes) > companion.MaxMediaFrameBytes {
		return errors.New("avbridge: jpeg payload empty or exceeds limit")
	}
	return u.push(ctx, companion.MediaFrameRequest{
		Kind:       "camera_jpeg",
		Width:      width,
		Height:     height,
		DataBase64: base64.StdEncoding.EncodeToString(jpegBytes),
		Timestamp:  time.Now().UnixMilli(),
	}, len(jpegBytes), false)
}

func (u *UplinkBridge) push(ctx context.Context, req companion.MediaFrameRequest, rawLen int, isAudio bool) error {
	u.mu.Lock()
	active := u.active
	cl := u.client
	u.mu.Unlock()
	if !active || cl == nil {
		return errors.New("avbridge: uplink stopped or companion not connected")
	}
	_, err := cl.PushMediaFrame(ctx, req)
	u.mu.Lock()
	if err != nil {
		u.stats.LastError = err.Error()
	} else {
		u.stats.LastError = ""
		if isAudio {
			u.stats.AudioFrames++
		} else {
			u.stats.VideoFrames++
		}
		u.stats.BytesSent += uint64(rawLen)
	}
	u.mu.Unlock()
	return err
}

// StreamMic polls 20ms PCM chunks from reader and pushes them via Companion until stopped or cancelled.
func (u *UplinkBridge) StreamMic(ctx context.Context, sampleRate, channels int, reader PCMReader) error {
	if reader == nil {
		return errors.New("avbridge: nil PCMReader")
	}
	if sampleRate <= 0 {
		sampleRate = 48000
	}
	if channels <= 0 {
		channels = 1
	}
	chunkBytes := (sampleRate / 50) * channels * 2 // 20ms s16le PCM
	buf := make([]byte, chunkBytes)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			u.mu.Lock()
			active := u.active
			u.mu.Unlock()
			if !active {
				return nil
			}
			n, err := reader.ReadPCM(buf)
			if err != nil {
				u.mu.Lock()
				u.stats.LastError = err.Error()
				u.mu.Unlock()
				return err
			}
			if n > 0 {
				if err := u.PushMicPCM(ctx, sampleRate, channels, buf[:n]); err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
				}
			}
		}
	}
}
