//! Miracast / Wi-Fi Direct Sink protocol state machine, RTP/MPEG-TS demuxer,
//! UIBC input packets, and Windows `WiFiDirectAdvertisementPublisher` /
//! `wlanapi.dll!WlanNotifyVsIeProviderInt` sink receiver.

use serde::Serialize;
use std::collections::{BTreeMap, HashMap};

/// Default Wi-Fi Display RTSP control port.
pub const DEFAULT_WFD_RTSP_PORT: u16 = 7236;

/// Live Miracast receiver connection state surfaced to `androfuse_miracast_poll_nv12`.
pub const MIRACAST_STATE_LISTENING: u32 = 0;
pub const MIRACAST_STATE_NEGOTIATING: u32 = 1;
pub const MIRACAST_STATE_STREAMING: u32 = 2;
pub const MIRACAST_STATE_STOPPED: u32 = 3;

/// Default WFD `wfd_video_formats` capability string advertising 1080p60/30 + 720p60/30 CEA modes.
pub const WFD_VIDEO_FORMATS_1080P60: &str =
    "40 00 02 10 000001E1 00000000 00000000 00 0000 0000 00 none none";

/// CEA/VESA video mode negotiated during WFD RTSP `SET_PARAMETER` (`wfd_video_formats`).
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
pub struct VideoMode {
    pub width: u32,
    pub height: u32,
    pub fps: u32,
}

impl VideoMode {
    /// Parses the WFD `wfd_video_formats` line and returns the real negotiated resolution & FPS.
    pub fn from_wfd(value: &str) -> Result<Self, String> {
        let fields: Vec<_> = value.split_whitespace().collect();
        if fields.len() < 7 {
            return Err("incomplete wfd_video_formats line".to_string());
        }
        let cea = u32::from_str_radix(fields[4], 16).map_err(|e| format!("bad CEA mask: {e}"))?;
        let vesa = u32::from_str_radix(fields[5], 16).map_err(|e| format!("bad VESA mask: {e}"))?;
        let hh = u32::from_str_radix(fields[6], 16).map_err(|e| format!("bad HH mask: {e}"))?;
        if vesa != 0 || hh != 0 {
            return Err("unsupported non-CEA WFD video format".to_string());
        }
        let (width, height, fps) = match cea {
            1 => (640, 480, 60),
            0x20 => (1280, 720, 30),
            0x40 => (1280, 720, 60),
            0x80 => (1920, 1080, 30),
            0x100 => (1920, 1080, 60),
            0x400 => (1280, 720, 25),
            0x800 => (1280, 720, 50),
            0x1000 => (1920, 1080, 25),
            0x2000 => (1920, 1080, 50),
            0x8000 => (1280, 720, 24),
            0x10000 => (1920, 1080, 24),
            _ => return Err(format!("unsupported CEA mode 0x{cea:08x}")),
        };
        Ok(Self { width, height, fps })
    }
}

/// Parsed WFD RTSP request or response message.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RtspMessage {
    pub start: String,
    pub headers: HashMap<String, String>,
    pub body: String,
}

impl RtspMessage {
    /// Extracts the `CSeq` integer header.
    pub fn sequence(&self) -> Result<u32, String> {
        self.headers
            .get("cseq")
            .ok_or_else(|| "missing CSeq header".to_string())?
            .parse::<u32>()
            .map_err(|e| format!("invalid CSeq: {e}"))
    }

    /// Reports whether this message is an `RTSP/...` response rather than a request.
    pub fn is_response(&self) -> bool {
        self.start.starts_with("RTSP/")
    }

    /// Returns the RTSP method verb, or `"RTSP/..."` if this message is a response.
    pub fn method(&self) -> &str {
        self.start.split_whitespace().next().unwrap_or("")
    }
}

/// Incremental RTSP header+body framer rejecting duplicate conflicting headers and oversized bodies.
#[derive(Default)]
pub struct RtspFramer {
    bytes: Vec<u8>,
}

impl RtspFramer {
    /// Appends raw TCP bytes into the framer buffer (96 KiB cap).
    pub fn push(&mut self, data: &[u8]) -> Result<(), String> {
        if self.bytes.len() + data.len() > 98_304 {
            return Err("RTSP message exceeded 96 KiB buffer".to_string());
        }
        self.bytes.extend_from_slice(data);
        Ok(())
    }

    /// Parses and drains the next complete RTSP message, if present.
    pub fn next_message(&mut self) -> Result<Option<RtspMessage>, String> {
        let Some(end) = self.bytes.windows(4).position(|w| w == b"\r\n\r\n") else {
            return Ok(None);
        };
        let head = std::str::from_utf8(&self.bytes[..end]).map_err(|e| e.to_string())?;
        let mut lines = head.split("\r\n");
        let start = lines
            .next()
            .ok_or_else(|| "empty RTSP header".to_string())?
            .to_owned();
        let mut headers = HashMap::new();
        for line in lines {
            let (k, v) = line
                .split_once(':')
                .ok_or_else(|| "malformed RTSP header line".to_string())?;
            let key = k.trim().to_ascii_lowercase();
            let val = v.trim().to_owned();
            if let Some(prev) = headers.insert(key, val.clone())
                && prev != val
            {
                return Err("conflicting duplicate RTSP headers".to_string());
            }
        }
        let content_len = headers
            .get("content-length")
            .map(|v| v.parse::<usize>())
            .transpose()
            .map_err(|e| format!("invalid content-length: {e}"))?
            .unwrap_or(0);
        if content_len > 65_536 {
            return Err("RTSP content-length exceeded 64 KiB".to_string());
        }
        let total = end + 4 + content_len;
        if self.bytes.len() < total {
            return Ok(None);
        }
        let body = std::str::from_utf8(&self.bytes[end + 4..total])
            .map_err(|e| e.to_string())?
            .to_owned();
        self.bytes.drain(..total);
        Ok(Some(RtspMessage {
            start,
            headers,
            body,
        }))
    }
}

/// Formats an RTSP message with `CSeq`, `User-Agent`, optional extra headers, and `Content-Length`.
pub fn format_rtsp(start: &str, sequence: u32, body: &str, extra: &str) -> String {
    let content = if body.is_empty() {
        String::new()
    } else {
        format!(
            "Content-Type: text/parameters\r\nContent-Length: {}\r\n",
            body.len()
        )
    };
    format!(
        "{start}\r\nCSeq: {sequence}\r\nUser-Agent: AndroFuse/0.1\r\n{extra}{content}\r\n{body}"
    )
}

/// Parses `key: value` lines from a WFD RTSP `SET_PARAMETER` body.
pub fn parse_wfd_parameters(body: &str) -> HashMap<String, String> {
    body.lines()
        .filter_map(|line| line.split_once(':'))
        .map(|(k, v)| (k.trim().to_ascii_lowercase(), v.trim().to_owned()))
        .collect()
}

/// Formats Sink capability answers for every parameter requested in a Source `GET_PARAMETER` body.
pub fn format_wfd_capabilities(request: &str, rtp_port: u16) -> String {
    let rtp_line = format!("RTP/AVP/UDP;unicast {rtp_port} 0 mode=play");
    let mut body = String::new();
    for name in request
        .lines()
        .map(|line| line.split(':').next().unwrap_or("").trim())
        .filter(|name| !name.is_empty())
    {
        let value = match name.to_ascii_lowercase().as_str() {
            "wfd_video_formats" => WFD_VIDEO_FORMATS_1080P60,
            "wfd_audio_codecs" => "LPCM 00000002 00",
            "wfd_client_rtp_ports" => rtp_line.as_str(),
            "wfd_content_protection" => "none",
            "wfd_display_edid" => "none",
            "wfd_coupled_sink" => "none",
            "wfd_connector_type" => "05",
            "wfd_idr_request_capability" => "1",
            "wfd_uibc_capability" => {
                "input_category_list=GENERIC, HIDC;generic_cap_list=Mouse, SingleTouch, MultiTouch;hidc_cap_list=Keyboard/USB;port=none"
            }
            _ => "none",
        };
        body.push_str(&format!("{name}: {value}\r\n"));
    }
    body
}

