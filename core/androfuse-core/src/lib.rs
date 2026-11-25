//! C-ABI core library for AndroFuse shared between the Windows desktop runtime
//! and the Android controller/companion runtime.

pub mod diag;
pub mod mesh;
#[cfg(windows)]
pub mod mf_video;
pub mod miracast;
pub mod scrcpy;
#[cfg(windows)]
pub mod wasapi_audio;

use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::panic::{AssertUnwindSafe, catch_unwind};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex, OnceLock};

/// C ABI version spoken between Go (`internal/core`) and `androfuse-core`.
pub const ABI_VERSION: u32 = 6;

static STATE: OnceLock<Mutex<Option<CoreState>>> = OnceLock::new();
static NEXT_DECODER_ID: AtomicU64 = AtomicU64::new(1);

#[cfg(windows)]
static DECODERS: OnceLock<Mutex<HashMap<u64, Mutex<mf_video::MfVideoDecoder>>>> = OnceLock::new();
#[cfg(not(windows))]
static DECODERS: OnceLock<Mutex<HashMap<u64, ()>>> = OnceLock::new();

#[cfg(windows)]
static MIRACAST_SESSIONS: OnceLock<Mutex<HashMap<u64, Mutex<miracast::MiracastReceiver>>>> =
    OnceLock::new();
#[cfg(not(windows))]
static MIRACAST_SESSIONS: OnceLock<Mutex<HashMap<u64, ()>>> = OnceLock::new();

#[cfg(windows)]
fn miracast_map() -> &'static Mutex<HashMap<u64, Mutex<miracast::MiracastReceiver>>> {
    MIRACAST_SESSIONS.get_or_init(|| Mutex::new(HashMap::new()))
}

#[cfg(not(windows))]
fn miracast_map() -> &'static Mutex<HashMap<u64, ()>> {
    MIRACAST_SESSIONS.get_or_init(|| Mutex::new(HashMap::new()))
}

#[cfg(windows)]
static AUDIO_PLAYERS: OnceLock<Mutex<HashMap<u64, Arc<wasapi_audio::WasapiAudioPlayer>>>> =
    OnceLock::new();
#[cfg(not(windows))]
static AUDIO_PLAYERS: OnceLock<Mutex<HashMap<u64, ()>>> = OnceLock::new();

#[cfg(windows)]
fn audio_map() -> &'static Mutex<HashMap<u64, Arc<wasapi_audio::WasapiAudioPlayer>>> {
    AUDIO_PLAYERS.get_or_init(|| Mutex::new(HashMap::new()))
}

#[cfg(not(windows))]
fn audio_map() -> &'static Mutex<HashMap<u64, ()>> {
    AUDIO_PLAYERS.get_or_init(|| Mutex::new(HashMap::new()))
}

#[cfg(windows)]
static MIC_CAPTURERS: OnceLock<Mutex<HashMap<u64, Arc<wasapi_audio::WasapiMicCapturer>>>> =
    OnceLock::new();
#[cfg(not(windows))]
static MIC_CAPTURERS: OnceLock<Mutex<HashMap<u64, ()>>> = OnceLock::new();

#[cfg(windows)]
fn mic_map() -> &'static Mutex<HashMap<u64, Arc<wasapi_audio::WasapiMicCapturer>>> {
    MIC_CAPTURERS.get_or_init(|| Mutex::new(HashMap::new()))
}

#[cfg(not(windows))]
fn mic_map() -> &'static Mutex<HashMap<u64, ()>> {
    MIC_CAPTURERS.get_or_init(|| Mutex::new(HashMap::new()))
}

#[derive(Debug, Clone)]
struct CoreState {
    version: String,
    state_dir: String,
    profile: String,
}

#[derive(Debug, Deserialize)]
struct StartConfig {
    version: String,
    state_dir: String,
    #[serde(default)]
    profile: String,
}

#[derive(Debug, Deserialize)]
struct CallRequest {
    method: String,
    #[serde(default)]
    params: serde_json::Value,
}

#[derive(Debug, Serialize)]
struct StatusResult {
    version: String,
    abi: u32,
    state_dir: String,
    profile: String,
    os: &'static str,
    arch: &'static str,
}

