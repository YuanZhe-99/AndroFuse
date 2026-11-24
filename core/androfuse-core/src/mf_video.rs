//! Windows Media Foundation H.264/HEVC NV12 video decoder (`#[cfg(windows)]`).
//!
//! Dynamically resolves `mfplat.dll` free functions from `System32` so Windows N
//! SKUs missing the Media Feature Pack still load `androfuse_core.dll` and fail
//! gracefully when opening a decoder. Uses `IMFDXGIDeviceManager` + DXVA/D3D11
//! staging texture readback when the MFT is `MF_SA_D3D11_AWARE`, respecting full
//! macroblock surface heights (`MF_MT_FRAME_SIZE`) vs visible crop dimensions.

#![cfg(windows)]

use crate::scrcpy::{CODEC_H264, CODEC_H265};
use std::ffi::c_void;
use std::mem::ManuallyDrop;
use std::time::Instant;
use windows::Win32::Foundation::{E_NOINTERFACE, FreeLibrary, HMODULE, S_OK};
use windows::Win32::Graphics::Direct3D::{
    D3D_DRIVER_TYPE_HARDWARE, D3D_FEATURE_LEVEL_11_0, D3D_FEATURE_LEVEL_11_1,
};
use windows::Win32::Graphics::Direct3D11::{
    D3D11_CPU_ACCESS_READ, D3D11_CREATE_DEVICE_BGRA_SUPPORT, D3D11_CREATE_DEVICE_VIDEO_SUPPORT,
    D3D11_MAP_READ, D3D11_MAPPED_SUBRESOURCE, D3D11_SDK_VERSION, D3D11_TEXTURE2D_DESC,
    D3D11_USAGE_STAGING, D3D11CreateDevice, ID3D11Device, ID3D11DeviceContext, ID3D11Texture2D,
};
use windows::Win32::Graphics::Dxgi::Common::{DXGI_FORMAT_NV12, DXGI_SAMPLE_DESC};
use windows::Win32::Media::MediaFoundation::{
    IMF2DBuffer, IMFActivate, IMFDXGIBuffer, IMFDXGIDeviceManager, IMFMediaBuffer, IMFMediaType,
    IMFSample, IMFTransform, MF_E_NO_MORE_TYPES, MF_E_NOTACCEPTING, MF_E_TRANSFORM_NEED_MORE_INPUT,
    MF_E_TRANSFORM_STREAM_CHANGE, MF_LOW_LATENCY, MF_MT_DEFAULT_STRIDE, MF_MT_FRAME_RATE,
    MF_MT_FRAME_SIZE, MF_MT_INTERLACE_MODE, MF_MT_MAJOR_TYPE, MF_MT_MINIMUM_DISPLAY_APERTURE,
    MF_MT_SUBTYPE, MF_SA_D3D11_AWARE, MF_VERSION, MFMediaType_Video,
    MFSampleExtension_Discontinuity, MFT_CATEGORY_VIDEO_DECODER, MFT_MESSAGE_COMMAND_DRAIN,
    MFT_MESSAGE_COMMAND_FLUSH, MFT_MESSAGE_NOTIFY_BEGIN_STREAMING,
    MFT_MESSAGE_NOTIFY_END_OF_STREAM, MFT_MESSAGE_NOTIFY_START_OF_STREAM,
    MFT_MESSAGE_SET_D3D_MANAGER, MFT_OUTPUT_DATA_BUFFER, MFT_OUTPUT_STREAM_PROVIDES_SAMPLES,
    MFT_REGISTER_TYPE_INFO, MFVideoFormat_H264, MFVideoFormat_HEVC, MFVideoFormat_NV12,
    MFVideoInterlace_Progressive,
};
use windows::Win32::System::Com::{
    CO_MTA_USAGE_COOKIE, CoDecrementMTAUsage, CoIncrementMTAUsage, CoTaskMemFree,
};
use windows::Win32::System::LibraryLoader::{
    GetProcAddress, LOAD_LIBRARY_SEARCH_SYSTEM32, LoadLibraryExW,
};
use windows::core::{GUID, HRESULT, Interface, PCSTR, PCWSTR};

type MfStartupFn = unsafe extern "system" fn(u32, u32) -> HRESULT;
type MfShutdownFn = unsafe extern "system" fn() -> HRESULT;
type MfCreateMediaTypeFn = unsafe extern "system" fn(*mut *mut c_void) -> HRESULT;
type MfCreateSampleFn = unsafe extern "system" fn(*mut *mut c_void) -> HRESULT;
type MfCreateMemoryBufferFn = unsafe extern "system" fn(u32, *mut *mut c_void) -> HRESULT;
type MfCreateDxgiDeviceManagerFn = unsafe extern "system" fn(*mut u32, *mut *mut c_void) -> HRESULT;
type MftEnumExFn = unsafe extern "system" fn(
    GUID,
    u32,
    *const MFT_REGISTER_TYPE_INFO,
    *const MFT_REGISTER_TYPE_INFO,
    *mut *mut *mut c_void,
    *mut u32,
) -> HRESULT;

