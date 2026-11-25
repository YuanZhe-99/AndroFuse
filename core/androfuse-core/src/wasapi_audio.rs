//! Shared-mode WASAPI audio output player fed by scrcpy raw 48kHz stereo PCM.

use std::collections::VecDeque;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex, mpsc};
use std::thread::{self, JoinHandle};
use std::time::Duration;
use windows::Win32::Media::Audio::{
    AUDCLNT_BUFFERFLAGS_SILENT, AUDCLNT_SHAREMODE_SHARED, AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM,
    AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY, IAudioCaptureClient, IAudioClient, IAudioRenderClient,
    IMMDeviceEnumerator, MMDeviceEnumerator, WAVEFORMATEX, eCapture, eConsole, eRender,
};
use windows::Win32::System::Com::{
    CLSCTX_ALL, COINIT_MULTITHREADED, CoCreateInstance, CoInitializeEx, CoUninitialize,
};

const WAVE_FORMAT_PCM: u16 = 1;
const MAX_RING_BYTES: usize = 48_000 * 4 / 2; // ~500 ms at 48kHz stereo 16-bit PCM

/// Bounded FIFO byte ring buffer keeping audio latency capped on jittery networks.
#[derive(Debug)]
pub struct PcmRingBuffer {
    cap: usize,
    buf: VecDeque<u8>,
}

impl PcmRingBuffer {
    pub fn new(cap: usize) -> Self {
        Self {
            cap: cap.max(4096),
            buf: VecDeque::with_capacity(cap.max(4096)),
        }
    }

    pub fn push(&mut self, data: &[u8]) {
        if data.is_empty() {
            return;
        }
        if data.len() >= self.cap {
            self.buf.clear();
            self.buf
                .extend(data[data.len() - self.cap..].iter().copied());
            return;
        }
        let needed = self.buf.len() + data.len();
        if needed > self.cap {
            let drop_n = needed - self.cap;
            self.buf.drain(..drop_n);
        }
        self.buf.extend(data.iter().copied());
    }

    pub fn pop_into(&mut self, dst: &mut [u8]) -> usize {
        let n = dst.len().min(self.buf.len());
        for slot in dst.iter_mut().take(n) {
            if let Some(b) = self.buf.pop_front() {
                *slot = b;
            }
        }
        n
    }

    pub fn len(&self) -> usize {
        self.buf.len()
    }

    pub fn is_empty(&self) -> bool {
        self.buf.is_empty()
    }
}

/// Active WASAPI shared-mode PCM render stream running on a dedicated COM thread.
pub struct WasapiAudioPlayer {
    ring: Arc<Mutex<PcmRingBuffer>>,
    stop: Arc<AtomicBool>,
    thread: Mutex<Option<JoinHandle<()>>>,
}

impl WasapiAudioPlayer {
    /// Opens the default Windows audio render endpoint at `sample_rate` Hz and `channels`.
    pub fn open(sample_rate: u32, channels: u16) -> Result<Self, String> {
        let rate = if sample_rate == 0 {
            48_000
        } else {
            sample_rate
        };
        let ch = if channels == 0 { 2 } else { channels };
        let ring = Arc::new(Mutex::new(PcmRingBuffer::new(MAX_RING_BYTES)));
        let stop = Arc::new(AtomicBool::new(false));
        let (tx, rx) = mpsc::sync_channel::<Result<(), String>>(1);

        let ring_t = Arc::clone(&ring);
        let stop_t = Arc::clone(&stop);
        let handle = thread::Builder::new()
            .name("androfuse-wasapi".to_string())
            .spawn(move || {
                // SAFETY: Initializes COM on this dedicated WASAPI worker thread.
                let co_hr = unsafe { CoInitializeEx(None, COINIT_MULTITHREADED) };
                let com_ok = co_hr.is_ok();
                let res = run_wasapi_loop(rate, ch, &ring_t, &stop_t, tx);
                let _ = res;
                if com_ok {
                    // SAFETY: Pairs with the successful `CoInitializeEx` on this same thread.
                    unsafe { CoUninitialize() };
                }
            })
            .map_err(|e| format!("spawn wasapi thread: {e}"))?;

        match rx.recv_timeout(Duration::from_secs(4)) {
            Ok(Ok(())) => Ok(Self {
                ring,
                stop,
                thread: Mutex::new(Some(handle)),
            }),
            Ok(Err(e)) => {
                stop.store(true, Ordering::Release);
                let _ = handle.join();
                Err(e)
            }
            Err(_) => {
                stop.store(true, Ordering::Release);
                let _ = handle.join();
                Err("timed out initializing WASAPI audio client".to_string())
            }
        }
    }

