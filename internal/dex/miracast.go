package dex

import (
	"errors"
	"sync"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/core"
)

// MiracastStats holds live honest state for a Wireless Miracast DeX receiver session (INV-05).
type MiracastStats struct {
	State               uint32
	NegotiatedWidth     uint32
	NegotiatedHeight    uint32
	FramesDecoded       int
	AvgDecodeUs         int64
	MeasuredFPS         float64
	HardwareAccelerated bool
}

// MiracastSession drives a live Windows Wi-Fi Direct / Miracast DeX receiver loop
// wrapped around `core.MiracastHandle` (`INV-04`, `INV-05`).
type MiracastSession struct {
	handle  *core.MiracastHandle
	ring    core.NV12Ring
	onFrame func(core.NV12Frame)
	onState func(uint32)

	mu     sync.Mutex
	stats  MiracastStats
	closed bool
	stopCh chan struct{}
	doneCh chan struct{}
}

// StartMiracast opens the Windows Wi-Fi Direct Sink on rtspPort (0 = 7236) and
// starts polling decoded NV12 frames into onFrame.
func StartMiracast(lib *core.Lib, rtspPort uint16, onFrame func(core.NV12Frame), onState func(uint32)) (*MiracastSession, error) {
	if lib == nil {
		return nil, errors.New("core library is required")
	}
	h, err := lib.StartMiracast(rtspPort)
	if err != nil {
		return nil, err
	}
	s := &MiracastSession{
		handle:  h,
		onFrame: onFrame,
		onState: onState,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
	go s.pollLoop()
	return s, nil
}

// SendPointer forwards a UIBC pointer event (`0=down, 1=up, 2=move`) to the connected phone.
func (s *MiracastSession) SendPointer(action uint8, xNorm, yNorm uint16) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed || s.handle == nil {
		return errors.New("miracast session closed")
	}
	return s.handle.SendPointer(action, xNorm, yNorm)
}

// Stats returns honest live Miracast receiver telemetry (INV-05).
func (s *MiracastSession) Stats() MiracastStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// Done closes when the Miracast poll loop terminates.
func (s *MiracastSession) Done() <-chan struct{} {
	return s.doneCh
}

// Close stops the polling loop and tears down the native Miracast Sink handles.
func (s *MiracastSession) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.stopCh)
	s.mu.Unlock()

	<-s.doneCh
	if s.handle != nil {
		s.handle.Close()
	}
}

func (s *MiracastSession) pollLoop() {
	defer close(s.doneCh)
	ticker := time.NewTicker(4 * time.Millisecond)
	defer ticker.Stop()

	var totalDecodeUs int64
	var winFrames int
	winStart := time.Now()
	lastState := uint32(99)

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			frame, state, ok, err := s.handle.PollInto(&s.ring)
			if err != nil {
				return
			}
			if state != lastState {
				lastState = state
				s.mu.Lock()
				s.stats.State = state
				s.mu.Unlock()
				if s.onState != nil {
					s.onState(state)
				}
			}
			if ok {
				totalDecodeUs += int64(frame.DecodeUs)
				winFrames++
				now := time.Now()
				s.mu.Lock()
				s.stats.FramesDecoded++
				if frame.Width > 0 && frame.Height > 0 {
					s.stats.NegotiatedWidth = uint32(frame.Width)
					s.stats.NegotiatedHeight = uint32(frame.Height)
				}
				s.stats.HardwareAccelerated = frame.Hardware
				if s.stats.FramesDecoded > 0 {
					s.stats.AvgDecodeUs = totalDecodeUs / int64(s.stats.FramesDecoded)
				}
				if dt := now.Sub(winStart).Seconds(); dt >= 0.5 {
					s.stats.MeasuredFPS = float64(winFrames) / dt
					winStart = now
					winFrames = 0
				}
				s.mu.Unlock()
				if s.onFrame != nil {
					s.onFrame(frame)
				}
			}
		}
	}
}