const MFSTARTUP_NOSOCKET: u32 = 1;
const MFT_ENUM_FLAG_SYNCMFT: u32 = 1;
const MFT_ENUM_FLAG_ASYNCMFT: u32 = 2;
const MFT_ENUM_FLAG_HARDWARE: u32 = 4;
const MFT_ENUM_FLAG_SORTANDFILTER: u32 = 0x40;

/// Decoded NV12 frame metadata reported across the C ABI.
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

#[repr(C)]
#[derive(Clone, Copy)]
struct MfVideoArea {
    offset_x: i32,
    offset_y: i32,
    width: i32,
    height: i32,
}

struct MfApi {
    module: HMODULE,
    shutdown: MfShutdownFn,
    create_media_type: MfCreateMediaTypeFn,
    create_sample: MfCreateSampleFn,
    create_memory_buffer: MfCreateMemoryBufferFn,
    create_dxgi_manager: Option<MfCreateDxgiDeviceManagerFn>,
    mft_enum_ex: MftEnumExFn,
    mta_cookie: CO_MTA_USAGE_COOKIE,
}

impl MfApi {
    fn load() -> Result<Self, String> {
        let wide: Vec<u16> = "mfplat.dll\0".encode_utf16().collect();
        // SAFETY: `wide` is NUL-terminated and loaded exclusively from System32.
        let module =
            unsafe { LoadLibraryExW(PCWSTR(wide.as_ptr()), None, LOAD_LIBRARY_SEARCH_SYSTEM32) }
                .map_err(|e| format!("mfplat.dll unavailable: {e}"))?;

        let resolve = |name: PCSTR| -> Result<unsafe extern "system" fn() -> isize, String> {
            // SAFETY: `module` is valid and `name` is NUL-terminated ASCII.
            unsafe { GetProcAddress(module, name) }
                .ok_or_else(|| "missing expected export in mfplat.dll".to_string())
        };

        // SAFETY: Function pointer transmutes match Microsoft mfapi.h ABI definitions.
        let (
            startup,
            shutdown,
            create_media_type,
            create_sample,
            create_memory_buffer,
            mft_enum_ex,
        ) = unsafe {
            (
                std::mem::transmute::<unsafe extern "system" fn() -> isize, MfStartupFn>(resolve(
                    PCSTR(c"MFStartup".as_ptr().cast()),
                )?),
                std::mem::transmute::<unsafe extern "system" fn() -> isize, MfShutdownFn>(resolve(
                    PCSTR(c"MFShutdown".as_ptr().cast()),
                )?),
                std::mem::transmute::<unsafe extern "system" fn() -> isize, MfCreateMediaTypeFn>(
                    resolve(PCSTR(c"MFCreateMediaType".as_ptr().cast()))?,
                ),
                std::mem::transmute::<unsafe extern "system" fn() -> isize, MfCreateSampleFn>(
                    resolve(PCSTR(c"MFCreateSample".as_ptr().cast()))?,
                ),
                std::mem::transmute::<unsafe extern "system" fn() -> isize, MfCreateMemoryBufferFn>(
                    resolve(PCSTR(c"MFCreateMemoryBuffer".as_ptr().cast()))?,
                ),
                std::mem::transmute::<unsafe extern "system" fn() -> isize, MftEnumExFn>(resolve(
                    PCSTR(c"MFTEnumEx".as_ptr().cast()),
                )?),
            )
        };
        // SAFETY: Same as above; optional for older systems.
        let create_dxgi_manager = unsafe {
            GetProcAddress(module, PCSTR(c"MFCreateDXGIDeviceManager".as_ptr().cast())).map(|f| {
                std::mem::transmute::<
                    unsafe extern "system" fn() -> isize,
                    MfCreateDxgiDeviceManagerFn,
                >(f)
            })
        };

        // SAFETY: Process-wide MTA usage cookie is safe to release from any thread.
        let mta_cookie = unsafe { CoIncrementMTAUsage() }.map_err(|e| {
            // SAFETY: `module` is owned by us here before `Self` construction.
            unsafe {
                let _ = FreeLibrary(module);
            }
            format!("CoIncrementMTAUsage failed: {e}")
        })?;

        // SAFETY: Calls MFStartup with MF_VERSION and MFSTARTUP_NOSOCKET.
        let mf_hr = unsafe { startup(MF_VERSION, MFSTARTUP_NOSOCKET) };
        if mf_hr.is_err() {
            // SAFETY: Balances CoIncrementMTAUsage and LoadLibraryExW on failure.
            unsafe {
                let _ = CoDecrementMTAUsage(mta_cookie);
                let _ = FreeLibrary(module);
            }
            return Err(format!("MFStartup failed: {mf_hr:?}"));
        }

        Ok(Self {
            module,
            shutdown,
            create_media_type,
            create_sample,
            create_memory_buffer,
            create_dxgi_manager,
            mft_enum_ex,
            mta_cookie,
        })
    }

