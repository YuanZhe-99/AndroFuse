//! Wire protocol framing and control-message serialization for `scrcpy-server v5.0.1`.

use serde::{Deserialize, Serialize};

/// Codec ID for H.264 video (`"h264"`).
pub const CODEC_H264: u32 = 0x6832_3634;
/// Codec ID for H.265 / HEVC video (`"h265"`).
pub const CODEC_H265: u32 = 0x6832_3635;
/// Codec ID for AV1 video (`0x00617631`).
pub const CODEC_AV1: u32 = 0x0061_7631;
/// Codec ID for Opus audio (`"opus"`).
pub const CODEC_OPUS: u32 = 0x6f70_7573;
/// Codec ID for AAC audio (`0x00616163`).
pub const CODEC_AAC: u32 = 0x0061_6163;
/// Codec ID for FLAC audio (`"flac"`).
pub const CODEC_FLAC: u32 = 0x666c_6163;
/// Codec ID for raw PCM audio (`0x00726177`).
pub const CODEC_RAW: u32 = 0x0072_6177;

/// Length in bytes of the NUL-padded UTF-8 device name header sent on the first scrcpy socket.
pub const DEVICE_NAME_LEN: usize = 64;
/// Length in bytes of a scrcpy session or media frame header.
pub const PACKET_HEADER_LEN: usize = 12;
/// Maximum payload accepted on a single media packet (32 MiB sanity bound).
pub const MAX_MEDIA_PACKET_SIZE: usize = 32 * 1024 * 1024;

/// Video stream handshake metadata parsed before frames arrive.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct StreamHeader {
    pub device_name: String,
    pub codec_id: u32,
}

/// Decoded item emitted by [`VideoStreamParser`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum VideoEvent {
    Session {
        width: u32,
        height: u32,
        client_resized: bool,
    },
    Media {
        pts_us: u64,
        is_config: bool,
        is_keyframe: bool,
        payload: Vec<u8>,
    },
}

/// Decodes `device_name` + `codec_id` from a scrcpy video socket handshake buffer.
///
/// Notes: When `has_dummy_byte` is true (ADB `tunnel_forward=true`), the leading dummy byte
/// `0x00` is consumed prior to the 64-byte device name and 4-byte big-endian codec ID.
pub fn parse_video_handshake(buf: &[u8], has_dummy_byte: bool) -> Option<(StreamHeader, usize)> {
    let offset = usize::from(has_dummy_byte);
    let total = offset + DEVICE_NAME_LEN + 4;
    if buf.len() < total {
        return None;
    }
    let name_raw = &buf[offset..offset + DEVICE_NAME_LEN];
    let nul_pos = name_raw
        .iter()
        .position(|&b| b == 0)
        .unwrap_or(DEVICE_NAME_LEN);
    let device_name = String::from_utf8_lossy(&name_raw[..nul_pos]).into_owned();
    let codec_id = u32::from_be_bytes(
        buf[offset + DEVICE_NAME_LEN..total]
            .try_into()
            .expect("fixed 4-byte slice"),
    );
    Some((
        StreamHeader {
            device_name,
            codec_id,
        },
        total,
    ))
}

/// Parses a 4-byte big-endian codec ID from a secondary scrcpy socket (audio).
pub fn parse_codec_id(buf: &[u8]) -> Option<u32> {
    if buf.len() < 4 {
        return None;
    }
    Some(u32::from_be_bytes(buf[..4].try_into().expect("4 bytes")))
}

/// Incremental framer for `scrcpy v5.0.1` session and media video packets.
#[derive(Debug, Default)]
pub struct VideoStreamParser {
    buf: Vec<u8>,
    last_config: Vec<u8>,
}

impl VideoStreamParser {
    /// Appends raw socket bytes into the parser buffer.
    pub fn push(&mut self, data: &[u8]) {
        self.buf.extend_from_slice(data);
    }

    /// Returns the most recently observed codec configuration (SPS/PPS/VPS) packet, if any.
    pub fn last_config(&self) -> &[u8] {
        &self.last_config
    }