/// Cross-platform C-ABI NV12 frame metadata populated by `androfuse_decoder_pull_nv12`.
#[repr(C)]
#[derive(Debug, Clone, Copy, Default)]
pub struct FrameMeta {
    pub width: u32,
    pub height: u32,
    pub y_stride: u32,
    pub uv_stride: u32,
    pub uv_offset: u32,
    pub total_len: u32,
    pub pts_us: u64,
    pub decode_us: u32,
    pub hardware: u8,
    pub surface_changed: u8,
    pub _reserved: [u8; 2],
}

fn state_lock() -> &'static Mutex<Option<CoreState>> {
    STATE.get_or_init(|| Mutex::new(None))
}

#[cfg(windows)]
fn decoders_map() -> &'static Mutex<HashMap<u64, Mutex<mf_video::MfVideoDecoder>>> {
    DECODERS.get_or_init(|| Mutex::new(HashMap::new()))
}

#[cfg(not(windows))]
fn decoders_map() -> &'static Mutex<HashMap<u64, ()>> {
    DECODERS.get_or_init(|| Mutex::new(HashMap::new()))
}

fn canonical_arch() -> &'static str {
    match std::env::consts::ARCH {
        "aarch64" => "arm64",
        "x86_64" => "x64",
        _ => std::env::consts::ARCH,
    }
}

/// Writes an allocated UTF-8 JSON buffer to `(out, out_len)`.
///
/// Ownership: The caller receives ownership of `*out` with capacity == len == `*out_len`
/// and must release it with `androfuse_free(*out, *out_len)`.
///
/// # Safety
/// `out` and `out_len` must be valid, non-null, properly aligned writable pointers.
unsafe fn write_response(out: *mut *mut u8, out_len: *mut usize, payload: Vec<u8>) -> i32 {
    if out.is_null() || out_len.is_null() {
        return -1;
    }
    let mut boxed = payload.into_boxed_slice();
    let len = boxed.len();
    let ptr = boxed.as_mut_ptr();
    std::mem::forget(boxed);
    // SAFETY: Caller guarantees `out` and `out_len` are valid writable pointers.
    unsafe {
        *out = ptr;
        *out_len = len;
    }
    0
}

fn ok_json<T: Serialize>(value: T) -> Vec<u8> {
    serde_json::json!({ "ok": true, "result": value })
        .to_string()
        .into_bytes()
}

fn err_json(msg: &str) -> Vec<u8> {
    serde_json::json!({ "ok": false, "error": msg })
        .to_string()
        .into_bytes()
}

/// Reads an input byte slice from a raw C pointer and length.
///
/// # Safety
/// If `len > 0`, `ptr` must be valid for `len` bytes for the duration of the call.
unsafe fn read_slice<'a>(ptr: *const u8, len: usize) -> Result<&'a [u8], &'static str> {
    if len == 0 {
        return Ok(&[]);
    }
    if ptr.is_null() {
        return Err("null input pointer");
    }
    // SAFETY: Caller guarantees `ptr` points to `len` readable bytes.
    Ok(unsafe { std::slice::from_raw_parts(ptr, len) })
}

/// Returns the C ABI version implemented by this core library.
#[unsafe(no_mangle)]
pub extern "C" fn androfuse_abi_version() -> u32 {
    ABI_VERSION
}

/// Initializes the core with JSON `{version, state_dir, profile?}` and writes a JSON response.
///
/// Ownership: Caller retains ownership of `json`; caller owns `*out` returned and must
/// free it via `androfuse_free(*out, *out_len)`.
///
/// # Safety
/// `json` must be valid for `len` bytes; `out` and `out_len` must be valid writable pointers.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn androfuse_start(
    json: *const u8,
    len: usize,
    out: *mut *mut u8,
    out_len: *mut usize,
) -> i32 {
    let body = catch_unwind(AssertUnwindSafe(|| {
        // SAFETY: Forwarded from FFI contract requiring `json` valid for `len` bytes.
        let bytes = unsafe { read_slice(json, len) };
        let bytes = match bytes {
            Ok(b) => b,
            Err(e) => return err_json(e),
        };
        let cfg: StartConfig = match serde_json::from_slice(bytes) {
            Ok(c) => c,
            Err(e) => return err_json(&format!("invalid start config: {e}")),
        };
        if cfg.version.trim().is_empty() {
            return err_json("version is required");
        }
        let mut guard = match state_lock().lock() {
            Ok(g) => g,
            Err(p) => p.into_inner(),
        };
        *guard = Some(CoreState {
            version: cfg.version.clone(),
            state_dir: cfg.state_dir.clone(),
            profile: cfg.profile.clone(),
        });
        ok_json(StatusResult {
            version: cfg.version,
            abi: ABI_VERSION,
            state_dir: cfg.state_dir,
            profile: cfg.profile,
            os: std::env::consts::OS,
            arch: canonical_arch(),
        })
    }))
    .unwrap_or_else(|_| err_json("panic in androfuse_start"));
    // SAFETY: Caller guarantees `out` and `out_len` are valid writable pointers.
    unsafe { write_response(out, out_len, body) }
}