    fn create_media_type(&self) -> Result<IMFMediaType, String> {
        let mut raw: *mut c_void = std::ptr::null_mut();
        // SAFETY: `raw` points to stack storage for COM output pointer.
        let hr = unsafe { (self.create_media_type)(&mut raw) };
        if hr.is_err() || raw.is_null() {
            return Err(format!("MFCreateMediaType failed: {hr:?}"));
        }
        // SAFETY: `MFCreateMediaType` returns an owned `IMFMediaType` COM pointer.
        Ok(unsafe { IMFMediaType::from_raw(raw) })
    }

    fn create_sample(&self) -> Result<IMFSample, String> {
        let mut raw: *mut c_void = std::ptr::null_mut();
        // SAFETY: `raw` points to stack storage for COM output pointer.
        let hr = unsafe { (self.create_sample)(&mut raw) };
        if hr.is_err() || raw.is_null() {
            return Err(format!("MFCreateSample failed: {hr:?}"));
        }
        // SAFETY: `MFCreateSample` returns an owned `IMFSample` COM pointer.
        Ok(unsafe { IMFSample::from_raw(raw) })
    }

    fn create_memory_buffer(&self, size: u32) -> Result<IMFMediaBuffer, String> {
        let mut raw: *mut c_void = std::ptr::null_mut();
        // SAFETY: `raw` points to stack storage for COM output pointer.
        let hr = unsafe { (self.create_memory_buffer)(size, &mut raw) };
        if hr.is_err() || raw.is_null() {
            return Err(format!("MFCreateMemoryBuffer failed: {hr:?}"));
        }
        // SAFETY: `MFCreateMemoryBuffer` returns an owned `IMFMediaBuffer` COM pointer.
        Ok(unsafe { IMFMediaBuffer::from_raw(raw) })
    }
}

impl Drop for MfApi {
    fn drop(&mut self) {
        // SAFETY: Balances successful MFStartup, CoIncrementMTAUsage, and LoadLibraryExW.
        unsafe {
            let _ = (self.shutdown)();
            let _ = CoDecrementMTAUsage(self.mta_cookie);
            let _ = FreeLibrary(self.module);
        }
    }
}

// SAFETY: All COM and D3D11 interfaces held inside MfVideoDecoder are MTA-agile / thread-safe and
// serialized by an external Mutex around each decoder handle.
unsafe impl Send for MfVideoDecoder {}

struct CachedNv12Frame {
    width: u32,
    height: u32,
    y_stride: u32,
    uv_stride: u32,
    pts_us: u64,
    decode_us: u32,
    y: Vec<u8>,
    uv: Vec<u8>,
}

type CreatedDecoder = (
    IMFTransform,
    Option<ID3D11Device>,
    Option<ID3D11DeviceContext>,
    Option<IMFDXGIDeviceManager>,
    bool,
);

/// Hardware-preferred Media Foundation H.264/HEVC decoder outputting planar NV12 frames.
pub struct MfVideoDecoder {
    mf: MfApi,
    transform: IMFTransform,
    _dxgi_manager: Option<IMFDXGIDeviceManager>,
    d3d_device: Option<ID3D11Device>,
    d3d_context: Option<ID3D11DeviceContext>,
    staging: Option<(ID3D11Texture2D, u32, u32)>,
    input_subtype: GUID,
    visible_w: u32,
    visible_h: u32,
    surface_w: u32,
    surface_h: u32,
    default_stride: u32,
    last_reported_dims: (u32, u32),
    hardware: bool,
    pending_frame: Option<CachedNv12Frame>,
}

impl MfVideoDecoder {
    /// Opens an H.264 or HEVC Media Foundation decoder with initial dimensions.
    pub fn open(codec_id: u32, width: u32, height: u32) -> Result<Self, String> {
        let mf = MfApi::load()?;
        let subtype = match codec_id {
            CODEC_H264 => MFVideoFormat_H264,
            CODEC_H265 => MFVideoFormat_HEVC,
            other => return Err(format!("unsupported codec_id 0x{other:08x}")),
        };
        let (transform, d3d_device, d3d_context, dxgi_manager, hardware) =
            create_decoder(&mf, subtype)?;

        let mut dec = Self {
            mf,
            transform,
            _dxgi_manager: dxgi_manager,
            d3d_device,
            d3d_context,
            staging: None,
            input_subtype: subtype,
            visible_w: width.max(16),
            visible_h: height.max(16),
            surface_w: width.max(16),
            surface_h: height.max(16),
            default_stride: width.max(16),
            last_reported_dims: (0, 0),
            hardware,
            pending_frame: None,
        };
        dec.configure_types()?;
        // SAFETY: Notifies Media Foundation transform streaming lifecycle start.
        unsafe {
            dec.transform
                .ProcessMessage(MFT_MESSAGE_NOTIFY_BEGIN_STREAMING, 0)
                .map_err(|e| format!("MFT_MESSAGE_NOTIFY_BEGIN_STREAMING failed: {e}"))?;
            dec.transform
                .ProcessMessage(MFT_MESSAGE_NOTIFY_START_OF_STREAM, 0)
                .map_err(|e| format!("MFT_MESSAGE_NOTIFY_START_OF_STREAM failed: {e}"))?;
        }
        Ok(dec)
    }