    /// Enqueues signed 16-bit interleaved PCM frames for playback.
    pub fn push_pcm(&self, data: &[u8]) {
        if let Ok(mut g) = self.ring.lock() {
            g.push(data);
        }
    }
}

impl Drop for WasapiAudioPlayer {
    fn drop(&mut self) {
        self.stop.store(true, Ordering::Release);
        if let Ok(mut guard) = self.thread.lock()
            && let Some(h) = guard.take()
        {
            let _ = h.join();
        }
    }
}

fn run_wasapi_loop(
    sample_rate: u32,
    channels: u16,
    ring: &Arc<Mutex<PcmRingBuffer>>,
    stop: &Arc<AtomicBool>,
    ready_tx: mpsc::SyncSender<Result<(), String>>,
) -> Result<(), String> {
    let init = (|| -> Result<(IAudioClient, IAudioRenderClient, u32, usize), String> {
        // SAFETY: COM is initialized on this thread; instantiates standard MMDeviceEnumerator.
        let enumerator: IMMDeviceEnumerator =
            unsafe { CoCreateInstance(&MMDeviceEnumerator, None, CLSCTX_ALL) }
                .map_err(|e| format!("MMDeviceEnumerator: {e}"))?;
        // SAFETY: Queries default console render audio endpoint.
        let device = unsafe { enumerator.GetDefaultAudioEndpoint(eRender, eConsole) }
            .map_err(|e| format!("GetDefaultAudioEndpoint: {e}"))?;
        // SAFETY: Activates IAudioClient interface on the default render device.
        let audio_client: IAudioClient = unsafe { device.Activate(CLSCTX_ALL, None) }
            .map_err(|e| format!("Activate IAudioClient: {e}"))?;

        let block_align = channels.saturating_mul(2);
        let wfx = WAVEFORMATEX {
            wFormatTag: WAVE_FORMAT_PCM,
            nChannels: channels,
            nSamplesPerSec: sample_rate,
            nAvgBytesPerSec: sample_rate.saturating_mul(u32::from(block_align)),
            nBlockAlign: block_align,
            wBitsPerSample: 16,
            cbSize: 0,
        };
        let stream_flags =
            AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM | AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY;
        // SAFETY: `wfx` lives for the duration of `Initialize`; 40ms buffer in 100ns units.
        unsafe {
            audio_client.Initialize(
                AUDCLNT_SHAREMODE_SHARED,
                stream_flags,
                400_000,
                0,
                &wfx,
                None,
            )
        }
        .map_err(|e| format!("IAudioClient::Initialize: {e}"))?;

        // SAFETY: Queries buffer frame count after successful initialization.
        let buffer_frames =
            unsafe { audio_client.GetBufferSize() }.map_err(|e| format!("GetBufferSize: {e}"))?;
        // SAFETY: Obtains IAudioRenderClient service from initialized IAudioClient.
        let render_client: IAudioRenderClient = unsafe { audio_client.GetService() }
            .map_err(|e| format!("GetService IAudioRenderClient: {e}"))?;
        // SAFETY: Starts WASAPI render stream.
        unsafe { audio_client.Start() }.map_err(|e| format!("IAudioClient::Start: {e}"))?;

        Ok((
            audio_client,
            render_client,
            buffer_frames,
            usize::from(block_align),
        ))
    })();

    let (audio_client, render_client, buffer_frames, frame_bytes) = match init {
        Ok(v) => {
            let _ = ready_tx.send(Ok(()));
            v
        }
        Err(e) => {
            let _ = ready_tx.send(Err(e.clone()));
            return Err(e);
        }
    };

    let mut scratch = vec![0u8; buffer_frames as usize * frame_bytes];
    while !stop.load(Ordering::Acquire) {
        // SAFETY: Reads current queued padding frames from active IAudioClient.
        let padding = unsafe { audio_client.GetCurrentPadding() }.unwrap_or(buffer_frames);
        let avail = buffer_frames.saturating_sub(padding);
        if avail > 0 {
            let need_bytes = avail as usize * frame_bytes;
            if scratch.len() < need_bytes {
                scratch.resize(need_bytes, 0);
            }
            let got_bytes = if let Ok(mut g) = ring.lock() {
                g.pop_into(&mut scratch[..need_bytes])
            } else {
                0
            };
            // SAFETY: Requests writable buffer for `avail` frames from WASAPI render client.
            if let Ok(dst_ptr) = unsafe { render_client.GetBuffer(avail) } {
                if got_bytes == 0 {
                    // SAFETY: Releases silent buffer when ring is empty.
                    let _ = unsafe {
                        render_client.ReleaseBuffer(avail, AUDCLNT_BUFFERFLAGS_SILENT.0 as u32)
                    };
                } else {
                    scratch[got_bytes..need_bytes].fill(0);
                    // SAFETY: `dst_ptr` points to `avail * frame_bytes` writable bytes owned by WASAPI until ReleaseBuffer.
                    unsafe {
                        std::ptr::copy_nonoverlapping(scratch.as_ptr(), dst_ptr, need_bytes);
                        let _ = render_client.ReleaseBuffer(avail, 0);
                    }
                }
            }
        }
        thread::sleep(Duration::from_millis(8));
    }

    // SAFETY: Stops active WASAPI render stream prior to releasing COM interfaces.
    let _ = unsafe { audio_client.Stop() };
    Ok(())
}