    /// Combines cached codec configuration with a keyframe payload when the IDR lacks inline SPS/PPS.
    pub fn with_config_prefix(&self, payload: &[u8], is_keyframe: bool, out: &mut Vec<u8>) {
        out.clear();
        if is_keyframe && !self.last_config.is_empty() && !payload.starts_with(&self.last_config) {
            out.extend_from_slice(&self.last_config);
        }
        out.extend_from_slice(payload);
    }

    /// Attempts to parse the next session or media packet from buffered bytes.
    ///
    /// Returns `Ok(None)` when more socket bytes are required, or `Err` on malformed sizes.
    pub fn next_event(&mut self) -> Result<Option<VideoEvent>, &'static str> {
        if self.buf.len() < PACKET_HEADER_LEN {
            return Ok(None);
        }
        let header: [u8; PACKET_HEADER_LEN] = self.buf[..PACKET_HEADER_LEN]
            .try_into()
            .expect("header length checked");

        // Bit 7 of byte 0 distinguishes session packets (width/height) from media packets.
        if header[0] & 0x80 != 0 {
            let client_resized = (header[3] & 0x01) != 0;
            let width = u32::from_be_bytes(header[4..8].try_into().expect("4 bytes"));
            let height = u32::from_be_bytes(header[8..12].try_into().expect("4 bytes"));
            self.buf.drain(..PACKET_HEADER_LEN);
            return Ok(Some(VideoEvent::Session {
                width,
                height,
                client_resized,
            }));
        }

        let pts_flags = u64::from_be_bytes(header[0..8].try_into().expect("8 bytes"));
        let is_config = ((pts_flags >> 62) & 1) != 0;
        let is_keyframe = ((pts_flags >> 61) & 1) != 0;
        let pts_us = pts_flags & 0x1fff_ffff_ffff_ffff;
        let size = u32::from_be_bytes(header[8..12].try_into().expect("4 bytes")) as usize;
        if size > MAX_MEDIA_PACKET_SIZE {
            return Err("scrcpy video packet exceeded 32 MiB bound");
        }
        let total = PACKET_HEADER_LEN + size;
        if self.buf.len() < total {
            return Ok(None);
        }
        let payload = self.buf[PACKET_HEADER_LEN..total].to_vec();
        self.buf.drain(..total);
        if is_config {
            self.last_config.clone_from(&payload);
        }
        Ok(Some(VideoEvent::Media {
            pts_us,
            is_config,
            is_keyframe,
            payload,
        }))
    }
}

/// Control messages serialized from Windows desktop to `scrcpy-server v5.0.1`.
#[derive(Debug, Clone, PartialEq)]
pub enum ControlMsg {
    InjectKeycode {
        action: u8,
        keycode: u32,
        repeat: u32,
        meta_state: u32,
    },
    InjectText {
        text: String,
    },
    InjectTouch {
        action: u8,
        pointer_id: u64,
        x: i32,
        y: i32,
        screen_w: u16,
        screen_h: u16,
        pressure_u16: u16,
        action_button: u32,
        buttons: u32,
    },
    InjectScroll {
        x: i32,
        y: i32,
        screen_w: u16,
        screen_h: u16,
        h_scroll_i16: i16,
        v_scroll_i16: i16,
        buttons: u32,
    },
    BackOrScreenOn {
        action: u8,
    },
    GetClipboard {
        copy_key: u8,
    },
    SetClipboard {
        sequence: u64,
        paste: bool,
        text: String,
    },
    SetDisplayPower {
        on: bool,
    },
    RotateDevice,
    UhidCreate {
        id: u16,
        vendor_id: u16,
        product_id: u16,
        name: String,
        report_desc: Vec<u8>,
    },
    UhidInput {
        id: u16,
        report: Vec<u8>,
    },
    UhidDestroy {
        id: u16,
    },
    StartApp {
        name: String,
    },
    ResetVideo,
    ResizeDisplay {
        width: u16,
        height: u16,
    },
}