    /// Feeds one encoded Annex-B access unit into Media Foundation.
    ///
    /// Notes: `flags` bit 0 marks discontinuity; bit 1 issues `MFT_MESSAGE_COMMAND_DRAIN`
    /// so any buffered frame is flushed out immediately.
    pub fn push(&mut self, data: &[u8], pts_us: u64, flags: u32) -> Result<(), String> {
        let discontinuity = (flags & 1) != 0;
        let drain = (flags & 2) != 0;
        let started = Instant::now();
        if !data.is_empty() {
            let buffer = self
                .mf
                .create_memory_buffer(u32::try_from(data.len()).map_err(|_| "frame too large")?)?;
            // SAFETY: `Lock` writes incoming Annex-B bytes into the COM memory buffer.
            unsafe {
                let mut ptr = std::ptr::null_mut();
                buffer
                    .Lock(&mut ptr, None, None)
                    .map_err(|e| format!("input buffer Lock failed: {e}"))?;
                std::ptr::copy_nonoverlapping(data.as_ptr(), ptr, data.len());
                buffer
                    .Unlock()
                    .map_err(|e| format!("input buffer Unlock failed: {e}"))?;
                buffer
                    .SetCurrentLength(data.len() as u32)
                    .map_err(|e| format!("SetCurrentLength failed: {e}"))?;
            }
            let sample = self.mf.create_sample()?;
            // SAFETY: Configures sample timestamps (100ns units) and buffer attachment.
            unsafe {
                sample
                    .AddBuffer(&buffer)
                    .map_err(|e| format!("AddBuffer failed: {e}"))?;
                sample
                    .SetSampleTime((pts_us as i64).saturating_mul(10))
                    .map_err(|e| format!("SetSampleTime failed: {e}"))?;
                sample
                    .SetSampleDuration(166_666)
                    .map_err(|e| format!("SetSampleDuration failed: {e}"))?;
                if discontinuity {
                    let _ = sample.SetUINT32(&MFSampleExtension_Discontinuity, 1);
                }
                match self.transform.ProcessInput(0, &sample, 0) {
                    Ok(()) => {}
                    Err(e) if e.code() == MF_E_NOTACCEPTING => {
                        self.drain_latest(started)?;
                        self.transform
                            .ProcessInput(0, &sample, 0)
                            .map_err(|err| format!("ProcessInput retry failed: {err}"))?;
                    }
                    Err(e) => return Err(format!("ProcessInput failed: {e}")),
                }
            }
        }
        if drain {
            // SAFETY: Ends current access-unit sequence, drains buffered output, and re-arms stream start.
            unsafe {
                let _ = self
                    .transform
                    .ProcessMessage(MFT_MESSAGE_NOTIFY_END_OF_STREAM, 0);
                let _ = self.transform.ProcessMessage(MFT_MESSAGE_COMMAND_DRAIN, 0);
            }
        }
        let res = self.drain_latest(started);
        if drain {
            // SAFETY: Re-arms stream acceptance if the caller continues feeding frames after a flush.
            unsafe {
                let _ = self
                    .transform
                    .ProcessMessage(MFT_MESSAGE_NOTIFY_START_OF_STREAM, 0);
            }
        }
        res
    }

    /// Writes the latest decoded NV12 frame into caller-provided `dst` without reallocating.
    ///
    /// Return codes:
    /// - `0`: Frame written and `meta` updated.
    /// - `1`: `dst.len()` smaller than `meta.total_len`; caller should grow buffer and retry (frame remains pending).
    /// - `2`: No frame available yet (`MF_E_TRANSFORM_NEED_MORE_INPUT`).
    pub fn pull_nv12(&mut self, dst: &mut [u8], meta: &mut FrameMeta) -> Result<i32, String> {
        if self.pending_frame.is_none() {
            self.drain_latest(Instant::now())?;
        }
        let Some(frame) = self.pending_frame.as_ref() else {
            return Ok(2);
        };
        let uv_h = frame.height.div_ceil(2);
        let y_bytes = frame.y_stride * frame.height;
        let uv_bytes = frame.uv_stride * uv_h;
        let total_len = y_bytes + uv_bytes;
        let changed = u8::from(self.last_reported_dims != (frame.width, frame.height));

        *meta = FrameMeta {
            width: frame.width,
            height: frame.height,
            y_stride: frame.y_stride,
            uv_stride: frame.uv_stride,
            uv_offset: y_bytes,
            total_len,
            pts_us: frame.pts_us,
            decode_us: frame.decode_us,
            hardware: u8::from(self.hardware),
            surface_changed: changed,
            _reserved: [0; 2],
        };

        if dst.len() < total_len as usize {
            return Ok(1);
        }

        let frame = self.pending_frame.take().expect("checked above");
        self.last_reported_dims = (frame.width, frame.height);
        dst[..y_bytes as usize].copy_from_slice(&frame.y[..y_bytes as usize]);
        dst[y_bytes as usize..total_len as usize].copy_from_slice(&frame.uv[..uv_bytes as usize]);
        Ok(0)
    }