/// Parses Source `wfd_uibc_capability` into `(uibc_port, supports_generic_pointer, supports_hidc_keyboard)`.
pub fn parse_uibc_capability(value: &str) -> (u16, bool, bool) {
    let values: HashMap<String, String> = value
        .split(';')
        .filter_map(|part| part.split_once('='))
        .map(|(k, v)| (k.trim().to_ascii_lowercase(), v.trim().to_owned()))
        .collect();
    let has = |key: &str, item: &str| {
        values
            .get(key)
            .is_some_and(|s| s.split(',').any(|p| p.trim().eq_ignore_ascii_case(item)))
    };
    let port = values
        .get("port")
        .and_then(|v| v.parse::<u16>().ok())
        .unwrap_or(0);
    let pointer = has("input_category_list", "GENERIC")
        && (has("generic_cap_list", "Mouse") || has("generic_cap_list", "SingleTouch"));
    let keyboard = has("input_category_list", "HIDC") && has("hidc_cap_list", "Keyboard/USB");
    (port, pointer, keyboard)
}

/// Builds the UIBC `HIDC` USB boot keyboard report descriptor registration packet.
pub fn uibc_keyboard_descriptor() -> Vec<u8> {
    const DESCRIPTOR: &[u8] = &[
        0x05, 0x01, 0x09, 0x06, 0xa1, 0x01, 0x85, 0x29, 0x05, 0x07, 0x19, 0xe0, 0x29, 0xe7, 0x15,
        0x00, 0x25, 0x01, 0x75, 0x01, 0x95, 0x08, 0x81, 0x02, 0x95, 0x01, 0x75, 0x08, 0x81, 0x03,
        0x95, 0x05, 0x75, 0x01, 0x05, 0x08, 0x19, 0x01, 0x29, 0x05, 0x91, 0x02, 0x95, 0x01, 0x75,
        0x03, 0x91, 0x03, 0x95, 0x06, 0x75, 0x08, 0x15, 0x00, 0x25, 0x65, 0x05, 0x07, 0x19, 0x00,
        0x29, 0x65, 0x81, 0x00, 0xc0,
    ];
    let length = (9 + DESCRIPTOR.len() + 1) & !1;
    let mut out = vec![0u8; length];
    out[1] = 1;
    out[2..4].copy_from_slice(&(length as u16).to_be_bytes());
    out[4] = 1;
    out[5] = 0;
    out[6] = 1;
    out[7..9].copy_from_slice(&(DESCRIPTOR.len() as u16).to_be_bytes());
    out[9..9 + DESCRIPTOR.len()].copy_from_slice(DESCRIPTOR);
    out
}

/// Extracts `(ssrc, seq_num, mpegts_payload)` from an RTP packet carrying MPEG-TS (PT=33).
pub fn rtp_payload(packet: &[u8]) -> Option<(u32, u16, &[u8])> {
    if packet.len() < 12 || (packet[0] >> 6) != 2 || (packet[1] & 0x7f) != 33 {
        return None;
    }
    let mut start = 12 + usize::from(packet[0] & 0x0f) * 4;
    if start > packet.len() {
        return None;
    }
    if packet[0] & 0x10 != 0 {
        if start + 4 > packet.len() {
            return None;
        }
        let words = u16::from_be_bytes([packet[start + 2], packet[start + 3]]);
        start += 4 + usize::from(words) * 4;
    }
    let mut end = packet.len();
    if packet[0] & 0x20 != 0 {
        let padding = usize::from(*packet.last()?);
        if padding == 0 || padding > end.saturating_sub(start) {
            return None;
        }
        end -= padding;
    }
    if start >= end || !(end - start).is_multiple_of(188) || packet[start] != 0x47 {
        return None;
    }
    Some((
        u32::from_be_bytes(packet[8..12].try_into().ok()?),
        u16::from_be_bytes(packet[2..4].try_into().ok()?),
        &packet[start..end],
    ))
}

/// Bounded RTP sequence reorder buffer resilient to wraparound and packet loss.
#[derive(Default)]
pub struct RtpReorder {
    source: Option<u32>,
    next: u16,
    pending: BTreeMap<u16, Vec<u8>>,
    gap_since: Option<u64>,
}

impl RtpReorder {
    /// Pushes one RTP payload and returns in-order chunks plus count of skipped sequence numbers.
    pub fn push(
        &mut self,
        source: u32,
        sequence: u16,
        payload: &[u8],
        now_ms: u64,
    ) -> (Vec<Vec<u8>>, u16) {
        if self.source != Some(source) {
            self.source = Some(source);
            self.next = sequence;
            self.pending.clear();
            self.gap_since = None;
        }
        if (sequence.wrapping_sub(self.next) as i16) >= 0 {
            self.pending
                .entry(sequence)
                .or_insert_with(|| payload.to_vec());
        }
        self.flush(now_ms)
    }

    /// Flushes contiguous packets or advances past a stalled gap after 20ms or 64 queued packets.
    pub fn flush(&mut self, now_ms: u64) -> (Vec<Vec<u8>>, u16) {
        let mut ready = Vec::new();
        self.drain(&mut ready);
        let mut skipped = 0;
        if self.pending.is_empty() {
            self.gap_since = None;
        } else {
            let since = *self.gap_since.get_or_insert(now_ms);
            if (self.pending.len() >= 64 || now_ms.saturating_sub(since) >= 20)
                && let Some(nearest) = self
                    .pending
                    .keys()
                    .copied()
                    .min_by_key(|s| s.wrapping_sub(self.next))
            {
                skipped = nearest.wrapping_sub(self.next);
                self.next = nearest;
                self.gap_since = None;
                self.drain(&mut ready);
                if !self.pending.is_empty() {
                    self.gap_since = Some(now_ms);
                }
            }
        }
        (ready, skipped)
    }

    fn drain(&mut self, ready: &mut Vec<Vec<u8>>) {
        while let Some(value) = self.pending.remove(&self.next) {
            ready.push(value);
            self.next = self.next.wrapping_add(1);
            self.gap_since = None;
        }
    }
}

/// Complete H.264 Annex-B access unit extracted from MPEG-TS PES packets.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ElementaryFrame {
    pub bytes: Vec<u8>,
    pub discontinuity: bool,
}

/// Incremental 188-byte MPEG-TS PAT/PMT/H.264 PES demuxer.
#[derive(Default)]
pub struct TransportStream {
    pmt_pid: Option<u16>,
    video_pid: Option<u16>,
    pes: Vec<u8>,
    expected_size: Option<usize>,
    continuity: Option<u8>,
    assembling: bool,
    discontinuity: bool,
}