impl ControlMsg {
    /// Encodes this control message according to `scrcpy v5.0.1` `ControlMessageReader.java`.
    pub fn encode(&self) -> Vec<u8> {
        let mut out = Vec::new();
        match self {
            Self::InjectKeycode {
                action,
                keycode,
                repeat,
                meta_state,
            } => {
                out.push(0);
                out.push(*action);
                out.extend_from_slice(&keycode.to_be_bytes());
                out.extend_from_slice(&repeat.to_be_bytes());
                out.extend_from_slice(&meta_state.to_be_bytes());
            }
            Self::InjectText { text } => {
                let bytes = text.as_bytes();
                let n = bytes.len().min(300);
                out.push(1);
                out.extend_from_slice(&(n as u32).to_be_bytes());
                out.extend_from_slice(&bytes[..n]);
            }
            Self::InjectTouch {
                action,
                pointer_id,
                x,
                y,
                screen_w,
                screen_h,
                pressure_u16,
                action_button,
                buttons,
            } => {
                out.push(2);
                out.push(*action);
                out.extend_from_slice(&pointer_id.to_be_bytes());
                out.extend_from_slice(&x.to_be_bytes());
                out.extend_from_slice(&y.to_be_bytes());
                out.extend_from_slice(&screen_w.to_be_bytes());
                out.extend_from_slice(&screen_h.to_be_bytes());
                out.extend_from_slice(&pressure_u16.to_be_bytes());
                out.extend_from_slice(&action_button.to_be_bytes());
                out.extend_from_slice(&buttons.to_be_bytes());
            }
            Self::InjectScroll {
                x,
                y,
                screen_w,
                screen_h,
                h_scroll_i16,
                v_scroll_i16,
                buttons,
            } => {
                out.push(3);
                out.extend_from_slice(&x.to_be_bytes());
                out.extend_from_slice(&y.to_be_bytes());
                out.extend_from_slice(&screen_w.to_be_bytes());
                out.extend_from_slice(&screen_h.to_be_bytes());
                out.extend_from_slice(&h_scroll_i16.to_be_bytes());
                out.extend_from_slice(&v_scroll_i16.to_be_bytes());
                out.extend_from_slice(&buttons.to_be_bytes());
            }
            Self::BackOrScreenOn { action } => {
                out.push(4);
                out.push(*action);
            }
            Self::GetClipboard { copy_key } => {
                out.push(8);
                out.push(*copy_key);
            }
            Self::SetClipboard {
                sequence,
                paste,
                text,
            } => {
                let bytes = text.as_bytes();
                out.push(9);
                out.extend_from_slice(&sequence.to_be_bytes());
                out.push(u8::from(*paste));
                out.extend_from_slice(&(bytes.len() as u32).to_be_bytes());
                out.extend_from_slice(bytes);
            }
            Self::SetDisplayPower { on } => {
                out.push(10);
                out.push(u8::from(*on));
            }
            Self::RotateDevice => out.push(11),
            Self::UhidCreate {
                id,
                vendor_id,
                product_id,
                name,
                report_desc,
            } => {
                let name_bytes = name.as_bytes();
                let name_len = name_bytes.len().min(127);
                out.push(12);
                out.extend_from_slice(&id.to_be_bytes());
                out.extend_from_slice(&vendor_id.to_be_bytes());
                out.extend_from_slice(&product_id.to_be_bytes());
                out.push(name_len as u8);
                out.extend_from_slice(&name_bytes[..name_len]);
                out.extend_from_slice(&(report_desc.len() as u16).to_be_bytes());
                out.extend_from_slice(report_desc);
            }
            Self::UhidInput { id, report } => {
                out.push(13);
                out.extend_from_slice(&id.to_be_bytes());
                out.extend_from_slice(&(report.len() as u16).to_be_bytes());
                out.extend_from_slice(report);
            }
            Self::UhidDestroy { id } => {
                out.push(14);
                out.extend_from_slice(&id.to_be_bytes());
            }
            Self::StartApp { name } => {
                let bytes = name.as_bytes();
                let len = bytes.len().min(255);
                out.push(16);
                out.push(len as u8);
                out.extend_from_slice(&bytes[..len]);
            }
            Self::ResetVideo => out.push(17),
            Self::ResizeDisplay { width, height } => {
                out.push(21);
                out.extend_from_slice(&width.to_be_bytes());
                out.extend_from_slice(&height.to_be_bytes());
            }
        }
        out
    }
}