    fn configure_types(&mut self) -> Result<(), String> {
        let input = self.mf.create_media_type()?;
        // SAFETY: Populating standard Media Foundation video input type attributes.
        unsafe {
            input
                .SetGUID(&MF_MT_MAJOR_TYPE, &MFMediaType_Video)
                .map_err(|e| format!("SetGUID(major): {e}"))?;
            input
                .SetGUID(&MF_MT_SUBTYPE, &self.input_subtype)
                .map_err(|e| format!("SetGUID(subtype): {e}"))?;
            input
                .SetUINT64(&MF_MT_FRAME_SIZE, pack_u32(self.visible_w, self.visible_h))
                .map_err(|e| format!("SetUINT64(frame_size): {e}"))?;
            input
                .SetUINT64(&MF_MT_FRAME_RATE, pack_u32(60, 1))
                .map_err(|e| format!("SetUINT64(frame_rate): {e}"))?;
            input
                .SetUINT32(&MF_MT_INTERLACE_MODE, MFVideoInterlace_Progressive.0 as u32)
                .map_err(|e| format!("SetUINT32(interlace): {e}"))?;
            self.transform
                .SetInputType(0, &input, 0)
                .map_err(|e| format!("SetInputType failed: {e}"))?;
        }
        self.select_nv12_output()
    }

    fn select_nv12_output(&mut self) -> Result<(), String> {
        // SAFETY: Iterates output types on stream 0 until MFVideoFormat_NV12 is found.
        unsafe {
            for idx in 0..64 {
                let output = match self.transform.GetOutputAvailableType(0, idx) {
                    Ok(t) => t,
                    Err(e) if e.code() == MF_E_NO_MORE_TYPES => break,
                    Err(e) => return Err(format!("GetOutputAvailableType failed: {e}")),
                };
                if output.GetGUID(&MF_MT_SUBTYPE).ok() == Some(MFVideoFormat_NV12) {
                    self.transform
                        .SetOutputType(0, &output, 0)
                        .map_err(|e| format!("SetOutputType(NV12) failed: {e}"))?;
                    self.refresh_output_geometry()?;
                    return Ok(());
                }
            }
        }
        Err("Media Foundation decoder offered no NV12 output format".to_string())
    }

    fn refresh_output_geometry(&mut self) -> Result<(), String> {
        // SAFETY: Queries current negotiated output IMFMediaType attributes.
        unsafe {
            let current = self
                .transform
                .GetOutputCurrentType(0)
                .map_err(|e| format!("GetOutputCurrentType failed: {e}"))?;
            if let Ok(packed) = current.GetUINT64(&MF_MT_FRAME_SIZE) {
                let (sw, sh) = unpack_u32(packed);
                if sw > 0 && sh > 0 {
                    self.surface_w = sw;
                    self.surface_h = sh;
                }
            }
            self.default_stride = current
                .GetUINT32(&MF_MT_DEFAULT_STRIDE)
                .unwrap_or(self.surface_w)
                .max(self.surface_w);

            let mut area = MfVideoArea {
                offset_x: 0,
                offset_y: 0,
                width: 0,
                height: 0,
            };
            let mut blob_size = 0u32;
            let got_crop = current
                .GetBlob(
                    &MF_MT_MINIMUM_DISPLAY_APERTURE,
                    std::slice::from_raw_parts_mut(
                        (&mut area as *mut MfVideoArea).cast::<u8>(),
                        std::mem::size_of::<MfVideoArea>(),
                    ),
                    Some(&mut blob_size),
                )
                .is_ok();
            if got_crop && area.width > 0 && area.height > 0 {
                self.visible_w = area.width as u32;
                self.visible_h = area.height as u32;
            } else {
                self.visible_w = self.surface_w;
                self.visible_h = self.surface_h;
            }
        }
        Ok(())
    }