/// Dispatches a JSON `{method, params}` request to the core and writes a JSON response.
///
/// Ownership: Caller retains ownership of `json`; caller owns `*out` returned and must
/// free it via `androfuse_free(*out, *out_len)`.
///
/// # Safety
/// `json` must be valid for `len` bytes; `out` and `out_len` must be valid writable pointers.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn androfuse_call(
    json: *const u8,
    len: usize,
    out: *mut *mut u8,
    out_len: *mut usize,
) -> i32 {
    let body = catch_unwind(AssertUnwindSafe(|| {
        // SAFETY: Forwarded from FFI contract requiring `json` valid for `len` bytes.
        let bytes = match unsafe { read_slice(json, len) } {
            Ok(b) => b,
            Err(e) => return err_json(e),
        };
        let req: CallRequest = match serde_json::from_slice(bytes) {
            Ok(r) => r,
            Err(e) => return err_json(&format!("invalid call request: {e}")),
        };
        match req.method.as_str() {
            "diagnostics.self_check" => ok_json(diag::run_self_check()),
            "miracast.test_sink" => match miracast::test_sink_advertisement() {
                Ok(res) => ok_json(res),
                Err(e) => err_json(&e),
            },
            "mesh.start" => match serde_json::from_value::<mesh::MeshConfig>(req.params) {
                Ok(cfg) => match mesh::start(&cfg) {
                    Ok(st) => ok_json(st),
                    Err(e) => err_json(&e),
                },
                Err(e) => err_json(&format!("invalid mesh config: {e}")),
            },
            "mesh.stop" => match mesh::stop() {
                Ok(()) => ok_json(mesh::status()),
                Err(e) => err_json(&e),
            },
            "mesh.status" => ok_json(mesh::status()),
            "status" => {
                let guard = match state_lock().lock() {
                    Ok(g) => g,
                    Err(p) => p.into_inner(),
                };
                match guard.as_ref() {
                    Some(st) => ok_json(StatusResult {
                        version: st.version.clone(),
                        abi: ABI_VERSION,
                        state_dir: st.state_dir.clone(),
                        profile: st.profile.clone(),
                        os: std::env::consts::OS,
                        arch: canonical_arch(),
                    }),
                    None => err_json("core not started"),
                }
            }
            other => err_json(&format!("unknown method: {other}")),
        }
    }))
    .unwrap_or_else(|_| err_json("panic in androfuse_call"));
    // SAFETY: Caller guarantees `out` and `out_len` are valid writable pointers.
    unsafe { write_response(out, out_len, body) }
}

/// Releases a response buffer previously returned by `androfuse_start` or `androfuse_call`.
///
/// Ownership: Takes ownership of `(ptr, len)` back from the caller and drops the allocation.
///
/// # Safety
/// `ptr` and `len` must exactly match a buffer returned by `androfuse_start` or `androfuse_call`,
/// and must not be freed more than once.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn androfuse_free(ptr: *mut u8, len: usize) {
    if ptr.is_null() {
        return;
    }
    // SAFETY: Buffer was allocated via `Vec::into_boxed_slice` with exact length `len`.
    unsafe {
        let slice = std::ptr::slice_from_raw_parts_mut(ptr, len);
        drop(Box::from_raw(slice));
    }
}

/// Stops the core, destroys active decoders, and clears in-memory state.
#[unsafe(no_mangle)]
pub extern "C" fn androfuse_stop() {
    let _ = mesh::stop();
    if let Ok(mut d) = decoders_map().lock() {
        d.clear();
    }
    if let Ok(mut m) = miracast_map().lock() {
        m.clear();
    }
    if let Ok(mut a) = audio_map().lock() {
        a.clear();
    }
    if let Ok(mut mic) = mic_map().lock() {
        mic.clear();
    }
    let mut guard = match state_lock().lock() {
        Ok(g) => g,
        Err(p) => p.into_inner(),
    };
    *guard = None;
}

