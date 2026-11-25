package scrcpy

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// MP4Recorder muxes raw H.264 Annex-B scrcpy packets directly into a fragmented
// MP4 (`ftyp`/`moov`/`moof`/`mdat`) file with zero re-encoding.
type MP4Recorder struct {
	mu          sync.Mutex
	file        *os.File
	path        string
	width       uint32
	height      uint32
	sps         []byte
	pps         []byte
	initWritten bool
	seqNum      uint32
	basePtsUs   uint64
	hasBasePts  bool
	decodeTime  uint64
	samples     []mp4Sample
	mdatBuf     bytes.Buffer
	closed      bool
}

type mp4Sample struct {
	size       uint32
	duration   uint32
	isKeyframe bool
}

// NewMP4Recorder opens path for direct H.264 Annex-B fragmented MP4 recording.
func NewMP4Recorder(path string, width, height uint32) (*MP4Recorder, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if width == 0 {
		width = 1280
	}
	if height == 0 {
		height = 720
	}
	return &MP4Recorder{
		file:   f,
		path:   path,
		width:  width,
		height: height,
		seqNum: 1,
	}, nil
}

// SetDimensions updates the video track dimensions before moov initialization.
func (r *MP4Recorder) SetDimensions(w, h uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w > 0 && h > 0 && !r.initWritten {
		r.width = w
		r.height = h
	}
}

// WriteAnnexB consumes one H.264 Annex-B access unit (config and/or frame).
func (r *MP4Recorder) WriteAnnexB(annexB []byte, ptsUs uint64, isConfig, isKeyframe bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.file == nil {
		return errors.New("recorder closed")
	}
	nals := splitAnnexBNALUs(annexB)
	if len(nals) == 0 {
		return nil
	}

	var avccSample bytes.Buffer
	for _, nal := range nals {
		if len(nal) == 0 {
			continue
		}
		nalType := nal[0] & 0x1F
		switch nalType {
		case 7:
			r.sps = append(r.sps[:0], nal...)
			continue
		case 8:
			r.pps = append(r.pps[:0], nal...)
			continue
		}
		if !isConfig {
			var lenBuf [4]byte
			binary.BigEndian.PutUint32(lenBuf[:], uint32(len(nal)))
			avccSample.Write(lenBuf[:])
			avccSample.Write(nal)
		}
	}

	if !r.initWritten && len(r.sps) > 0 && len(r.pps) > 0 {
		if err := r.writeInitBoxes(); err != nil {
			return err
		}
		r.initWritten = true
	}

	if isConfig || avccSample.Len() == 0 || !r.initWritten {
		return nil
	}

	// Flush accumulated fragment right before a new keyframe (or every 30 samples).
	if (isKeyframe && len(r.samples) > 0) || len(r.samples) >= 30 {
		if err := r.flushFragmentLocked(); err != nil {
			return err
		}
	}

	if !r.hasBasePts {
		r.basePtsUs = ptsUs
		r.hasBasePts = true
	}
	// Convert microseconds to 90kHz timescale (default 1/60s = 1500 ticks if non-monotonic/zero).
	const defaultDur = 1500
	var dur uint32 = defaultDur
	if ptsUs > r.basePtsUs {
		targetTick := ((ptsUs - r.basePtsUs) * 90) / 1000
		if targetTick > r.decodeTime {
			delta := targetTick - r.decodeTime
			if delta > 0 && delta < 90000 {
				dur = uint32(delta)
			}
		}
	}
	r.samples = append(r.samples, mp4Sample{
		size:       uint32(avccSample.Len()),
		duration:   dur,
		isKeyframe: isKeyframe,
	})
	r.mdatBuf.Write(avccSample.Bytes())
	return nil
}

// Close flushes remaining samples and closes the MP4 file.
func (r *MP4Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	var firstErr error
	if r.initWritten && len(r.samples) > 0 {
		firstErr = r.flushFragmentLocked()
	}
	if r.file != nil {
		if err := r.file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		r.file = nil
	}
	return firstErr
}