    fn drain_latest(&mut self, started: Instant) -> Result<(), String> {
        // SAFETY: Standard Media Foundation ProcessOutput loop on stream 0.
        unsafe {
            loop {
                let info = self
                    .transform
                    .GetOutputStreamInfo(0)
                    .map_err(|e| format!("GetOutputStreamInfo failed: {e}"))?;
                let mft_provides =
                    (info.dwFlags & (MFT_OUTPUT_STREAM_PROVIDES_SAMPLES.0 as u32)) != 0;
                let sample = if mft_provides {
                    None
                } else {
                    let s = self.mf.create_sample()?;
                    let buf_cap = info
                        .cbSize
                        .max(self.default_stride * self.surface_h * 3 / 2);
                    s.AddBuffer(&self.mf.create_memory_buffer(buf_cap)?)
                        .map_err(|e| format!("output AddBuffer failed: {e}"))?;
                    Some(s)
                };

                let mut out_buf = [MFT_OUTPUT_DATA_BUFFER {
                    pSample: ManuallyDrop::new(sample),
                    ..Default::default()
                }];
                let mut status = 0u32;
                let hr = self.transform.ProcessOutput(0, &mut out_buf, &mut status);
                let out_sample = ManuallyDrop::take(&mut out_buf[0].pSample);
                drop(ManuallyDrop::take(&mut out_buf[0].pEvents));

                match hr {
                    Ok(()) => {
                        let Some(sample) = out_sample else {
                            return Err("MFT ProcessOutput succeeded with null sample".into());
                        };
                        let frame =
                            self.extract_nv12(&sample, started.elapsed().as_micros() as u32)?;
                        self.pending_frame = Some(frame);
                    }
                    Err(e) if e.code() == MF_E_TRANSFORM_NEED_MORE_INPUT => break,
                    Err(e) if e.code() == MF_E_TRANSFORM_STREAM_CHANGE => {
                        self.select_nv12_output()?;
                    }
                    Err(e) => return Err(format!("ProcessOutput failed: {e}")),
                }
            }
        }
        Ok(())
    }

    fn extract_nv12(
        &mut self,
        sample: &IMFSample,
        decode_us: u32,
    ) -> Result<CachedNv12Frame, String> {
        // SAFETY: Reads either DXGI D3D11 NV12 surface (via staging texture) or CPU IMF2DBuffer/IMFMediaBuffer.
        unsafe {
            let pts_100ns = sample.GetSampleTime().unwrap_or(0).max(0) as u64;
            let pts_us = pts_100ns / 10;
            let buffer = sample
                .GetBufferByIndex(0)
                .map_err(|e| format!("GetBufferByIndex(0): {e}"))?;

            if let Ok(dxgi) = buffer.cast::<IMFDXGIBuffer>() {
                let mut raw_tex = std::ptr::null_mut();
                dxgi.GetResource(&ID3D11Texture2D::IID, &mut raw_tex)
                    .map_err(|e| format!("IMFDXGIBuffer::GetResource: {e}"))?;
                let src_tex = ID3D11Texture2D::from_raw(raw_tex);
                let subresource = dxgi.GetSubresourceIndex().unwrap_or(0);
                let mut src_desc = D3D11_TEXTURE2D_DESC::default();
                src_tex.GetDesc(&mut src_desc);

                let device = self
                    .d3d_device
                    .as_ref()
                    .ok_or_else(|| "missing D3D11 device for DXGI readback".to_string())?;
                let ctx = self
                    .d3d_context
                    .as_ref()
                    .ok_or_else(|| "missing D3D11 context for DXGI readback".to_string())?;

                let need_new_staging = match &self.staging {
                    Some((_, sw, sh)) => *sw != src_desc.Width || *sh != src_desc.Height,
                    None => true,
                };
                if need_new_staging {
                    let st_desc = D3D11_TEXTURE2D_DESC {
                        Width: src_desc.Width,
                        Height: src_desc.Height,
                        MipLevels: 1,
                        ArraySize: 1,
                        Format: DXGI_FORMAT_NV12,
                        SampleDesc: DXGI_SAMPLE_DESC {
                            Count: 1,
                            Quality: 0,
                        },
                        Usage: D3D11_USAGE_STAGING,
                        CPUAccessFlags: D3D11_CPU_ACCESS_READ.0 as u32,
                        ..Default::default()
                    };
                    let mut st_tex: Option<ID3D11Texture2D> = None;
                    device
                        .CreateTexture2D(&st_desc, None, Some(&mut st_tex))
                        .map_err(|e| format!("CreateTexture2D(staging NV12): {e}"))?;
                    self.staging = st_tex.map(|t| (t, src_desc.Width, src_desc.Height));
                }
                let (st_tex, st_w, st_h) = self.staging.as_ref().expect("created above").clone();

                ctx.CopySubresourceRegion(&st_tex, 0, 0, 0, 0, &src_tex, subresource, None);
                let mut mapped = D3D11_MAPPED_SUBRESOURCE::default();
                ctx.Map(&st_tex, 0, D3D11_MAP_READ, 0, Some(&mut mapped))
                    .map_err(|e| format!("D3D11 Map(staging NV12): {e}"))?;

                let pitch = mapped.RowPitch as isize;
                let w = self.visible_w.min(st_w);
                let h = self.visible_h.min(st_h);
                let frame = copy_nv12_from_ptr(
                    mapped.pData.cast::<u8>(),
                    pitch,
                    w,
                    h,
                    st_h,
                    pts_us,
                    decode_us,
                );
                ctx.Unmap(&st_tex, 0);
                return Ok(frame);
            }

            // CPU buffer path (`IMF2DBuffer` or linear `IMFMediaBuffer`)
            if let Ok(buf2d) = buffer.cast::<IMF2DBuffer>() {
                let mut scanline0 = std::ptr::null_mut();
                let mut pitch = 0i32;
                buf2d
                    .Lock2D(&mut scanline0, &mut pitch)
                    .map_err(|e| format!("Lock2D failed: {e}"))?;
                let w = self.visible_w.min(self.surface_w);
                let h = self.visible_h.min(self.surface_h);
                let frame = copy_nv12_from_ptr(
                    scanline0,
                    pitch as isize,
                    w,
                    h,
                    self.surface_h,
                    pts_us,
                    decode_us,
                );
                let _ = buf2d.Unlock2D();
                return Ok(frame);
            }

            let mut data_ptr = std::ptr::null_mut();
            let mut cur_len = 0u32;
            buffer
                .Lock(&mut data_ptr, None, Some(&mut cur_len))
                .map_err(|e| format!("output buffer Lock failed: {e}"))?;
            let stride = self.default_stride.max(self.surface_w);
            let w = self.visible_w.min(self.surface_w);
            let h = self.visible_h.min(self.surface_h);
            let uv_h = h.div_ceil(2);
            let needed =
                (stride as usize) * (self.surface_h as usize) + (stride as usize) * (uv_h as usize);
            let res = if (cur_len as usize) < needed {
                Err(format!(
                    "output NV12 buffer too small: len={cur_len} needed={needed} (surface={}x{} visible={w}x{h})",
                    self.surface_w, self.surface_h
                ))
            } else {
                Ok(copy_nv12_from_ptr(
                    data_ptr,
                    stride as isize,
                    w,
                    h,
                    self.surface_h,
                    pts_us,
                    decode_us,
                ))
            };
            let _ = buffer.Unlock();
            res
        }
    }
}