/// Opens a native video decoder instance (Windows Media Foundation H.264/HEVC → NV12).
///
/// Ownership: Caller owns the returned opaque numeric handle in `*out_handle` and must release it
/// with `androfuse_decoder_close`.
///
/// # Safety
/// `out_handle` must be a valid writable non-null pointer.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn androfuse_decoder_open(
    codec_id: u32,
    width: u32,
    height: u32,
    out_handle: *mut u64,
) -> i32 {
    if out_handle.is_null() {
        return -1;
    }
    catch_unwind(AssertUnwindSafe(|| {
        #[cfg(windows)]
        {
            match mf_video::MfVideoDecoder::open(codec_id, width, height) {
                Ok(dec) => {
                    let id = NEXT_DECODER_ID.fetch_add(1, Ordering::Relaxed);
                    let mut map = match decoders_map().lock() {
                        Ok(g) => g,
                        Err(p) => p.into_inner(),
                    };
                    map.insert(id, Mutex::new(dec));
                    // SAFETY: Caller guarantees `out_handle` is non-null writable storage.
                    unsafe { *out_handle = id };
                    0
                }
                Err(_) => -2,
            }
        }
        #[cfg(not(windows))]
        {
            let _ = (codec_id, width, height, &NEXT_DECODER_ID);
            let _ = decoders_map();
            -2
        }
    }))
    .unwrap_or(-99)
}

/// Feeds an encoded H.264/HEVC access unit into a decoder opened via `androfuse_decoder_open`.
///
/// Ownership: Caller retains ownership of `data`.
///
/// # Safety
/// If `len > 0`, `data` must point to `len` readable bytes for the duration of the call.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn androfuse_decoder_push(
    handle: u64,
    data: *const u8,
    len: usize,
    pts_us: u64,
    flags: u32,
) -> i32 {
    catch_unwind(AssertUnwindSafe(|| {
        // SAFETY: Caller guarantees `data` is valid for `len` bytes.
        let bytes = match unsafe { read_slice(data, len) } {
            Ok(b) => b,
            Err(_) => return -1,
        };
        #[cfg(windows)]
        {
            let map = match decoders_map().lock() {
                Ok(g) => g,
                Err(p) => p.into_inner(),
            };
            let Some(dec_lock) = map.get(&handle) else {
                return -2;
            };
            let mut dec = match dec_lock.lock() {
                Ok(g) => g,
                Err(p) => p.into_inner(),
            };
            match dec.push(bytes, pts_us, flags) {
                Ok(()) => 0,
                Err(_) => -3,
            }
        }
        #[cfg(not(windows))]
        {
            let _ = (handle, bytes, pts_us, flags);
            -2
        }
    }))
    .unwrap_or(-99)
}

/// Copies the latest decoded NV12 frame into caller-provided `dst` and writes `out_meta`.
///
/// Return values: `0` = frame written; `1` = `dst_cap` too small (`out_meta.total_len` has required
/// size); `2` = need more input; `< 0` = error.
///
/// Ownership: Caller retains ownership of `dst` and `out_meta`.
///
/// # Safety
/// `out_meta` must be valid writable storage; if `dst_cap > 0`, `dst` must point to `dst_cap`
/// writable bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn androfuse_decoder_pull_nv12(
    handle: u64,
    dst: *mut u8,
    dst_cap: usize,
    out_meta: *mut FrameMeta,
) -> i32 {
    if out_meta.is_null() {
        return -1;
    }
    catch_unwind(AssertUnwindSafe(|| {
        #[cfg(windows)]
        {
            let dst_slice = if dst_cap == 0 {
                &mut []
            } else if dst.is_null() {
                return -1;
            } else {
                // SAFETY: Caller guarantees `dst` is valid for `dst_cap` writable bytes.
                unsafe { std::slice::from_raw_parts_mut(dst, dst_cap) }
            };
            let map = match decoders_map().lock() {
                Ok(g) => g,
                Err(p) => p.into_inner(),
            };
            let Some(dec_lock) = map.get(&handle) else {
                return -2;
            };
            let mut dec = match dec_lock.lock() {
                Ok(g) => g,
                Err(p) => p.into_inner(),
            };
            let mut mf_meta = mf_video::FrameMeta::default();
            match dec.pull_nv12(dst_slice, &mut mf_meta) {
                Ok(rc) => {
                    // SAFETY: Caller guarantees `out_meta` is valid writable storage.
                    unsafe {
                        *out_meta = FrameMeta {
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
                        };
                    }
                    rc
                }
                Err(_) => -3,
            }
        }
        #[cfg(not(windows))]
        {
            let _ = (handle, dst, dst_cap);
            -2
        }
    }))
    .unwrap_or(-99)
}