impl TransportStream {
    /// Pushes a slice of 188-byte TS packets and emits assembled H.264 elementary frames.
    pub fn push(&mut self, data: &[u8]) -> Result<Vec<ElementaryFrame>, String> {
        let mut frames = Vec::new();
        for packet in data.as_chunks::<188>().0 {
            if packet[0] != 0x47 {
                return Err("invalid MPEG-TS sync byte".to_string());
            }
            if packet[1] & 0x80 != 0 {
                self.discard();
                continue;
            }
            let pid = (u16::from(packet[1] & 31) << 8) | u16::from(packet[2]);
            let start = packet[1] & 0x40 != 0;
            let adaptation = (packet[3] >> 4) & 3;
            if adaptation == 0 || adaptation == 2 {
                continue;
            }
            let mut offset = 4;
            if adaptation == 3 {
                offset += 1 + usize::from(packet[4]);
                if offset > 188 {
                    return Err("invalid MPEG-TS adaptation length".to_string());
                }
            }
            if offset == 188 {
                continue;
            }
            let payload = &packet[offset..];
            if pid == 0 && start {
                if let Some(sec) = ts_section(payload)
                    && sec.len() >= 12
                    && sec[0] == 0
                {
                    let end = sec.len().saturating_sub(4);
                    for item in sec[8..end].as_chunks::<4>().0 {
                        if item[0] != 0 || item[1] != 0 {
                            self.pmt_pid =
                                Some((u16::from(item[2] & 31) << 8) | u16::from(item[3]));
                            break;
                        }
                    }
                }
            } else if Some(pid) == self.pmt_pid && start {
                if let Some(sec) = ts_section(payload)
                    && sec.len() >= 16
                    && sec[0] == 2
                {
                    let info = (usize::from(sec[10] & 15) << 8) | usize::from(sec[11]);
                    let mut cursor = 12 + info;
                    let end = sec.len().saturating_sub(4);
                    while cursor + 5 <= end {
                        let kind = sec[cursor];
                        let stream_pid =
                            (u16::from(sec[cursor + 1] & 31) << 8) | u16::from(sec[cursor + 2]);
                        let extra =
                            (usize::from(sec[cursor + 3] & 15) << 8) | usize::from(sec[cursor + 4]);
                        if kind == 0x1b {
                            self.video_pid = Some(stream_pid);
                        }
                        cursor += 5 + extra;
                    }
                }
            } else if Some(pid) == self.video_pid {
                let cc = packet[3] & 15;
                let broken = self.continuity.is_some_and(|last| cc != ((last + 1) & 15));
                self.continuity = Some(cc);
                if broken {
                    self.discard();
                }
                if start {
                    if self.assembling
                        && let Some(frame) = self.finish(false)?
                    {
                        frames.push(frame);
                    }
                    self.pes.clear();
                    self.expected_size = None;
                    if payload.len() >= 9 && payload[..3] == [0, 0, 1] && payload[3] & 0xf0 == 0xe0
                    {
                        let len = u16::from_be_bytes([payload[4], payload[5]]);
                        self.expected_size = (len != 0).then(|| 6 + usize::from(len));
                        self.pes.extend_from_slice(payload);
                        self.assembling = true;
                    } else {
                        self.assembling = false;
                    }
                } else if self.assembling && !broken {
                    self.pes.extend_from_slice(payload);
                }
                if self
                    .expected_size
                    .is_some_and(|size| self.pes.len() >= size)
                    && let Some(frame) = self.finish(broken)?
                {
                    frames.push(frame);
                }
            }
        }
        Ok(frames)
    }

    /// Flushes any bounded frame currently buffered in `pes`.
    pub fn flush(&mut self) -> Result<Option<ElementaryFrame>, String> {
        self.finish(false)
    }

    /// Clears partial state and flags discontinuity on the next recovered picture.
    pub fn discard(&mut self) {
        self.pes.clear();
        self.expected_size = None;
        self.assembling = false;
        self.discontinuity = true;
    }

    fn finish(&mut self, discontinuity: bool) -> Result<Option<ElementaryFrame>, String> {
        self.assembling = false;
        let size = self.expected_size.take().unwrap_or(self.pes.len());
        if self.pes.len() < 9 || size > self.pes.len() || size < 9 {
            self.pes.clear();
            return Ok(None);
        }
        if self.pes[..3] != [0, 0, 1] || self.pes[3] & 0xf0 != 0xe0 {
            self.pes.clear();
            return Err("invalid H.264 video PES header".to_string());
        }
        let start = 9 + usize::from(self.pes[8]);
        if start > size {
            self.pes.clear();
            return Err("invalid PES header extension length".to_string());
        }
        let bytes = self.pes[start..size].to_vec();
        self.pes.clear();
        if bytes.is_empty() {
            return Ok(None);
        }
        let pending = std::mem::take(&mut self.discontinuity);
        Ok(Some(ElementaryFrame {
            bytes,
            discontinuity: discontinuity || pending,
        }))
    }
}

fn ts_section(payload: &[u8]) -> Option<&[u8]> {
    let start = 1 + usize::from(*payload.first()?);
    if start + 3 > payload.len() {
        return None;
    }
    let length =
        ((usize::from(payload[start + 1] & 15) << 8) | usize::from(payload[start + 2])) + 3;
    payload.get(start..start + length)
}

/// Encodes a Miracast UIBC `GENERIC` single-touch/mouse pointer packet (`action`: 0=down, 1=up, 2=move)
/// in pixel coordinates `(x_px, y_px)` inside the negotiated video resolution.
pub fn uibc_pointer(action: u8, x_px: u16, y_px: u16) -> [u8; 14] {
    let mut out = [0u8; 14];
    out[2..4].copy_from_slice(&14u16.to_be_bytes());
    out[4] = action;
    out[5..7].copy_from_slice(&7u16.to_be_bytes());
    out[7] = 1;
    out[9..11].copy_from_slice(&x_px.to_be_bytes());
    out[11..13].copy_from_slice(&y_px.to_be_bytes());
    out
}

/// Result of a safe Wi-Fi Direct / Miracast IE Sink advertisement self-test on Windows.
#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct MiracastSinkTestResult {
    pub port_7236_available: bool,
    pub publisher_started: bool,
    pub publisher_status: String,
    pub wfd_handle_opened: bool,
    pub ie_enable_error: i32,
    pub ie_sink_error: i32,
    pub cleaned_up: bool,
    pub notes: Vec<String>,
}

/// Miracast Sink is intentionally hidden and disabled across all Windows architectures because
/// third-party WFD Sink advertisement requires Microsoft's undocumented `wlanapi.dll!WlanNotifyVsIeProviderInt`
/// private interface, which lacks OS contract guarantees and triggered kernel-mode `BugCheck 0x0000007E`
/// (`qcwlan8280.sys`) during live wireless DeX association.
#[cfg(windows)]
fn check_miracast_driver_safety() -> Result<(), String> {
    Err(
        "Miracast Sink is hidden and disabled because it relies on the undocumented Windows \
         WlanNotifyVsIeProviderInt interface (triggered kernel BugCheck 0x0000007E in qcwlan8280.sys); \
         use USB ADB or encrypted TLS Wi-Fi ADB Native DeX"
            .to_string(),
    )
}

/// Runs a short-lived Wi-Fi Direct Sink advertisement lifecycle test (disabled: undocumented OS API).
pub fn test_sink_advertisement() -> Result<MiracastSinkTestResult, String> {
    #[cfg(windows)]
    {
        check_miracast_driver_safety()?;
        windows_sink::test_advertisement()
    }
    #[cfg(not(windows))]
    {
        Err("Miracast sink advertisement requires Windows".to_string())
    }
}

/// Latest decoded NV12 frame plus receiver state shared safely with polling callers.
#[cfg(windows)]
pub struct MiracastReceiver {
    stop: std::sync::Arc<std::sync::atomic::AtomicBool>,
    shared: std::sync::Arc<std::sync::Mutex<MiracastShared>>,
    worker: Option<std::thread::JoinHandle<()>>,
}

#[cfg(windows)]
#[derive(Default)]
struct MiracastShared {
    state: u32,
    latest_frame: Option<(crate::FrameMeta, Vec<u8>)>,
    uibc_tx: Option<std::sync::mpsc::SyncSender<Vec<u8>>>,
}

#[cfg(windows)]
impl MiracastReceiver {
    /// Starts the Windows Wi-Fi Direct Sink advertisement, outbound RTSP/UIBC client,
    /// paired UDP RTP/RTCP receiver, and Media Foundation H.264 decoder.
    pub fn start(rtsp_port: u16) -> Result<Self, String> {
        check_miracast_driver_safety()?;
        let stop = std::sync::Arc::new(std::sync::atomic::AtomicBool::new(false));
        let shared = std::sync::Arc::new(std::sync::Mutex::new(MiracastShared {
            state: MIRACAST_STATE_LISTENING,
            ..Default::default()
        }));
        let (init_tx, init_rx) = std::sync::mpsc::sync_channel::<Result<(), String>>(1);

        let stop_w = stop.clone();
        let shared_w = shared.clone();
        let worker = std::thread::Builder::new()
            .name("androfuse-miracast-sink".into())
            .spawn(move || windows_sink::run_receiver(rtsp_port, stop_w, shared_w, init_tx))
            .map_err(|e| format!("spawn miracast thread: {e}"))?;

        match init_rx.recv_timeout(std::time::Duration::from_secs(10)) {
            Ok(Ok(())) => Ok(Self {
                stop,
                shared,
                worker: Some(worker),
            }),
            Ok(Err(e)) => {
                stop.store(true, std::sync::atomic::Ordering::SeqCst);
                let _ = worker.join();
                Err(e)
            }
            Err(_) => {
                stop.store(true, std::sync::atomic::Ordering::SeqCst);
                let _ = worker.join();
                Err("timed out starting Miracast sink advertisement".into())
            }
        }
    }