/// Active WASAPI shared-mode PCM capture stream reading from the default Windows microphone.
pub struct WasapiMicCapturer {
    ring: Arc<Mutex<PcmRingBuffer>>,
    stop: Arc<AtomicBool>,
    thread: Mutex<Option<JoinHandle<()>>>,
}

impl WasapiMicCapturer {
    /// Opens the default Windows audio capture endpoint at `sample_rate` Hz and `channels`.
    pub fn open(sample_rate: u32, channels: u16) -> Result<Self, String> {
        let rate = if sample_rate == 0 {
            48_000
        } else {
            sample_rate
        };
        let ch = if channels == 0 { 1 } else { channels };
        let ring = Arc::new(Mutex::new(PcmRingBuffer::new(MAX_RING_BYTES)));
        let stop = Arc::new(AtomicBool::new(false));
        let (tx, rx) = mpsc::sync_channel::<Result<(), String>>(1);

        let ring_t = Arc::clone(&ring);
        let stop_t = Arc::clone(&stop);
        let handle = thread::Builder::new()
            .name("androfuse-wasapi-mic".to_string())
            .spawn(move || {
                // SAFETY: Initializes COM on this dedicated WASAPI capture worker thread.
                let co_hr = unsafe { CoInitializeEx(None, COINIT_MULTITHREADED) };
                let com_ok = co_hr.is_ok();
                let _ = run_wasapi_capture_loop(rate, ch, &ring_t, &stop_t, tx);
                if com_ok {
                    // SAFETY: Pairs with the successful `CoInitializeEx` on this same thread.
                    unsafe { CoUninitialize() };
                }
            })
            .map_err(|e| format!("spawn wasapi mic thread: {e}"))?;

        match rx.recv_timeout(Duration::from_secs(4)) {
            Ok(Ok(())) => Ok(Self {
                ring,
                stop,
                thread: Mutex::new(Some(handle)),
            }),
            Ok(Err(e)) => {
                stop.store(true, Ordering::Release);
                let _ = handle.join();
                Err(e)
            }
            Err(_) => {
                stop.store(true, Ordering::Release);
                let _ = handle.join();
                Err("timed out initializing WASAPI capture client".to_string())
            }
        }
    }

    /// Reads captured signed 16-bit interleaved PCM frames into `dst`.
    pub fn read_pcm(&self, dst: &mut [u8]) -> usize {
        if dst.is_empty() {
            return 0;
        }
        if let Ok(mut g) = self.ring.lock() {
            g.pop_into(dst)
        } else {
            0
        }
    }
}

impl Drop for WasapiMicCapturer {
    fn drop(&mut self) {
        self.stop.store(true, Ordering::Release);
        if let Ok(mut guard) = self.thread.lock()
            && let Some(h) = guard.take()
        {
            let _ = h.join();
        }
    }
}