/// Destroys a decoder opened via `androfuse_decoder_open` and releases its Media Foundation/D3D11 state.
#[unsafe(no_mangle)]
pub extern "C" fn androfuse_decoder_close(handle: u64) {
    if let Ok(mut map) = decoders_map().lock() {
        map.remove(&handle);
    }
}

/// Starts a Windows Wi-Fi Direct / Miracast DeX receiver session on `rtsp_port` (0 = 7236).
///
/// Ownership: Caller owns `*out_handle` and must release it with `androfuse_miracast_stop`.
///
/// # Safety
/// `out_handle` must be writable non-null storage.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn androfuse_miracast_start(rtsp_port: u16, out_handle: *mut u64) -> i32 {
    if out_handle.is_null() {
        return -1;
    }
    catch_unwind(AssertUnwindSafe(|| {
        #[cfg(windows)]
        {
            match miracast::MiracastReceiver::start(rtsp_port) {
                Ok(rx) => {
                    let id = NEXT_DECODER_ID.fetch_add(1, Ordering::Relaxed);
                    let mut map = match miracast_map().lock() {
                        Ok(g) => g,
                        Err(p) => p.into_inner(),
                    };
                    map.insert(id, Mutex::new(rx));
                    // SAFETY: Caller guarantees `out_handle` is valid writable storage.
                    unsafe { *out_handle = id };
                    0
                }
                Err(_) => -2,
            }
        }
        #[cfg(not(windows))]
        {
            let _ = rtsp_port;
            let _ = miracast_map();
            -2
        }
    }))
    .unwrap_or(-99)
}

/// Polls the newest NV12 frame and live receiver state (`0=Listening, 1=Negotiating, 2=Streaming, 3=Stopped`).
///
/// Ownership: Caller retains ownership of `dst`, `out_meta`, and `out_state`.
///
/// # Safety
/// `out_meta` must be non-null writable storage; `out_state` may be null; if `dst_cap > 0`, `dst` must
/// be writable for `dst_cap` bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn androfuse_miracast_poll_nv12(
    handle: u64,
    dst: *mut u8,
    dst_cap: usize,
    out_meta: *mut FrameMeta,
    out_state: *mut u32,
) -> i32 {
    if out_meta.is_null() {
        return -1;
    }
    catch_unwind(AssertUnwindSafe(|| {
        #[cfg(windows)]
        {
            let dst_slice = if dst_cap == 0 {
                &mut []
            } else if dst.is_null() {
                return -1;
            } else {
                // SAFETY: Caller guarantees `dst` is writable for `dst_cap` bytes.
                unsafe { std::slice::from_raw_parts_mut(dst, dst_cap) }
            };
            let map = match miracast_map().lock() {
                Ok(g) => g,
                Err(p) => p.into_inner(),
            };
            let Some(rx_lock) = map.get(&handle) else {
                return -2;
            };
            let rx = match rx_lock.lock() {
                Ok(g) => g,
                Err(p) => p.into_inner(),
            };
            let mut meta = FrameMeta::default();
            match rx.poll_nv12(dst_slice, &mut meta) {
                Ok((rc, state)) => {
                    // SAFETY: Caller guarantees `out_meta` (and non-null `out_state`) are writable.
                    unsafe {
                        *out_meta = meta;
                        if !out_state.is_null() {
                            *out_state = state;
                        }
                    }
                    rc
                }
                Err(_) => -3,
            }
        }
        #[cfg(not(windows))]
        {
            let _ = (handle, dst, dst_cap, out_state);
            -2
        }
    }))
    .unwrap_or(-99)
}