    /// Copies the newest decoded NV12 frame into `dst` (matching `pull_nv12` semantics) and
    /// writes the receiver state (`MIRACAST_STATE_*`) into `*out_state`.
    pub fn poll_nv12(
        &self,
        dst: &mut [u8],
        out_meta: &mut crate::FrameMeta,
    ) -> Result<(i32, u32), String> {
        let mut guard = match self.shared.lock() {
            Ok(g) => g,
            Err(p) => p.into_inner(),
        };
        let state = guard.state;
        let Some((meta, buf)) = guard.latest_frame.as_ref() else {
            return Ok((2, state)); // PullNeedMoreInput
        };
        if dst.len() < buf.len() {
            *out_meta = *meta;
            return Ok((1, state)); // PullBufferTooSmall
        }
        dst[..buf.len()].copy_from_slice(buf);
        *out_meta = *meta;
        guard.latest_frame = None;
        Ok((0, state))
    }

    /// Queues one UIBC GENERIC pointer packet (`x_px, y_px` in video pixel coordinates).
    pub fn send_pointer(&self, action: u8, x_px: u16, y_px: u16) -> Result<(), String> {
        let guard = match self.shared.lock() {
            Ok(g) => g,
            Err(p) => p.into_inner(),
        };
        if let Some(tx) = guard.uibc_tx.as_ref() {
            let _ = tx.try_send(uibc_pointer(action, x_px, y_px).to_vec());
        }
        Ok(())
    }

    /// Signals shutdown and waits for clean reverse-order teardown of WFD publisher and IE provider.
    pub fn stop(&mut self) {
        self.stop.store(true, std::sync::atomic::Ordering::SeqCst);
        if let Ok(mut g) = self.shared.lock() {
            g.uibc_tx = None;
            g.state = MIRACAST_STATE_STOPPED;
        }
        if let Some(w) = self.worker.take() {
            let _ = w.join();
        }
    }
}

#[cfg(windows)]
impl Drop for MiracastReceiver {
    fn drop(&mut self) {
        self.stop();
    }
}

#[cfg(windows)]
#[allow(dead_code)]
mod windows_sink {
    use super::{
        DEFAULT_WFD_RTSP_PORT, MIRACAST_STATE_LISTENING, MIRACAST_STATE_NEGOTIATING,
        MIRACAST_STATE_STOPPED, MIRACAST_STATE_STREAMING, MiracastShared, MiracastSinkTestResult,
        RtpReorder, RtspFramer, TransportStream, VideoMode, format_rtsp, format_wfd_capabilities,
        parse_uibc_capability, parse_wfd_parameters, rtp_payload, uibc_keyboard_descriptor,
    };
    use std::io::{Read, Write};
    use std::net::{IpAddr, Ipv4Addr, Shutdown, SocketAddr, TcpListener, TcpStream, UdpSocket};
    use std::os::windows::io::FromRawSocket;
    use std::sync::atomic::{AtomicBool, Ordering};
    use std::sync::{Arc, Mutex, mpsc};
    use std::thread;
    use std::time::{Duration, Instant};
    use windows::Devices::WiFiDirect::{
        WiFiDirectAdvertisementListenStateDiscoverability, WiFiDirectAdvertisementPublisher,
        WiFiDirectAdvertisementPublisherStatus, WiFiDirectConnectionListener,
        WiFiDirectConnectionParameters, WiFiDirectConnectionRequest,
        WiFiDirectConnectionRequestedEventArgs, WiFiDirectDevice,
    };
    use windows::Foundation::{AsyncStatus, TypedEventHandler};
    use windows::Win32::Foundation::{FreeLibrary, HMODULE};
    use windows::Win32::Networking::WinSock::{
        AF_INET, FD_SET, FIONBIO, IN_ADDR, IN_ADDR_0, IPPROTO_TCP, SO_RCVBUF, SOCK_DGRAM,
        SOCK_STREAM, SOCKADDR, SOCKADDR_IN, SOCKET, SOCKET_ERROR, SOL_SOCKET, TIMEVAL, WSACleanup,
        WSADATA, WSAStartup, bind, closesocket, connect, ioctlsocket, select, setsockopt, socket,
    };
    use windows::Win32::System::LibraryLoader::{
        GetProcAddress, LOAD_LIBRARY_SEARCH_SYSTEM32, LoadLibraryExW,
    };
    use windows::Win32::System::WinRT::{RO_INIT_MULTITHREADED, RoInitialize, RoUninitialize};
    use windows::core::{GUID, PCSTR, PCWSTR};

    type WfdOpenHandleFn = unsafe extern "system" fn(u32, *mut u32, *mut isize) -> u32;
    type WfdCloseHandleFn = unsafe extern "system" fn(isize) -> u32;
    type WlanNotifyVsIeFn = unsafe extern "system" fn(GUID, u32, *const u8) -> u32;

    const MIRACAST_IE_PROVIDER_GUID: GUID = GUID::from_u128(0xb7d94b4d_5db1_4e70_b5c3_dd003eeebe66);

    struct Apartment(bool);
    impl Apartment {
        fn new() -> Self {
            // SAFETY: Standard WinRT MTA initialization paired with RoUninitialize on drop.
            let ok = unsafe { RoInitialize(RO_INIT_MULTITHREADED) }.is_ok();
            Self(ok)
        }
    }
    impl Drop for Apartment {
        fn drop(&mut self) {
            if self.0 {
                // SAFETY: Matches successful RoInitialize on this thread.
                unsafe { RoUninitialize() };
            }
        }
    }

    struct WsaGuard(bool);
    impl WsaGuard {
        fn new() -> Self {
            let mut data = WSADATA::default();
            // SAFETY: Standard WinSock 2.2 initialization paired with WSACleanup on drop.
            let rc = unsafe { WSAStartup(0x0202, &mut data) };
            Self(rc == 0)
        }
    }
    impl Drop for WsaGuard {
        fn drop(&mut self) {
            if self.0 {
                // SAFETY: Matches successful WSAStartup above.
                unsafe {
                    let _ = WSACleanup();
                }
            }
        }
    }

    struct SinkAdvertisement {
        publisher: WiFiDirectAdvertisementPublisher,
        listener: WiFiDirectConnectionListener,
        module: HMODULE,
        close: WfdCloseHandleFn,
        notify: WlanNotifyVsIeFn,
        handle: isize,
        enabled: bool,
        sink: bool,
    }