/// Device-to-client messages emitted on the scrcpy control socket.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum DeviceMsg {
    Clipboard(String),
    AckClipboard(u64),
    UhidOutput { id: u16, data: Vec<u8> },
}

/// Parses one [`DeviceMsg`] from `buf`, returning the message and consumed byte count.
pub fn parse_device_msg(buf: &[u8]) -> Result<Option<(DeviceMsg, usize)>, &'static str> {
    if buf.is_empty() {
        return Ok(None);
    }
    match buf[0] {
        0 => {
            if buf.len() < 5 {
                return Ok(None);
            }
            let len = u32::from_be_bytes(buf[1..5].try_into().expect("4 bytes")) as usize;
            if len > 256 * 1024 {
                return Err("clipboard message exceeds 256 KiB");
            }
            if buf.len() < 5 + len {
                return Ok(None);
            }
            let text = String::from_utf8_lossy(&buf[5..5 + len]).into_owned();
            Ok(Some((DeviceMsg::Clipboard(text), 5 + len)))
        }
        1 => {
            if buf.len() < 9 {
                return Ok(None);
            }
            let seq = u64::from_be_bytes(buf[1..9].try_into().expect("8 bytes"));
            Ok(Some((DeviceMsg::AckClipboard(seq), 9)))
        }
        2 => {
            if buf.len() < 5 {
                return Ok(None);
            }
            let id = u16::from_be_bytes(buf[1..3].try_into().expect("2 bytes"));
            let len = u16::from_be_bytes(buf[3..5].try_into().expect("2 bytes")) as usize;
            if buf.len() < 5 + len {
                return Ok(None);
            }
            Ok(Some((
                DeviceMsg::UhidOutput {
                    id,
                    data: buf[5..5 + len].to_vec(),
                },
                5 + len,
            )))
        }
        _ => Err("unknown scrcpy device message type"),
    }
}

/// Standard USB HID descriptor for a 5-button relative mouse + vertical/horizontal wheel.
pub const UHID_MOUSE_REPORT_DESC: &[u8] = &[
    0x05, 0x01, // Usage Page (Generic Desktop)
    0x09, 0x02, // Usage (Mouse)
    0xA1, 0x01, // Collection (Application)
    0x09, 0x01, //   Usage (Pointer)
    0xA1, 0x00, //   Collection (Physical)
    0x05, 0x09, //     Usage Page (Buttons)
    0x19, 0x01, //     Usage Minimum (Button 1)
    0x29, 0x05, //     Usage Maximum (Button 5)
    0x15, 0x00, //     Logical Minimum (0)
    0x25, 0x01, //     Logical Maximum (1)
    0x95, 0x05, //     Report Count (5)
    0x75, 0x01, //     Report Size (1)
    0x81, 0x02, //     Input (Data, Variable, Absolute)
    0x95, 0x01, //     Report Count (1)
    0x75, 0x03, //     Report Size (3)
    0x81, 0x01, //     Input (Constant) - 3-bit padding
    0x05, 0x01, //     Usage Page (Generic Desktop)
    0x09, 0x30, //     Usage (X)
    0x09, 0x31, //     Usage (Y)
    0x09, 0x38, //     Usage (Wheel)
    0x15, 0x81, //     Logical Minimum (-127)
    0x25, 0x7F, //     Logical Maximum (127)
    0x75, 0x08, //     Report Size (8)
    0x95, 0x03, //     Report Count (3)
    0x81, 0x06, //     Input (Data, Variable, Relative)
    0xC0, //         End Collection
    0xC0, //       End Collection
];