/// Sends a Miracast UIBC GENERIC pointer packet (`action`: 0=down, 1=up, 2=move) to a running session.
#[unsafe(no_mangle)]
pub extern "C" fn androfuse_miracast_input(
    handle: u64,
    action: u8,
    x_norm: u16,
    y_norm: u16,
) -> i32 {
    catch_unwind(AssertUnwindSafe(|| {
        #[cfg(windows)]
        {
            let map = match miracast_map().lock() {
                Ok(g) => g,
                Err(p) => p.into_inner(),
            };
            let Some(rx_lock) = map.get(&handle) else {
                return -2;
            };
            let rx = match rx_lock.lock() {
                Ok(g) => g,
                Err(p) => p.into_inner(),
            };
            match rx.send_pointer(action, x_norm, y_norm) {
                Ok(()) => 0,
                Err(_) => -3,
            }
        }
        #[cfg(not(windows))]
        {
            let _ = (handle, action, x_norm, y_norm);
            -2
        }
    }))
    .unwrap_or(-99)
}

/// Stops a Miracast receiver session and tears down its Wi-Fi Direct / IE advertisement cleanly.
#[unsafe(no_mangle)]
pub extern "C" fn androfuse_miracast_stop(handle: u64) {
    if let Ok(mut map) = miracast_map().lock() {
        map.remove(&handle);
    }
}

/// Opens a shared-mode WASAPI PCM audio output stream (`sample_rate` Hz, `channels` channels, s16le).
///
/// Ownership: Caller owns the returned opaque handle in `*out_handle` and must release it with
/// `androfuse_audio_close`.
///
/// # Safety
/// `out_handle` must be a valid writable pointer.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn androfuse_audio_open(
    sample_rate: u32,
    channels: u16,
    out_handle: *mut u64,
) -> i32 {
    if out_handle.is_null() {
        return -1;
    }
    catch_unwind(AssertUnwindSafe(|| {
        #[cfg(windows)]
        {
            match wasapi_audio::WasapiAudioPlayer::open(sample_rate, channels) {
                Ok(player) => {
                    let id = NEXT_DECODER_ID.fetch_add(1, Ordering::Relaxed);
                    let mut map = match audio_map().lock() {
                        Ok(g) => g,
                        Err(p) => p.into_inner(),
                    };
                    map.insert(id, Arc::new(player));
                    // SAFETY: `out_handle` was checked non-null and is guaranteed writable by caller.
                    unsafe {
                        *out_handle = id;
                    }
                    0
                }
                Err(_) => -3,
            }
        }
        #[cfg(not(windows))]
        {
            let _ = (sample_rate, channels);
            -2
        }
    }))
    .unwrap_or(-99)
}

/// Pushes interleaved signed 16-bit PCM frames to an open WASAPI audio output stream.
///
/// # Safety
/// If `len > 0`, `data` must be valid for `len` readable bytes for the duration of the call.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn androfuse_audio_push_pcm(handle: u64, data: *const u8, len: usize) -> i32 {
    catch_unwind(AssertUnwindSafe(|| {
        #[cfg(windows)]
        {
            // SAFETY: Forwarded from FFI contract requiring `data` valid for `len` bytes.
            let slice = match unsafe { read_slice(data, len) } {
                Ok(s) => s,
                Err(_) => return -1,
            };
            let player = {
                let map = match audio_map().lock() {
                    Ok(g) => g,
                    Err(p) => p.into_inner(),
                };
                map.get(&handle).cloned()
            };
            let Some(player) = player else {
                return -2;
            };
            player.push_pcm(slice);
            0
        }
        #[cfg(not(windows))]
        {
            let _ = (handle, data, len);
            -2
        }
    }))
    .unwrap_or(-99)
}

/// Closes a WASAPI audio output stream handle and joins its render thread.
#[unsafe(no_mangle)]
pub extern "C" fn androfuse_audio_close(handle: u64) {
    if let Ok(mut map) = audio_map().lock() {
        map.remove(&handle);
    }
}