    impl SinkAdvertisement {
        fn new() -> Result<Self, String> {
            let publisher = WiFiDirectAdvertisementPublisher::new()
                .map_err(|e| format!("WiFiDirectAdvertisementPublisher::new: {e}"))?;
            let listener = WiFiDirectConnectionListener::new()
                .map_err(|e| format!("WiFiDirectConnectionListener::new: {e}"))?;
            let adv = publisher.Advertisement().map_err(|e| e.to_string())?;
            adv.SetIsAutonomousGroupOwnerEnabled(true)
                .map_err(|e| e.to_string())?;
            adv.SetListenStateDiscoverability(
                WiFiDirectAdvertisementListenStateDiscoverability::Normal,
            )
            .map_err(|e| e.to_string())?;

            let wlan_w: Vec<u16> = "wlanapi.dll\0".encode_utf16().collect();
            // SAFETY: NUL-terminated System32 module load.
            let module: HMODULE = unsafe {
                LoadLibraryExW(PCWSTR(wlan_w.as_ptr()), None, LOAD_LIBRARY_SEARCH_SYSTEM32)
            }
            .map_err(|e| format!("LoadLibraryExW(wlanapi.dll): {e}"))?;

            // SAFETY: Querying known WFD exports on valid `wlanapi.dll` module.
            let (open_p, close_p, notify_p) = unsafe {
                (
                    GetProcAddress(module, PCSTR(c"WFDOpenHandle".as_ptr().cast())),
                    GetProcAddress(module, PCSTR(c"WFDCloseHandle".as_ptr().cast())),
                    GetProcAddress(module, PCSTR(c"WlanNotifyVsIeProviderInt".as_ptr().cast())),
                )
            };
            let (Some(open_f), Some(close_f), Some(notify_f)) = (open_p, close_p, notify_p) else {
                // SAFETY: Freeing `wlanapi.dll` opened above on early error.
                unsafe {
                    let _ = FreeLibrary(module);
                }
                return Err("wlanapi.dll missing WFD entrypoints".into());
            };
            // SAFETY: Verified WFD function signatures.
            let (wfd_open, close, notify): (WfdOpenHandleFn, WfdCloseHandleFn, WlanNotifyVsIeFn) = unsafe {
                (
                    std::mem::transmute::<unsafe extern "system" fn() -> isize, WfdOpenHandleFn>(
                        open_f,
                    ),
                    std::mem::transmute::<unsafe extern "system" fn() -> isize, WfdCloseHandleFn>(
                        close_f,
                    ),
                    std::mem::transmute::<unsafe extern "system" fn() -> isize, WlanNotifyVsIeFn>(
                        notify_f,
                    ),
                )
            };

            let mut handle: isize = 0;
            let mut version: u32 = 0;
            // SAFETY: Stack output pointers for WFD handle open.
            let rc = unsafe { wfd_open(1, &mut version, &mut handle) };
            if rc != 0 || handle == 0 {
                // SAFETY: Freeing `wlanapi.dll` opened above on early error.
                unsafe {
                    let _ = FreeLibrary(module);
                }
                return Err(format!("WFDOpenHandle failed with error {rc}"));
            }

            Ok(Self {
                publisher,
                listener,
                module,
                close,
                notify,
                handle,
                enabled: false,
                sink: false,
            })
        }

        fn start(&mut self, port: u16) -> Result<(), String> {
            self.publisher
                .Start()
                .map_err(|e| format!("WiFiDirectAdvertisementPublisher::Start: {e}"))?;
            // Wait until Hosted Network reaches Started BEFORE calling WlanNotifyVsIeProviderInt.
            let deadline = Instant::now() + Duration::from_secs(8);
            loop {
                let status = self
                    .publisher
                    .Status()
                    .map_err(|e| format!("publisher.Status: {e}"))?;
                match status {
                    WiFiDirectAdvertisementPublisherStatus::Started => break,
                    WiFiDirectAdvertisementPublisherStatus::Aborted => {
                        return Err(
                            "Wi-Fi Direct publisher aborted (turn on Wi-Fi and close Windows Wireless Display)"
                                .into(),
                        );
                    }
                    _ => {}
                }
                if Instant::now() > deadline {
                    return Err("timed out waiting for Wi-Fi Direct publisher to start".into());
                }
                thread::sleep(Duration::from_millis(40));
            }
            self.command(0, 0, u32::from(port))?;
            self.enabled = true;
            self.command(3, 1, 0)?;
            self.sink = true;
            Ok(())
        }

        fn command(&self, cmd: u32, role: u32, port: u32) -> Result<(), String> {
            let mut msg = [0u8; 20];
            msg[..4].copy_from_slice(&cmd.to_le_bytes());
            msg[4..8].copy_from_slice(&role.to_le_bytes());
            msg[16..20].copy_from_slice(&port.to_le_bytes());
            // SAFETY: Passing valid 20-byte IE command buffer to WlanNotifyVsIeProviderInt.
            let rc = unsafe { (self.notify)(MIRACAST_IE_PROVIDER_GUID, 20, msg.as_ptr()) };
            if rc != 0 {
                return Err(format!(
                    "WlanNotifyVsIeProviderInt(cmd={cmd}) failed ({rc})"
                ));
            }
            Ok(())
        }
    }

    impl Drop for SinkAdvertisement {
        fn drop(&mut self) {
            if self.sink {
                let _ = self.command(3, 0, 0);
            }
            if self.enabled {
                let _ = self.command(1, 0, 0);
            }
            if self.publisher.Status().ok() == Some(WiFiDirectAdvertisementPublisherStatus::Started)
            {
                let _ = self.publisher.Stop();
            }
            // SAFETY: Closing valid WFD handle and freeing `wlanapi.dll` module in reverse order.
            unsafe {
                let _ = (self.close)(self.handle);
                let _ = FreeLibrary(self.module);
            }
        }
    }

    pub(super) fn test_advertisement() -> Result<MiracastSinkTestResult, String> {
        let _apt = Apartment::new();
        let mut notes = Vec::new();
        let port_7236_available = TcpListener::bind(("0.0.0.0", DEFAULT_WFD_RTSP_PORT)).is_ok();
        if !port_7236_available {
            notes.push(
                "TCP port 7236 is busy (likely held by Windows Wireless Display receiver)".into(),
            );
        }

        let mut adv = SinkAdvertisement::new()?;
        let start_res = adv.start(DEFAULT_WFD_RTSP_PORT);
        let publisher_status = adv
            .publisher
            .Status()
            .map(|s| format!("{s:?}"))
            .unwrap_or_else(|_| "Unknown".into());
        let ok = start_res.is_ok();
        if let Err(e) = start_res {
            notes.push(e);
        }
        drop(adv);

        Ok(MiracastSinkTestResult {
            port_7236_available,
            publisher_started: ok,
            publisher_status,
            wfd_handle_opened: true,
            ie_enable_error: if ok { 0 } else { -1 },
            ie_sink_error: if ok { 0 } else { -1 },
            cleaned_up: true,
            notes,
        })
    }

    pub(super) fn run_receiver(
        rtsp_port: u16,
        stop: Arc<AtomicBool>,
        shared: Arc<Mutex<MiracastShared>>,
        init_tx: mpsc::SyncSender<Result<(), String>>,
    ) {
        let _apt = Apartment::new();
        let _wsa = WsaGuard::new();
        let port = if rtsp_port == 0 {
            DEFAULT_WFD_RTSP_PORT
        } else {
            rtsp_port
        };

        let init_and_run = (|| -> Result<(), String> {
            let guard = TcpListener::bind(("0.0.0.0", port)).map_err(|e| {
                format!("TCP port {port} busy (close Windows Wireless Display): {e}")
            })?;
            guard
                .set_nonblocking(true)
                .map_err(|e| format!("set_nonblocking: {e}"))?;

            let (req_tx, req_rx) = mpsc::sync_channel::<WiFiDirectConnectionRequest>(1);
            let session_active = Arc::new(AtomicBool::new(false));
            let mut adv = SinkAdvertisement::new()?;

            let active_cb = session_active.clone();
            let token = adv
                .listener
                .ConnectionRequested(&TypedEventHandler::new(
                    move |_: &Option<WiFiDirectConnectionListener>,
                          args: &Option<WiFiDirectConnectionRequestedEventArgs>| {
                        let Some(args) = args else {
                            return Ok(());
                        };
                        if active_cb
                            .compare_exchange(false, true, Ordering::AcqRel, Ordering::Acquire)
                            .is_err()
                        {
                            return Ok(());
                        }
                        match args.GetConnectionRequest() {
                            Ok(request) => {
                                // Move `request` alive to worker thread so WinRT never cancels mid-association.
                                if req_tx.try_send(request).is_err() {
                                    active_cb.store(false, Ordering::Release);
                                }
                            }
                            Err(_) => {
                                active_cb.store(false, Ordering::Release);
                            }
                        }
                        Ok(())
                    },
                ))
                .map_err(|e| format!("register ConnectionRequested: {e}"))?;

            adv.start(port)?;
            let _ = init_tx.send(Ok(()));

            while !stop.load(Ordering::Acquire) {
                let request = match req_rx.recv_timeout(Duration::from_millis(120)) {
                    Ok(r) => r,
                    Err(mpsc::RecvTimeoutError::Timeout) => continue,
                    Err(_) => break,
                };
                if let Ok(mut g) = shared.lock() {
                    g.state = MIRACAST_STATE_NEGOTIATING;
                }
                let _ = connect_phone(&stop, &shared, &guard, port, request);
                session_active.store(false, Ordering::Release);
                if let Ok(mut g) = shared.lock() {
                    g.uibc_tx = None;
                    if !stop.load(Ordering::Acquire) {
                        g.state = MIRACAST_STATE_LISTENING;
                    }
                }
            }

            let _ = adv.listener.RemoveConnectionRequested(token);
            Ok(())
        })();

        if let Err(e) = init_and_run {
            let _ = init_tx.send(Err(e));
        }
        if let Ok(mut g) = shared.lock() {
            g.uibc_tx = None;
            g.state = MIRACAST_STATE_STOPPED;
        }
    }