/// Copies packed Y and interleaved UV planes from an NV12 memory layout whose UV
/// plane begins `surface_h * pitch` bytes after `scanline0`.
///
/// # Safety
/// `scanline0` must remain readable across `[0..h * pitch)` and
/// `[surface_h * pitch..(surface_h + ceil(h/2)) * pitch)`.
unsafe fn copy_nv12_from_ptr(
    scanline0: *const u8,
    pitch: isize,
    w: u32,
    h: u32,
    surface_h: u32,
    pts_us: u64,
    decode_us: u32,
) -> CachedNv12Frame {
    let even_w = (w + 1) & !1;
    let uv_h = h.div_ceil(2);
    let mut y = vec![0u8; (even_w * h) as usize];
    let mut uv = vec![0u8; (even_w * uv_h) as usize];
    // SAFETY: Caller guarantees scanlines for Y and UV planes within `surface_h` bounds.
    unsafe {
        for row in 0..h as isize {
            let src_row = scanline0.offset(row * pitch);
            std::ptr::copy_nonoverlapping(
                src_row,
                y.as_mut_ptr().add(row as usize * even_w as usize),
                w as usize,
            );
        }
        let uv_start = scanline0.offset(surface_h as isize * pitch);
        for row in 0..uv_h as isize {
            let src_row = uv_start.offset(row * pitch);
            std::ptr::copy_nonoverlapping(
                src_row,
                uv.as_mut_ptr().add(row as usize * even_w as usize),
                even_w as usize,
            );
        }
    }
    CachedNv12Frame {
        width: w,
        height: h,
        y_stride: even_w,
        uv_stride: even_w,
        pts_us,
        decode_us,
        y,
        uv,
    }
}

impl Drop for MfVideoDecoder {
    fn drop(&mut self) {
        // SAFETY: Flushes transform and clears D3D11 manager before dropping COM interfaces.
        unsafe {
            let _ = self.transform.ProcessMessage(MFT_MESSAGE_COMMAND_FLUSH, 0);
            let _ = self
                .transform
                .ProcessMessage(MFT_MESSAGE_NOTIFY_END_OF_STREAM, 0);
            if self._dxgi_manager.is_some() {
                let _ = self
                    .transform
                    .ProcessMessage(MFT_MESSAGE_SET_D3D_MANAGER, 0);
            }
        }
    }
}