/// Builds a 4-byte HID mouse input report matching [`UHID_MOUSE_REPORT_DESC`].
pub fn uhid_mouse_report(buttons: u8, dx: i8, dy: i8, wheel: i8) -> [u8; 4] {
    [buttons & 0x1F, dx as u8, dy as u8, wheel as u8]
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn handshake_and_fragmented_session_media_packets() {
        let mut hs = Vec::with_capacity(69);
        hs.push(0); // tunnel_forward dummy byte
        let mut name = [0u8; 64];
        name[..10].copy_from_slice(b"GalaxyFold");
        hs.extend_from_slice(&name);
        hs.extend_from_slice(&CODEC_H264.to_be_bytes());
        let (hdr, consumed) = parse_video_handshake(&hs, true).unwrap();
        assert_eq!(consumed, 69);
        assert_eq!(hdr.device_name, "GalaxyFold");
        assert_eq!(hdr.codec_id, CODEC_H264);

        let mut parser = VideoStreamParser::default();
        // Session packet: 1440x900 with client_resized bit (byte3 bit 0)
        let mut pkt = Vec::new();
        pkt.extend_from_slice(&[0x80, 0, 0, 1]);
        pkt.extend_from_slice(&1440u32.to_be_bytes());
        pkt.extend_from_slice(&900u32.to_be_bytes());
        // Config packet: bit 62 set, size = 4
        pkt.extend_from_slice(&(1u64 << 62).to_be_bytes());
        pkt.extend_from_slice(&4u32.to_be_bytes());
        pkt.extend_from_slice(&[0x00, 0x00, 0x00, 0x01]);
        // Keyframe packet: bit 61 set + pts=1234, size = 3
        pkt.extend_from_slice(&((1u64 << 61) | 1234).to_be_bytes());
        pkt.extend_from_slice(&3u32.to_be_bytes());
        pkt.extend_from_slice(&[0x65, 0x88, 0x80]);

        for chunk in pkt.chunks(5) {
            parser.push(chunk);
        }
        assert_eq!(
            parser.next_event().unwrap(),
            Some(VideoEvent::Session {
                width: 1440,
                height: 900,
                client_resized: true,
            })
        );
        assert_eq!(
            parser.next_event().unwrap(),
            Some(VideoEvent::Media {
                pts_us: 0,
                is_config: true,
                is_keyframe: false,
                payload: vec![0, 0, 0, 1],
            })
        );
        let media = parser.next_event().unwrap().unwrap();
        if let VideoEvent::Media {
            pts_us,
            is_keyframe,
            payload,
            ..
        } = media
        {
            assert_eq!(pts_us, 1234);
            assert!(is_keyframe);
            let mut merged = Vec::new();
            parser.with_config_prefix(&payload, is_keyframe, &mut merged);
            assert_eq!(merged, vec![0, 0, 0, 1, 0x65, 0x88, 0x80]);
        } else {
            panic!("expected media packet");
        }
        assert_eq!(parser.next_event().unwrap(), None);
    }

    #[test]
    fn control_and_device_messages_match_scrcpy_v5() {
        let start = ControlMsg::StartApp {
            name: "+com.sec.android.app.clockpackage".to_string(),
        }
        .encode();
        assert_eq!(start[0], 16);
        assert_eq!(start[1], 33);
        assert_eq!(&start[2..], b"+com.sec.android.app.clockpackage");

        let resize = ControlMsg::ResizeDisplay {
            width: 1440,
            height: 900,
        }
        .encode();
        assert_eq!(resize, vec![21, 0x05, 0xA0, 0x03, 0x84]);

        let uhid = ControlMsg::UhidCreate {
            id: 1,
            vendor_id: 0x18D1,
            product_id: 0xAF01,
            name: "mouse".to_string(),
            report_desc: UHID_MOUSE_REPORT_DESC.to_vec(),
        }
        .encode();
        assert_eq!(uhid[0], 12);
        assert_eq!(&uhid[1..3], &1u16.to_be_bytes());
        assert_eq!(uhid[7], 5);

        let mut dev = vec![0, 0, 0, 0, 5];
        dev.extend_from_slice(b"hello");
        assert_eq!(
            parse_device_msg(&dev).unwrap(),
            Some((DeviceMsg::Clipboard("hello".to_string()), 10))
        );
        assert_eq!(
            uhid_mouse_report(1, -2, 3, -1),
            [1, (-2i8) as u8, 3, (-1i8) as u8]
        );
    }
}