    fn connect_phone(
        stop: &Arc<AtomicBool>,
        shared: &Arc<Mutex<MiracastShared>>,
        guard: &TcpListener,
        rtsp_port: u16,
        request: WiFiDirectConnectionRequest,
    ) -> Result<(), String> {
        let info = request.DeviceInformation().map_err(|e| e.to_string())?;
        let params = WiFiDirectConnectionParameters::new().map_err(|e| e.to_string())?;
        params.SetGroupOwnerIntent(15).map_err(|e| e.to_string())?;
        let id = info.Id().map_err(|e| e.to_string())?;
        let op = WiFiDirectDevice::FromIdAsync2(&id, &params).map_err(|e| e.to_string())?;

        let deadline = Instant::now() + Duration::from_secs(20);
        while op.Status().map_err(|e| e.to_string())? == AsyncStatus::Started {
            if stop.load(Ordering::Acquire) || Instant::now() > deadline {
                let _ = op.Cancel();
                return Err("Wi-Fi Direct connection cancelled or timed out".into());
            }
            thread::sleep(Duration::from_millis(30));
        }
        let device = op
            .GetResults()
            .map_err(|e| format!("WiFiDirectDevice::GetResults: {e}"))?;

        let res = (|| -> Result<(), String> {
            let endpoints = device
                .GetConnectionEndpointPairs()
                .map_err(|e| e.to_string())?;
            let mut peer: Option<(Ipv4Addr, Ipv4Addr)> = None;
            for ep in endpoints {
                let local = ep
                    .LocalHostName()
                    .and_then(|h| h.CanonicalName())
                    .ok()
                    .and_then(|s| s.to_string().parse::<IpAddr>().ok());
                let remote = ep
                    .RemoteHostName()
                    .and_then(|h| h.CanonicalName())
                    .ok()
                    .and_then(|s| s.to_string().parse::<IpAddr>().ok());
                if let (Some(IpAddr::V4(l)), Some(IpAddr::V4(r))) = (local, remote) {
                    peer = Some((l, r));
                    break;
                }
            }
            let (local, remote) =
                peer.ok_or_else(|| "no IPv4 Wi-Fi Direct EndpointPair".to_string())?;

            // In Miracast WFD, Sink connects outbound to Source:7236 bound to `local`.
            let stream = match tcp_bound_connect(local, remote, rtsp_port, Duration::from_secs(5)) {
                Ok(s) => s,
                Err(_) => {
                    let dl = Instant::now() + Duration::from_secs(5);
                    loop {
                        if stop.load(Ordering::Acquire) || Instant::now() > dl {
                            return Err("phone did not establish RTSP control connection".into());
                        }
                        match guard.accept() {
                            Ok((s, addr)) if addr.ip() == IpAddr::V4(remote) => break s,
                            Ok(_) => {}
                            Err(e) if e.kind() == std::io::ErrorKind::WouldBlock => {
                                thread::sleep(Duration::from_millis(30));
                            }
                            Err(e) => return Err(e.to_string()),
                        }
                    }
                }
            };
            run_rtsp_session(stop, shared, stream, local, remote)
        })();

        // Explicitly close the WiFiDirectDevice only after RTSP/RTP teardown completes.
        let _ = device.Close();
        drop(request);
        res
    }

    fn sockaddr_v4(ip: Ipv4Addr, port: u16) -> SOCKADDR_IN {
        SOCKADDR_IN {
            sin_family: AF_INET,
            sin_port: port.to_be(),
            sin_addr: IN_ADDR {
                S_un: IN_ADDR_0 {
                    S_addr: u32::from_ne_bytes(ip.octets()),
                },
            },
            sin_zero: [0; 8],
        }
    }

    fn tcp_bound_connect(
        local: Ipv4Addr,
        remote: Ipv4Addr,
        port: u16,
        timeout: Duration,
    ) -> Result<TcpStream, String> {
        // SAFETY: Creating a standard IPv4 TCP WinSock2 socket and wrapping via FromRawSocket on success.
        unsafe {
            let sock: SOCKET = socket(i32::from(AF_INET.0), SOCK_STREAM, IPPROTO_TCP.0)
                .map_err(|e| format!("WinSock socket(TCP) failed: {e}"))?;
            let local_sa = sockaddr_v4(local, 0);
            if bind(
                sock,
                (&raw const local_sa).cast::<SOCKADDR>(),
                std::mem::size_of::<SOCKADDR_IN>() as i32,
            ) == SOCKET_ERROR
            {
                let _ = closesocket(sock);
                return Err("bind TCP to Wi-Fi Direct local IP failed".into());
            }
            let mut nonblock: u32 = 1;
            let _ = ioctlsocket(sock, FIONBIO, &mut nonblock);

            let remote_sa = sockaddr_v4(remote, port);
            let _ = connect(
                sock,
                (&raw const remote_sa).cast::<SOCKADDR>(),
                std::mem::size_of::<SOCKADDR_IN>() as i32,
            );

            let mut write_set = FD_SET {
                fd_count: 1,
                fd_array: [SOCKET(0); 64],
            };
            write_set.fd_array[0] = sock;
            let tv = TIMEVAL {
                tv_sec: timeout.as_secs() as i32,
                tv_usec: timeout.subsec_micros() as i32,
            };
            let ready = select(0, None, Some(&mut write_set), None, Some(&tv));
            if ready <= 0 {
                let _ = closesocket(sock);
                return Err("connect timeout to phone RTSP/UIBC port".into());
            }
            let mut blocking: u32 = 0;
            let _ = ioctlsocket(sock, FIONBIO, &mut blocking);

            let stream = TcpStream::from_raw_socket(sock.0 as u64);
            configure_tcp(&stream)?;
            Ok(stream)
        }
    }

    fn configure_tcp(stream: &TcpStream) -> Result<(), String> {
        stream.set_nonblocking(false).map_err(|e| e.to_string())?;
        stream.set_nodelay(true).map_err(|e| e.to_string())?;
        stream
            .set_read_timeout(Some(Duration::from_millis(120)))
            .map_err(|e| e.to_string())?;
        stream
            .set_write_timeout(Some(Duration::from_secs(2)))
            .map_err(|e| e.to_string())?;
        Ok(())
    }

    fn reserve_udp_pair(local: Ipv4Addr) -> Result<(UdpSocket, UdpSocket), String> {
        for _ in 0..24 {
            // SAFETY: Creating an IPv4 UDP socket with 4 MiB receive buffer before FromRawSocket ownership transfer.
            let first: UdpSocket = unsafe {
                let sock: SOCKET = socket(i32::from(AF_INET.0), SOCK_DGRAM, 0)
                    .map_err(|e| format!("WinSock socket(UDP) failed: {e}"))?;
                let rcv_buf: i32 = 4 * 1024 * 1024;
                let _ = setsockopt(sock, SOL_SOCKET, SO_RCVBUF, Some(&rcv_buf.to_ne_bytes()));
                let local_sa = sockaddr_v4(local, 0);
                if bind(
                    sock,
                    (&raw const local_sa).cast::<SOCKADDR>(),
                    std::mem::size_of::<SOCKADDR_IN>() as i32,
                ) == SOCKET_ERROR
                {
                    let _ = closesocket(sock);
                    return Err("bind UDP RTP failed".into());
                }
                UdpSocket::from_raw_socket(sock.0 as u64)
            };
            let port = first.local_addr().map_err(|e| e.to_string())?.port();
            if port % 2 != 0 {
                continue;
            }
            if let Ok(second) = UdpSocket::bind(SocketAddr::new(IpAddr::V4(local), port + 1)) {
                let _ = first.set_read_timeout(Some(Duration::from_millis(20)));
                let _ = second.set_nonblocking(true);
                return Ok((first, second));
            }
        }
        Err("could not reserve even/odd UDP RTP/RTCP port pair".into())
    }