fn create_decoder(mf: &MfApi, subtype: GUID) -> Result<CreatedDecoder, String> {
    let input_info = MFT_REGISTER_TYPE_INFO {
        guidMajorType: MFMediaType_Video,
        guidSubtype: subtype,
    };
    let sync_flags = MFT_ENUM_FLAG_SYNCMFT | MFT_ENUM_FLAG_SORTANDFILTER;
    let fallback_flags = MFT_ENUM_FLAG_HARDWARE
        | MFT_ENUM_FLAG_SYNCMFT
        | MFT_ENUM_FLAG_ASYNCMFT
        | MFT_ENUM_FLAG_SORTANDFILTER;

    let mut activates: *mut *mut c_void = std::ptr::null_mut();
    let mut count = 0u32;
    // SAFETY: Enumerates synchronous/D3D11-aware decoder MFTs first, falling back to hardware/async MFTs.
    let mut hr = unsafe {
        (mf.mft_enum_ex)(
            MFT_CATEGORY_VIDEO_DECODER,
            sync_flags,
            &input_info,
            std::ptr::null(),
            &mut activates,
            &mut count,
        )
    };
    if hr.is_err() || activates.is_null() || count == 0 {
        // SAFETY: Retries with full MFT enumeration flags.
        hr = unsafe {
            (mf.mft_enum_ex)(
                MFT_CATEGORY_VIDEO_DECODER,
                fallback_flags,
                &input_info,
                std::ptr::null(),
                &mut activates,
                &mut count,
            )
        };
    }
    if hr.is_err() || activates.is_null() || count == 0 {
        return Err(format!("no Media Foundation decoder MFT found (hr={hr:?})"));
    }

    let mut chosen: Option<IMFTransform> = None;
    let mut last_err = String::new();
    // SAFETY: Iterates and owns `count` `IMFActivate` pointers returned by `MFTEnumEx`.
    unsafe {
        let mut candidates = Vec::with_capacity(count as usize);
        for i in 0..count as usize {
            let raw = *activates.add(i);
            if !raw.is_null() {
                candidates.push(IMFActivate::from_raw(raw));
            }
        }
        CoTaskMemFree(Some(activates.cast()));

        // Prefer D3D11-aware decoder MFT first, then any activatable MFT.
        for pass_aware in [true, false] {
            if chosen.is_some() {
                break;
            }
            for act in &candidates {
                match act.ActivateObject::<IMFTransform>() {
                    Ok(t) => {
                        let aware = t
                            .GetAttributes()
                            .and_then(|a| a.GetUINT32(&MF_SA_D3D11_AWARE))
                            .unwrap_or(0)
                            != 0;
                        if !pass_aware || aware {
                            chosen = Some(t);
                            break;
                        }
                        let _ = act.ShutdownObject();
                    }
                    Err(e) => {
                        last_err = e.to_string();
                    }
                }
            }
        }
    }

    let transform = chosen.ok_or_else(|| {
        if last_err.is_empty() {
            "unable to activate video decoder MFT".to_string()
        } else {
            format!("unable to activate video decoder MFT: {last_err}")
        }
    })?;

    // SAFETY: Configures low-latency decode and binds a D3D11 DXGI Device Manager when supported.
    let (d3d_device, d3d_context, dxgi_manager, hardware) = unsafe {
        if let Ok(attrs) = transform.GetAttributes() {
            let _ = attrs.SetUINT32(&MF_LOW_LATENCY, 1);
            let d3d11_aware = attrs.GetUINT32(&MF_SA_D3D11_AWARE).unwrap_or(0) != 0;
            if d3d11_aware && let Some(create_mgr) = mf.create_dxgi_manager {
                let mut dev: Option<ID3D11Device> = None;
                let mut ctx: Option<ID3D11DeviceContext> = None;
                let levels = [D3D_FEATURE_LEVEL_11_1, D3D_FEATURE_LEVEL_11_0];
                if D3D11CreateDevice(
                    None,
                    D3D_DRIVER_TYPE_HARDWARE,
                    HMODULE::default(),
                    D3D11_CREATE_DEVICE_BGRA_SUPPORT | D3D11_CREATE_DEVICE_VIDEO_SUPPORT,
                    Some(&levels),
                    D3D11_SDK_VERSION,
                    Some(&mut dev),
                    None,
                    Some(&mut ctx),
                )
                .is_ok()
                    && let (Some(d), Some(c)) = (dev, ctx)
                {
                    let mut token = 0u32;
                    let mut raw_mgr: *mut c_void = std::ptr::null_mut();
                    if create_mgr(&mut token, &mut raw_mgr) == S_OK && !raw_mgr.is_null() {
                        let mgr = IMFDXGIDeviceManager::from_raw(raw_mgr);
                        if mgr.ResetDevice(&d, token).is_ok()
                            && transform
                                .ProcessMessage(MFT_MESSAGE_SET_D3D_MANAGER, mgr.as_raw() as usize)
                                .is_ok()
                        {
                            return Ok((transform, Some(d), Some(c), Some(mgr), true));
                        }
                    }
                }
            }
        }
        (None, None, None, false)
    };
    let _ = E_NOINTERFACE;
    Ok((transform, d3d_device, d3d_context, dxgi_manager, hardware))
}

fn pack_u32(hi: u32, lo: u32) -> u64 {
    (u64::from(hi) << 32) | u64::from(lo)
}

fn unpack_u32(v: u64) -> (u32, u32) {
    ((v >> 32) as u32, (v & 0xffff_ffff) as u32)
}