fn run_wasapi_capture_loop(
    sample_rate: u32,
    channels: u16,
    ring: &Arc<Mutex<PcmRingBuffer>>,
    stop: &Arc<AtomicBool>,
    ready_tx: mpsc::SyncSender<Result<(), String>>,
) -> Result<(), String> {
    let init = (|| -> Result<(IAudioClient, IAudioCaptureClient, usize), String> {
        // SAFETY: COM is initialized on this thread; instantiates standard MMDeviceEnumerator.
        let enumerator: IMMDeviceEnumerator =
            unsafe { CoCreateInstance(&MMDeviceEnumerator, None, CLSCTX_ALL) }
                .map_err(|e| format!("MMDeviceEnumerator: {e}"))?;
        // SAFETY: Queries default console capture (microphone) audio endpoint.
        let device = unsafe { enumerator.GetDefaultAudioEndpoint(eCapture, eConsole) }
            .map_err(|e| format!("GetDefaultAudioEndpoint(eCapture): {e}"))?;
        // SAFETY: Activates IAudioClient interface on the default capture device.
        let audio_client: IAudioClient = unsafe { device.Activate(CLSCTX_ALL, None) }
            .map_err(|e| format!("Activate capture IAudioClient: {e}"))?;

        let block_align = channels.saturating_mul(2);
        let wfx = WAVEFORMATEX {
            wFormatTag: WAVE_FORMAT_PCM,
            nChannels: channels,
            nSamplesPerSec: sample_rate,
            nAvgBytesPerSec: sample_rate.saturating_mul(u32::from(block_align)),
            nBlockAlign: block_align,
            wBitsPerSample: 16,
            cbSize: 0,
        };
        let stream_flags =
            AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM | AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY;
        // SAFETY: `wfx` lives for the duration of `Initialize`; 40ms capture buffer.
        unsafe {
            audio_client.Initialize(
                AUDCLNT_SHAREMODE_SHARED,
                stream_flags,
                400_000,
                0,
                &wfx,
                None,
            )
        }
        .map_err(|e| format!("capture IAudioClient::Initialize: {e}"))?;

        // SAFETY: Obtains IAudioCaptureClient service from initialized IAudioClient.
        let capture_client: IAudioCaptureClient = unsafe { audio_client.GetService() }
            .map_err(|e| format!("GetService IAudioCaptureClient: {e}"))?;
        // SAFETY: Starts WASAPI capture stream.
        unsafe { audio_client.Start() }.map_err(|e| format!("capture IAudioClient::Start: {e}"))?;
        Ok((audio_client, capture_client, usize::from(block_align)))
    })();

    let (audio_client, capture_client, frame_bytes) = match init {
        Ok(v) => {
            let _ = ready_tx.send(Ok(()));
            v
        }
        Err(e) => {
            let _ = ready_tx.send(Err(e.clone()));
            return Err(e);
        }
    };

    let mut silence_scratch = Vec::new();
    while !stop.load(Ordering::Acquire) {
        // SAFETY: Queries next available captured packet size in frames.
        while let Ok(pkt_frames) = unsafe { capture_client.GetNextPacketSize() } {
            if pkt_frames == 0 || stop.load(Ordering::Acquire) {
                break;
            }
            let mut data_ptr = std::ptr::null_mut();
            let mut num_frames = 0u32;
            let mut flags = 0u32;
            // SAFETY: Pointers receive WASAPI buffer slice for the queued capture packet.
            let hr = unsafe {
                capture_client.GetBuffer(&mut data_ptr, &mut num_frames, &mut flags, None, None)
            };
            if hr.is_err() {
                break;
            }
            let byte_len = num_frames as usize * frame_bytes;
            if byte_len > 0 {
                if (flags & (AUDCLNT_BUFFERFLAGS_SILENT.0 as u32)) != 0 || data_ptr.is_null() {
                    if silence_scratch.len() < byte_len {
                        silence_scratch.resize(byte_len, 0);
                    } else {
                        silence_scratch[..byte_len].fill(0);
                    }
                    if let Ok(mut g) = ring.lock() {
                        g.push(&silence_scratch[..byte_len]);
                    }
                } else {
                    // SAFETY: `data_ptr` points to `byte_len` valid bytes owned by WASAPI until ReleaseBuffer.
                    let pcm = unsafe { std::slice::from_raw_parts(data_ptr, byte_len) };
                    if let Ok(mut g) = ring.lock() {
                        g.push(pcm);
                    }
                }
            }
            // SAFETY: Releases the capture packet acquired by GetBuffer.
            let _ = unsafe { capture_client.ReleaseBuffer(num_frames) };
        }
        thread::sleep(Duration::from_millis(8));
    }

    // SAFETY: Stops active WASAPI capture stream prior to releasing COM interfaces.
    let _ = unsafe { audio_client.Stop() };
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn ring_buffer_push_pop_and_drop_oldest() {
        let mut ring = PcmRingBuffer::new(4096);
        let chunk = vec![0x2A_u8; 3000];
        ring.push(&chunk);
        assert_eq!(ring.len(), 3000);

        let extra = vec![0x7B_u8; 2000];
        ring.push(&extra);
        // 3000 + 2000 > 4096 -> oldest 904 bytes dropped, leaving 2096 of 0x2A + 2000 of 0x7B.
        assert_eq!(ring.len(), 4096);

        let mut out = vec![0u8; 4096];
        let popped = ring.pop_into(&mut out);
        assert_eq!(popped, 4096);
        assert_eq!(ring.len(), 0);
        assert!(out[..2096].iter().all(|&b| b == 0x2A));
        assert!(out[2096..].iter().all(|&b| b == 0x7B));
    }
}