    fn run_rtsp_session(
        stop: &Arc<AtomicBool>,
        shared: &Arc<Mutex<MiracastShared>>,
        mut stream: TcpStream,
        local: Ipv4Addr,
        remote: Ipv4Addr,
    ) -> Result<(), String> {
        configure_tcp(&stream)?;
        let (rtp, rtcp) = reserve_udp_pair(local)?;
        let rtp_port = rtp.local_addr().map_err(|e| e.to_string())?.port();
        let mut udp = Some((rtp, rtcp));
        let life = Arc::new(AtomicBool::new(true));
        let recover = Arc::new(AtomicBool::new(false));
        let mut video_thread: Option<thread::JoinHandle<()>> = None;
        let mut input_thread: Option<thread::JoinHandle<()>> = None;
        let mut input_shutdown: Option<TcpStream> = None;

        let mut mode: Option<VideoMode> = None;
        let mut sequence: u32 = 0;
        let mut setup_sequence: u32 = 0;
        let mut play_sequence: u32 = 0;
        let mut session_id = String::new();
        let mut url = format!("rtsp://{remote}/wfd1.0/streamid=0");
        let mut uibc_caps = String::new();
        let mut sent_options = false;
        let mut framer = RtspFramer::default();
        let mut buf = [0u8; 8192];
        let mut last_recover = Instant::now() - Duration::from_secs(2);

        let res = (|| -> Result<(), String> {
            while !stop.load(Ordering::Acquire) && life.load(Ordering::Acquire) {
                if recover.load(Ordering::Acquire)
                    && last_recover.elapsed() > Duration::from_secs(1)
                    && !session_id.is_empty()
                {
                    recover.store(false, Ordering::Release);
                    sequence += 1;
                    let msg = format_rtsp(
                        &format!("SET_PARAMETER {url} RTSP/1.0"),
                        sequence,
                        "wfd_idr_request\r\n",
                        &format!("Session: {session_id}\r\n"),
                    );
                    stream
                        .write_all(msg.as_bytes())
                        .map_err(|e| e.to_string())?;
                    last_recover = Instant::now();
                }

                match stream.read(&mut buf) {
                    Ok(0) => break,
                    Ok(n) => framer.push(&buf[..n])?,
                    Err(e)
                        if matches!(
                            e.kind(),
                            std::io::ErrorKind::WouldBlock | std::io::ErrorKind::TimedOut
                        ) =>
                    {
                        continue;
                    }
                    Err(e) => return Err(e.to_string()),
                }

                while let Some(msg) = framer.next_message()? {
                    let received = msg.sequence()?;
                    if msg.is_response() {
                        if !msg.start.starts_with("RTSP/1.0 200") {
                            return Err(format!("phone rejected RTSP request: {}", msg.start));
                        }
                        if received == setup_sequence {
                            session_id = msg
                                .headers
                                .get("session")
                                .ok_or_else(|| "missing Session header on SETUP reply".to_string())?
                                .split(';')
                                .next()
                                .unwrap_or("")
                                .to_owned();
                            sequence += 1;
                            play_sequence = sequence;
                            let play = format_rtsp(
                                &format!("PLAY {url} RTSP/1.0"),
                                sequence,
                                "",
                                &format!("Session: {session_id}\r\n"),
                            );
                            stream
                                .write_all(play.as_bytes())
                                .map_err(|e| e.to_string())?;
                        } else if received == play_sequence {
                            let (uibc_port, has_pointer, has_kb) =
                                parse_uibc_capability(&uibc_caps);
                            if uibc_port > 0 && has_pointer {
                                sequence += 1;
                                let en = format_rtsp(
                                    &format!("SET_PARAMETER {url} RTSP/1.0"),
                                    sequence,
                                    "wfd_uibc_setting: enable\r\n",
                                    &format!("Session: {session_id}\r\n"),
                                );
                                stream.write_all(en.as_bytes()).map_err(|e| e.to_string())?;
                                if let Ok(mut uibc_stream) = tcp_bound_connect(
                                    local,
                                    remote,
                                    uibc_port,
                                    Duration::from_secs(4),
                                ) {
                                    if has_kb {
                                        let _ = uibc_stream.write_all(&uibc_keyboard_descriptor());
                                    }
                                    input_shutdown = uibc_stream.try_clone().ok();
                                    let (tx, rx) = mpsc::sync_channel::<Vec<u8>>(512);
                                    if let Ok(mut g) = shared.lock() {
                                        g.uibc_tx = Some(tx);
                                    }
                                    let life_in = life.clone();
                                    let stop_in = stop.clone();
                                    input_thread = Some(thread::spawn(move || {
                                        while life_in.load(Ordering::Acquire)
                                            && !stop_in.load(Ordering::Acquire)
                                        {
                                            match rx.recv_timeout(Duration::from_millis(100)) {
                                                Ok(pkt) => {
                                                    if uibc_stream.write_all(&pkt).is_err() {
                                                        break;
                                                    }
                                                }
                                                Err(mpsc::RecvTimeoutError::Timeout) => {}
                                                Err(_) => break,
                                            }
                                        }
                                        let _ = uibc_stream.shutdown(Shutdown::Both);
                                    }));
                                }
                            }
                            recover.store(true, Ordering::Release);
                        }
                        continue;
                    }

                    match msg.method() {
                        "OPTIONS" => {
                            let ok = format_rtsp(
                                "RTSP/1.0 200 OK",
                                received,
                                "",
                                "Public: org.wfa.wfd1.0, OPTIONS, GET_PARAMETER, SET_PARAMETER, SETUP, PLAY, TEARDOWN\r\n",
                            );
                            stream.write_all(ok.as_bytes()).map_err(|e| e.to_string())?;
                            if !sent_options {
                                sent_options = true;
                                sequence += 1;
                                let m2 = format_rtsp(
                                    "OPTIONS * RTSP/1.0",
                                    sequence,
                                    "",
                                    "Require: org.wfa.wfd1.0\r\n",
                                );
                                stream.write_all(m2.as_bytes()).map_err(|e| e.to_string())?;
                            }
                        }
                        "GET_PARAMETER" => {
                            let body = if msg.body.is_empty() {
                                String::new()
                            } else {
                                format_wfd_capabilities(&msg.body, rtp_port)
                            };
                            let reply = format_rtsp("RTSP/1.0 200 OK", received, &body, "");
                            stream
                                .write_all(reply.as_bytes())
                                .map_err(|e| e.to_string())?;
                        }
                        "SET_PARAMETER" => {
                            let params = parse_wfd_parameters(&msg.body);
                            if let Some(vf) = params.get("wfd_video_formats") {
                                let selected = VideoMode::from_wfd(vf)?;
                                mode = Some(selected);
                                if let Some((rtp_s, rtcp_s)) = udp.take() {
                                    video_thread = Some(spawn_video_worker(
                                        stop.clone(),
                                        shared.clone(),
                                        rtp_s,
                                        rtcp_s,
                                        IpAddr::V4(remote),
                                        selected,
                                        life.clone(),
                                        recover.clone(),
                                    ));
                                }
                            }
                            if let Some(u) = params.get("wfd_uibc_capability") {
                                uibc_caps.clone_from(u);
                            }
                            if let Some(p_url) = params.get("wfd_presentation_url")
                                && let Some(first) = p_url.split_whitespace().next()
                            {
                                url = first.to_owned();
                            }
                            let ok = format_rtsp("RTSP/1.0 200 OK", received, "", "");
                            stream.write_all(ok.as_bytes()).map_err(|e| e.to_string())?;

                            match params.get("wfd_trigger_method").map(String::as_str) {
                                Some("SETUP") => {
                                    let _ = mode;
                                    sequence += 1;
                                    setup_sequence = sequence;
                                    let setup = format_rtsp(
                                        &format!("SETUP {url} RTSP/1.0"),
                                        sequence,
                                        "",
                                        &format!(
                                            "Transport: RTP/AVP/UDP;unicast;client_port={rtp_port}-{}\r\n",
                                            rtp_port + 1
                                        ),
                                    );
                                    stream
                                        .write_all(setup.as_bytes())
                                        .map_err(|e| e.to_string())?;
                                }
                                Some("TEARDOWN") => return Ok(()),
                                _ => {}
                            }
                        }
                        "TEARDOWN" => {
                            let ok = format_rtsp("RTSP/1.0 200 OK", received, "", "");
                            let _ = stream.write_all(ok.as_bytes());
                            return Ok(());
                        }
                        _ => {
                            let err =
                                format_rtsp("RTSP/1.0 405 Method Not Allowed", received, "", "");
                            stream
                                .write_all(err.as_bytes())
                                .map_err(|e| e.to_string())?;
                        }
                    }
                }
            }
            Ok(())
        })();

        life.store(false, Ordering::Release);
        if let Ok(mut g) = shared.lock() {
            g.uibc_tx = None;
        }
        if let Some(inp) = input_shutdown {
            let _ = inp.shutdown(Shutdown::Both);
        }
        let _ = stream.shutdown(Shutdown::Both);
        if let Some(w) = input_thread {
            let _ = w.join();
        }
        if let Some(w) = video_thread {
            let _ = w.join();
        }
        res
    }