func (r *MP4Recorder) writeInitBoxes() error {
	ftyp := box("ftyp",
		[]byte("isom"),
		u32(0x00000200),
		[]byte("isomiso2avc1mp41"),
	)

	mvhd := fullBox("mvhd", 0, 0,
		u32(0), u32(0), // creation, modification
		u32(90000), // timescale
		u32(0),     // duration
		u32(0x00010000),
		u16(0x0100),
		make([]byte, 10),
		identityMatrix(),
		make([]byte, 24),
		u32(2), // next_track_ID
	)

	tkhd := fullBox("tkhd", 0, 3,
		u32(0), u32(0), // creation, modification
		u32(1), // track_ID
		u32(0), // reserved
		u32(0), // duration
		make([]byte, 8),
		u16(0), u16(0), u16(0), u16(0),
		identityMatrix(),
		u32(r.width<<16),
		u32(r.height<<16),
	)

	mdhd := fullBox("mdhd", 0, 0,
		u32(0), u32(0),
		u32(90000),
		u32(0),
		u16(0x55c4),
		u16(0),
	)
	hdlr := fullBox("hdlr", 0, 0,
		u32(0),
		[]byte("vide"),
		make([]byte, 12),
		[]byte("AndroFuse H.264\x00"),
	)
	vmhd := fullBox("vmhd", 0, 1, make([]byte, 8))
	urlBox := fullBox("url ", 0, 1)
	dref := fullBox("dref", 0, 0, u32(1), urlBox)
	dinf := box("dinf", dref)

	profileIdc := byte(0x42)
	compat := byte(0xE0)
	levelIdc := byte(0x1F)
	if len(r.sps) >= 4 {
		profileIdc = r.sps[1]
		compat = r.sps[2]
		levelIdc = r.sps[3]
	}
	avcC := box("avcC",
		[]byte{1, profileIdc, compat, levelIdc, 0xFF, 0xE1},
		u16(uint16(len(r.sps))),
		r.sps,
		[]byte{1},
		u16(uint16(len(r.pps))),
		r.pps,
	)
	compressor := make([]byte, 32)
	copy(compressor, "\x09AndroFuse")
	avc1 := box("avc1",
		make([]byte, 6),
		u16(1), // data_reference_index
		make([]byte, 16),
		u16(uint16(r.width)),
		u16(uint16(r.height)),
		u32(0x00480000),
		u32(0x00480000),
		u32(0),
		u16(1),
		compressor,
		u16(0x0018),
		u16(0xFFFF),
		avcC,
	)
	stsd := fullBox("stsd", 0, 0, u32(1), avc1)
	stts := fullBox("stts", 0, 0, u32(0))
	stsc := fullBox("stsc", 0, 0, u32(0))
	stsz := fullBox("stsz", 0, 0, u32(0), u32(0))
	stco := fullBox("stco", 0, 0, u32(0))
	stbl := box("stbl", stsd, stts, stsc, stsz, stco)
	minf := box("minf", vmhd, dinf, stbl)
	mdia := box("mdia", mdhd, hdlr, minf)
	trak := box("trak", tkhd, mdia)

	trex := fullBox("trex", 0, 0,
		u32(1),    // track_ID
		u32(1),    // default_sample_description_index
		u32(1500), // default_sample_duration
		u32(0),    // default_sample_size
		u32(0),    // default_sample_flags
	)
	mvex := box("mvex", trex)
	moov := box("moov", mvhd, trak, mvex)

	if _, err := r.file.Write(ftyp); err != nil {
		return err
	}
	_, err := r.file.Write(moov)
	return err
}

func (r *MP4Recorder) flushFragmentLocked() error {
	if len(r.samples) == 0 {
		return nil
	}
	mfhd := fullBox("mfhd", 0, 0, u32(r.seqNum))
	r.seqNum++

	tfhd := fullBox("tfhd", 0, 0x020000, u32(1)) // default-base-is-moof
	tfdt := fullBox("tfdt", 1, 0, u64(r.decodeTime))

	// trun flags: data-offset-present (0x1) | sample-duration (0x100) | sample-size (0x200) | sample-flags (0x400)
	const trunFlags = 0x000701
	var trunEntries bytes.Buffer
	for _, s := range r.samples {
		binary.Write(&trunEntries, binary.BigEndian, s.duration)
		binary.Write(&trunEntries, binary.BigEndian, s.size)
		var flags uint32 = 0x01010000 // non-sync sample
		if s.isKeyframe {
			flags = 0x02000000 // sync sample
		}
		binary.Write(&trunEntries, binary.BigEndian, flags)
		r.decodeTime += uint64(s.duration)
	}

	// Build temporary trun with placeholder data_offset = 0 to measure exact moof size.
	buildMoof := func(dataOffset uint32) []byte {
		trun := fullBox("trun", 0, trunFlags, u32(uint32(len(r.samples))), u32(dataOffset), trunEntries.Bytes())
		traf := box("traf", tfhd, tfdt, trun)
		return box("moof", mfhd, traf)
	}
	moofSize := len(buildMoof(0))
	moof := buildMoof(uint32(moofSize + 8)) // +8 bytes for mdat header
	mdat := box("mdat", r.mdatBuf.Bytes())

	r.samples = r.samples[:0]
	r.mdatBuf.Reset()

	if _, err := r.file.Write(moof); err != nil {
		return fmt.Errorf("write moof: %w", err)
	}
	if _, err := r.file.Write(mdat); err != nil {
		return fmt.Errorf("write mdat: %w", err)
	}
	return nil
}

func splitAnnexBNALUs(data []byte) [][]byte {
	var out [][]byte
	n := len(data)
	start := -1
	i := 0
	for i < n {
		scLen := 0
		if i+3 < n && data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1 {
			scLen = 4
		} else if i+2 < n && data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
			scLen = 3
		}
		if scLen > 0 {
			if start >= 0 && i > start {
				out = append(out, data[start:i])
			}
			i += scLen
			start = i
			continue
		}
		i++
	}
	if start >= 0 && start < n {
		out = append(out, data[start:n])
	}
	return out
}

func box(typ string, parts ...[]byte) []byte {
	total := 8
	for _, p := range parts {
		total += len(p)
	}
	buf := make([]byte, 8, total)
	binary.BigEndian.PutUint32(buf[0:4], uint32(total))
	copy(buf[4:8], typ[:4])
	for _, p := range parts {
		buf = append(buf, p...)
	}
	return buf
}

func fullBox(typ string, version byte, flags uint32, parts ...[]byte) []byte {
	hdr := []byte{version, byte(flags >> 16), byte(flags >> 8), byte(flags)}
	all := make([][]byte, 0, len(parts)+1)
	all = append(all, hdr)
	all = append(all, parts...)
	return box(typ, all...)
}

func u16(v uint16) []byte {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	return b[:]
}

func u32(v uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return b[:]
}

func u64(v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return b[:]
}

func identityMatrix() []byte {
	var b [36]byte
	binary.BigEndian.PutUint32(b[0:4], 0x00010000)
	binary.BigEndian.PutUint32(b[16:20], 0x00010000)
	binary.BigEndian.PutUint32(b[32:36], 0x40000000)
	return b[:]
}