/// Opens a shared-mode WASAPI microphone PCM capture stream (Windows only).
///
/// Ownership: Caller owns the returned opaque numeric handle in `*out_handle` and must release it
/// with `androfuse_mic_close`.
///
/// # Safety
/// `out_handle` must be a valid writable non-null pointer.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn androfuse_mic_open(
    sample_rate: u32,
    channels: u16,
    out_handle: *mut u64,
) -> i32 {
    if out_handle.is_null() {
        return -1;
    }
    catch_unwind(AssertUnwindSafe(|| {
        #[cfg(windows)]
        {
            match wasapi_audio::WasapiMicCapturer::open(sample_rate, channels) {
                Ok(cap) => {
                    let id = NEXT_DECODER_ID.fetch_add(1, Ordering::Relaxed);
                    let mut map = match mic_map().lock() {
                        Ok(g) => g,
                        Err(p) => p.into_inner(),
                    };
                    map.insert(id, Arc::new(cap));
                    // SAFETY: `out_handle` was checked non-null and is guaranteed writable by caller.
                    unsafe {
                        *out_handle = id;
                    }
                    0
                }
                Err(_) => -3,
            }
        }
        #[cfg(not(windows))]
        {
            let _ = (sample_rate, channels);
            -2
        }
    }))
    .unwrap_or(-99)
}

/// Reads captured signed 16-bit PCM frames into caller buffer `dst` (up to `dst_cap` bytes),
/// writing the actual byte count copied into `*out_len`.
///
/// # Safety
/// If `dst_cap > 0`, `dst` must be writable for `dst_cap` bytes; `out_len` must be a valid writable pointer.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn androfuse_mic_read_pcm(
    handle: u64,
    dst: *mut u8,
    dst_cap: usize,
    out_len: *mut usize,
) -> i32 {
    if out_len.is_null() || (dst_cap > 0 && dst.is_null()) {
        return -1;
    }
    catch_unwind(AssertUnwindSafe(|| {
        #[cfg(windows)]
        {
            let cap = {
                let map = match mic_map().lock() {
                    Ok(g) => g,
                    Err(p) => p.into_inner(),
                };
                map.get(&handle).cloned()
            };
            let Some(cap) = cap else {
                return -2;
            };
            let n = if dst_cap == 0 {
                0
            } else {
                // SAFETY: Caller guarantees `dst` is writable for `dst_cap` bytes.
                let slice = unsafe { std::slice::from_raw_parts_mut(dst, dst_cap) };
                cap.read_pcm(slice)
            };
            // SAFETY: Caller guarantees `out_len` is valid writable storage.
            unsafe {
                *out_len = n;
            }
            0
        }
        #[cfg(not(windows))]
        {
            let _ = (handle, dst, dst_cap, out_len);
            -2
        }
    }))
    .unwrap_or(-99)
}