    #[allow(clippy::too_many_arguments)]
    fn spawn_video_worker(
        stop: Arc<AtomicBool>,
        shared: Arc<Mutex<MiracastShared>>,
        rtp: UdpSocket,
        rtcp: UdpSocket,
        remote: IpAddr,
        mode: VideoMode,
        life: Arc<AtomicBool>,
        recover: Arc<AtomicBool>,
    ) -> thread::JoinHandle<()> {
        thread::spawn(move || {
            let _apt = Apartment::new();
            let Ok(mut decoder) = crate::mf_video::MfVideoDecoder::open(
                crate::scrcpy::CODEC_H264,
                mode.width,
                mode.height,
            ) else {
                life.store(false, Ordering::Release);
                return;
            };
            let mut demux = TransportStream::default();
            let mut reorder = RtpReorder::default();
            let mut packet = vec![0u8; 65_536];
            let mut nv12_scratch =
                vec![0u8; (mode.width as usize) * ((mode.height as usize) + 16) * 3 / 2];
            let started = Instant::now();

            while life.load(Ordering::Acquire) && !stop.load(Ordering::Acquire) {
                let now_ms = started.elapsed().as_millis() as u64;
                let batch = match rtp.recv_from(&mut packet) {
                    Ok((len, peer)) if peer.ip() == remote => {
                        let Some((ssrc, seq, payload)) = rtp_payload(&packet[..len]) else {
                            continue;
                        };
                        reorder.push(ssrc, seq, payload, now_ms)
                    }
                    Ok(_) => continue,
                    Err(e)
                        if matches!(
                            e.kind(),
                            std::io::ErrorKind::WouldBlock | std::io::ErrorKind::TimedOut
                        ) =>
                    {
                        reorder.flush(now_ms)
                    }
                    Err(_) => break,
                };

                if batch.1 > 0 {
                    demux.discard();
                    recover.store(true, Ordering::Release);
                }
                for chunk in batch.0 {
                    if let Ok(frames) = demux.push(&chunk) {
                        for ef in frames {
                            let flags = u32::from(ef.discontinuity);
                            if decoder.push(&ef.bytes, now_ms * 1000, flags).is_ok() {
                                let mut mf_meta = crate::mf_video::FrameMeta::default();
                                let rc = decoder
                                    .pull_nv12(&mut nv12_scratch, &mut mf_meta)
                                    .unwrap_or(2);
                                if rc == 1 && (mf_meta.total_len as usize) > nv12_scratch.len() {
                                    nv12_scratch.resize(mf_meta.total_len as usize, 0);
                                    let _ = decoder.pull_nv12(&mut nv12_scratch, &mut mf_meta);
                                }
                                if mf_meta.total_len > 0
                                    && (mf_meta.total_len as usize) <= nv12_scratch.len()
                                    && let Ok(mut g) = shared.lock()
                                {
                                    g.state = MIRACAST_STATE_STREAMING;
                                    let total = mf_meta.total_len as usize;
                                    g.latest_frame = Some((
                                        crate::FrameMeta {
                                            width: mf_meta.width,
                                            height: mf_meta.height,
                                            y_stride: mf_meta.y_stride,
                                            uv_stride: mf_meta.uv_stride,
                                            uv_offset: mf_meta.uv_offset,
                                            total_len: mf_meta.total_len,
                                            pts_us: mf_meta.pts_us,
                                            decode_us: mf_meta.decode_us,
                                            hardware: mf_meta.hardware,
                                            surface_changed: mf_meta.surface_changed,
                                            _reserved: mf_meta._reserved,
                                        },
                                        nv12_scratch[..total].to_vec(),
                                    ));
                                }
                            }
                        }
                    }
                }
                let mut ctrl = [0u8; 1500];
                while rtcp.recv_from(&mut ctrl).is_ok() {}
            }
            life.store(false, Ordering::Release);
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ts_packet(pid: u16, start: bool, cc: u8, data: &[u8]) -> [u8; 188] {
        let mut p = [0xff; 188];
        p[0] = 0x47;
        p[1] = ((pid >> 8) as u8 & 31) | if start { 0x40 } else { 0 };
        p[2] = pid as u8;
        let pad = 184 - data.len();
        if pad > 0 {
            p[3] = 0x30 | cc;
            p[4] = (pad - 1) as u8;
            p[5..4 + pad].fill(0);
            p[4 + pad..].copy_from_slice(data);
        } else {
            p[3] = 0x10 | cc;
            p[4..].copy_from_slice(data);
        }
        p
    }

    #[test]
    fn rtsp_wfd_mode_capabilities_uibc_and_ts_demux_roundtrip() {
        let m30 = VideoMode::from_wfd("40 00 02 04 00000080 00000000 00000000").unwrap();
        assert_eq!(
            m30,
            VideoMode {
                width: 1920,
                height: 1080,
                fps: 30
            }
        );
        let m60 = VideoMode::from_wfd("00 00 02 10 00000100 00000000 00000000").unwrap();
        assert_eq!(
            m60,
            VideoMode {
                width: 1920,
                height: 1080,
                fps: 60
            }
        );

        let caps_body =
            format_wfd_capabilities("wfd_video_formats\r\nwfd_client_rtp_ports\r\n", 19000);
        assert!(caps_body.contains("RTP/AVP/UDP;unicast 19000 0 mode=play"));
        let uibc = parse_uibc_capability(
            "input_category_list=GENERIC, HIDC;generic_cap_list=Mouse, SingleTouch;hidc_cap_list=Keyboard/USB;port=34137",
        );
        assert_eq!(uibc, (34137, true, true));
        assert_eq!(
            &uibc_keyboard_descriptor()[..9],
            &[0, 1, 0, 74, 1, 0, 1, 0, 65]
        );
        assert_eq!(
            uibc_pointer(0, 960, 540),
            [0, 0, 0, 14, 0, 0, 7, 1, 0, 3, 192, 2, 28, 0]
        );

        let msg_txt = format_rtsp(
            "GET_PARAMETER rtsp://localhost/wfd1.0 RTSP/1.0",
            3,
            "wfd_video_formats\r\n",
            "",
        );
        let mut framer = RtspFramer::default();
        for c in msg_txt.as_bytes().chunks(7) {
            framer.push(c).unwrap();
        }
        let msg = framer.next_message().unwrap().unwrap();
        assert_eq!(msg.sequence().unwrap(), 3);
        assert_eq!(msg.method(), "GET_PARAMETER");

        let mut ts = TransportStream {
            video_pid: Some(0x1011),
            ..Default::default()
        };
        let pes = [0, 0, 1, 0xe0, 0, 9, 0x80, 0, 0, 0, 0, 1, 0x65, 0x88, 0x80];
        let frames = ts.push(&ts_packet(0x1011, true, 0, &pes)).unwrap();
        assert_eq!(frames.len(), 1);
        assert_eq!(frames[0].bytes, vec![0, 0, 1, 0x65, 0x88, 0x80]);
    }
}