/// Closes a WASAPI microphone capture handle and joins its capture thread.
#[unsafe(no_mangle)]
pub extern "C" fn androfuse_mic_close(handle: u64) {
    if let Ok(mut map) = mic_map().lock() {
        map.remove(&handle);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn abi_start_status_diag_stop_roundtrip() {
        assert_eq!(androfuse_abi_version(), ABI_VERSION);

        let start_req = br#"{"version":"0.1.0","state_dir":"C:\\tmp","profile":"test"}"#;
        let mut out: *mut u8 = std::ptr::null_mut();
        let mut out_len: usize = 0;
        // SAFETY: Valid slice and stack pointers within test.
        let rc =
            unsafe { androfuse_start(start_req.as_ptr(), start_req.len(), &mut out, &mut out_len) };
        assert_eq!(rc, 0);
        // SAFETY: `out` was allocated by `androfuse_start` with length `out_len`.
        let start_resp: serde_json::Value = unsafe {
            let s = std::slice::from_raw_parts(out, out_len);
            let v = serde_json::from_slice(s).unwrap();
            androfuse_free(out, out_len);
            v
        };
        assert_eq!(start_resp["ok"], true);
        assert_eq!(start_resp["result"]["abi"], ABI_VERSION);

        let diag_req = br#"{"method":"diagnostics.self_check"}"#;
        // SAFETY: Valid slice and stack pointers within test.
        let rc =
            unsafe { androfuse_call(diag_req.as_ptr(), diag_req.len(), &mut out, &mut out_len) };
        assert_eq!(rc, 0);
        // SAFETY: `out` was allocated by `androfuse_call` with length `out_len`.
        let diag_resp: serde_json::Value = unsafe {
            let s = std::slice::from_raw_parts(out, out_len);
            let v = serde_json::from_slice(s).unwrap();
            androfuse_free(out, out_len);
            v
        };
        assert_eq!(diag_resp["ok"], true);
        assert!(diag_resp["result"]["native_arch"].is_string());

        androfuse_stop();

        let status_req = br#"{"method":"status"}"#;
        // SAFETY: Valid slice and stack pointers within test.
        let rc = unsafe {
            androfuse_call(
                status_req.as_ptr(),
                status_req.len(),
                &mut out,
                &mut out_len,
            )
        };
        assert_eq!(rc, 0);
        // SAFETY: `out` was allocated by `androfuse_call` with length `out_len`.
        let status_resp: serde_json::Value = unsafe {
            let s = std::slice::from_raw_parts(out, out_len);
            let v = serde_json::from_slice(s).unwrap();
            androfuse_free(out, out_len);
            v
        };
        assert_eq!(status_resp["ok"], false);
    }

    #[cfg(windows)]
    #[test]
    fn mf_decoder_open_and_synthetic_h264_nv12_roundtrip() {
        use crate::scrcpy::CODEC_H264;

        fn decode_b64(s: &str) -> Vec<u8> {
            let mut out = Vec::new();
            let mut acc = 0u32;
            let mut bits = 0u8;
            for b in s.bytes() {
                let val = match b {
                    b'A'..=b'Z' => b - b'A',
                    b'a'..=b'z' => b - b'a' + 26,
                    b'0'..=b'9' => b - b'0' + 52,
                    b'+' => 62,
                    b'/' => 63,
                    _ => continue,
                };
                acc = (acc << 6) | u32::from(val);
                bits += 6;
                if bits >= 8 {
                    bits -= 8;
                    out.push((acc >> bits) as u8);
                }
            }
            out
        }

        let fixture: serde_json::Value = serde_json::from_str(include_str!(
            "../../../internal/scrcpy/testdata/fold8_scrcpy_session.json"
        ))
        .unwrap();
        let packets = fixture["packets"].as_array().unwrap();
        let csd = decode_b64(packets[1]["payload_b64"].as_str().unwrap());

        let mut handle = 0u64;
        // SAFETY: Stack pointer `handle` is valid writable storage.
        let rc = unsafe { androfuse_decoder_open(CODEC_H264, 64, 48, &mut handle) };
        assert_eq!(rc, 0, "Media Foundation H.264 decoder failed to open");
        assert!(handle != 0);

        let mut decoded = 0u32;
        let mut meta = FrameMeta::default();
        let mut buf = vec![0u8; 64 * 1024];
        for (idx, pkt) in packets[2..].iter().enumerate() {
            let mut au = Vec::new();
            if pkt["is_keyframe"].as_bool().unwrap_or(false) {
                au.extend_from_slice(&csd);
            }
            au.extend_from_slice(&decode_b64(pkt["payload_b64"].as_str().unwrap()));
            let pts = pkt["pts_us"].as_u64().unwrap_or(0);
            let flags = u32::from(idx == 0);
            // SAFETY: `au` is a valid Rust slice.
            let push_rc =
                unsafe { androfuse_decoder_push(handle, au.as_ptr(), au.len(), pts, flags) };
            assert_eq!(push_rc, 0);
            // SAFETY: `buf` and `meta` are valid writable buffers.
            if unsafe {
                androfuse_decoder_pull_nv12(handle, buf.as_mut_ptr(), buf.len(), &mut meta)
            } == 0
            {
                assert_eq!((meta.width, meta.height), (64, 48));
                decoded += 1;
            }
        }
        let flush_rc = {
            // SAFETY: Null data with zero length is valid for drain-only call (flags = 2).
            unsafe { androfuse_decoder_push(handle, std::ptr::null(), 0, 0, 2) }
        };
        assert_eq!(flush_rc, 0);
        // SAFETY: `buf` and `meta` are valid writable buffers.
        let pull_flush =
            unsafe { androfuse_decoder_pull_nv12(handle, buf.as_mut_ptr(), buf.len(), &mut meta) };
        if pull_flush == 0 {
            assert_eq!((meta.width, meta.height), (64, 48));
            decoded += 1;
        }
        assert!(decoded >= 1, "expected >= 1 decoded 64x48 NV12 frame");

        androfuse_decoder_close(handle);
    }
}
