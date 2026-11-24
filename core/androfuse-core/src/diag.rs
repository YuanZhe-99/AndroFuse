//! Host environment diagnostics: native architecture / WOW64 emulation check,
//! Windows Media Foundation hardware H.264/HEVC decoder probe, and Wi-Fi Direct
//! / Miracast symbol availability probe.

use serde::Serialize;

/// SelfCheck describes real, measured capabilities of the current host.
#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct SelfCheck {
    pub os: &'static str,
    pub process_arch: &'static str,
    pub native_arch: String,
    pub is_emulated: bool,
    pub os_build: u32,
    pub media_foundation_present: bool,
    pub h264_hw_decoder: bool,
    pub h264_any_decoder: bool,
    pub hevc_hw_decoder: bool,
    pub hevc_any_decoder: bool,
    pub wifi_direct_api: bool,
    pub miracast_ie_provider: bool,
    pub notes: Vec<String>,
}

/// Probes host architecture, video decoders, and wireless display symbols.
///
/// Notes: Passive inspection only; never opens a Wi-Fi Direct session or
/// modifies system state.
pub fn run_self_check() -> SelfCheck {
    #[cfg(windows)]
    {
        windows_impl::run()
    }
    #[cfg(not(windows))]
    {
        let mut notes = Vec::new();
        if cfg!(target_os = "android") {
            notes.push("Android controller runtime uses NDK AMediaCodec".to_string());
        } else {
            notes.push("Desktop host diagnostics target Windows 11 x64/ARM64".to_string());
        }
        SelfCheck {
            os: std::env::consts::OS,
            process_arch: canonical_arch(std::env::consts::ARCH),
            native_arch: canonical_arch(std::env::consts::ARCH).to_string(),
            is_emulated: false,
            os_build: 0,
            media_foundation_present: false,
            h264_hw_decoder: false,
            h264_any_decoder: false,
            hevc_hw_decoder: false,
            hevc_any_decoder: false,
            wifi_direct_api: false,
            miracast_ie_provider: false,
            notes,
        }
    }
}

fn canonical_arch(arch: &str) -> &'static str {
    match arch {
        "aarch64" => "arm64",
        "x86_64" => "x64",
        "x86" => "x86",
        "arm" => "arm",
        _ => "unknown",
    }
}

#[cfg(windows)]
mod windows_impl {
    use super::{SelfCheck, canonical_arch};
    use std::ffi::c_void;
    use std::mem::size_of;
    use windows_sys::Win32::Foundation::{FreeLibrary, HMODULE};
    use windows_sys::Win32::System::Com::{
        COINIT_MULTITHREADED, CoInitializeEx, CoTaskMemFree, CoUninitialize,
    };
    use windows_sys::Win32::System::LibraryLoader::{
        GetProcAddress, LOAD_LIBRARY_SEARCH_SYSTEM32, LoadLibraryExW,
    };
    use windows_sys::Win32::System::SystemInformation::{
        IMAGE_FILE_MACHINE, IMAGE_FILE_MACHINE_AMD64, IMAGE_FILE_MACHINE_ARM64,
        IMAGE_FILE_MACHINE_I386, IMAGE_FILE_MACHINE_UNKNOWN,
    };
    use windows_sys::Win32::System::Threading::{GetCurrentProcess, IsWow64Process2};
    use windows_sys::core::{GUID, HRESULT, PCSTR, PCWSTR};

    #[repr(C)]
    #[derive(Clone, Copy)]
    struct MftRegisterTypeInfo {
        guid_major_type: GUID,
        guid_subtype: GUID,
    }

    #[repr(C)]
    struct OsversionInfoExW {
        dw_os_version_info_size: u32,
        dw_major_version: u32,
        dw_minor_version: u32,
        dw_build_number: u32,
        dw_platform_id: u32,
        sz_csd_version: [u16; 128],
        w_service_pack_major: u16,
        w_service_pack_minor: u16,
        w_suite_mask: u16,
        w_product_type: u8,
        w_reserved: u8,
    }

    type RtlGetVersionFn = unsafe extern "system" fn(*mut OsversionInfoExW) -> i32;
    type MfStartupFn = unsafe extern "system" fn(u32, u32) -> HRESULT;
    type MfShutdownFn = unsafe extern "system" fn() -> HRESULT;
    type MftEnumExFn = unsafe extern "system" fn(
        GUID,
        u32,
        *const MftRegisterTypeInfo,
        *const MftRegisterTypeInfo,
        *mut *mut *mut c_void,
        *mut u32,
    ) -> HRESULT;

    const MF_VERSION: u32 = (0x0002 << 16) | 0x0070;
    const MFSTARTUP_NOSOCKET: u32 = 1;
    const MFT_ENUM_FLAG_SYNCMFT: u32 = 0x0000_0001;
    const MFT_ENUM_FLAG_ASYNCMFT: u32 = 0x0000_0002;
    const MFT_ENUM_FLAG_HARDWARE: u32 = 0x0000_0004;
    const MFT_ENUM_FLAG_SORTANDFILTER: u32 = 0x0000_0040;
    const RPC_E_CHANGED_MODE: HRESULT = 0x8001_0106_u32 as i32;

    const MFT_CATEGORY_VIDEO_DECODER: GUID = GUID {
        data1: 0xd6c0_2d4b,
        data2: 0x6833,
        data3: 0x45b4,
        data4: [0x97, 0x1a, 0x05, 0xa4, 0xb0, 0x4b, 0xab, 0x91],
    };
    const MF_MEDIA_TYPE_VIDEO: GUID = GUID {
        data1: 0x7364_6976,
        data2: 0x0000,
        data3: 0x0010,
        data4: [0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71],
    };
    const MF_VIDEO_FORMAT_H264: GUID = GUID {
        data1: 0x3436_3248,
        data2: 0x0000,
        data3: 0x0010,
        data4: [0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71],
    };
    const MF_VIDEO_FORMAT_HEVC: GUID = GUID {
        data1: 0x4356_4548,
        data2: 0x0000,
        data3: 0x0010,
        data4: [0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71],
    };

    pub(super) fn run() -> SelfCheck {
        let mut notes = Vec::new();
        let process_arch = canonical_arch(std::env::consts::ARCH);
        let (native_arch, is_emulated) = probe_arch(process_arch, &mut notes);
        let os_build = probe_os_build();
        if os_build > 0 && os_build < 22000 {
            notes.push(format!(
                "Windows 10 build {os_build} detected; Windows 11 (22000+) is primary"
            ));
        }

        let (
            media_foundation_present,
            h264_hw_decoder,
            h264_any_decoder,
            hevc_hw_decoder,
            hevc_any_decoder,
        ) = probe_media_foundation(&mut notes);
        let (wifi_direct_api, miracast_ie_provider) = probe_wlan(&mut notes);

        SelfCheck {
            os: "windows",
            process_arch,
            native_arch,
            is_emulated,
            os_build,
            media_foundation_present,
            h264_hw_decoder,
            h264_any_decoder,
            hevc_hw_decoder,
            hevc_any_decoder,
            wifi_direct_api,
            miracast_ie_provider,
            notes,
        }
    }

    fn wide_lit(s: &str) -> Vec<u16> {
        s.encode_utf16().chain(std::iter::once(0)).collect()
    }

    fn machine_name(m: IMAGE_FILE_MACHINE) -> &'static str {
        match m {
            IMAGE_FILE_MACHINE_ARM64 => "arm64",
            IMAGE_FILE_MACHINE_AMD64 => "x64",
            IMAGE_FILE_MACHINE_I386 => "x86",
            _ => "unknown",
        }
    }

    fn probe_arch(process_arch: &str, notes: &mut Vec<String>) -> (String, bool) {
        let mut process_machine: IMAGE_FILE_MACHINE = IMAGE_FILE_MACHINE_UNKNOWN;
        let mut native_machine: IMAGE_FILE_MACHINE = IMAGE_FILE_MACHINE_UNKNOWN;
        // SAFETY: Both pointers reference valid stack-allocated u16 values and
        // GetCurrentProcess() returns the current process pseudo-handle.
        let ok = unsafe {
            IsWow64Process2(
                GetCurrentProcess(),
                &mut process_machine,
                &mut native_machine,
            )
        };
        if ok == 0 {
            return (process_arch.to_string(), false);
        }
        let native = machine_name(native_machine);
        let emulated = process_machine != IMAGE_FILE_MACHINE_UNKNOWN || native != process_arch;
        if emulated {
            notes.push(format!(
                "Process ({process_arch}) is running emulated on native {native}; install native {native} build"
            ));
        }
        (native.to_string(), emulated)
    }

    fn probe_os_build() -> u32 {
        let ntdll = wide_lit("ntdll.dll");
        // SAFETY: `ntdll` is NUL-terminated UTF-16 and we only load from System32.
        let module =
            unsafe { LoadLibraryExW(ntdll.as_ptr(), 0 as _, LOAD_LIBRARY_SEARCH_SYSTEM32) };
        if module.is_null() {
            return 0;
        }
        // SAFETY: `RtlGetVersion` is queried by NUL-terminated ASCII name on a valid HMODULE.
        let proc = unsafe { GetProcAddress(module, c"RtlGetVersion".as_ptr().cast()) };
        let build = if let Some(f) = proc {
            // SAFETY: `RtlGetVersion` matches `RtlGetVersionFn` signature in ntdll.dll.
            let rtl_get_version: RtlGetVersionFn = unsafe { std::mem::transmute(f) };
            // SAFETY: Zero-initialized OSVERSIONINFOW-compatible struct with size field set.
            let mut info: OsversionInfoExW = unsafe { std::mem::zeroed() };
            info.dw_os_version_info_size = size_of::<OsversionInfoExW>() as u32;
            // SAFETY: `info` points to valid writable storage of `dw_os_version_info_size`.
            if unsafe { rtl_get_version(&mut info) } == 0 {
                info.dw_build_number
            } else {
                0
            }
        } else {
            0
        };
        // SAFETY: `module` was returned by `LoadLibraryExW` above and is non-null.
        unsafe { FreeLibrary(module) };
        build
    }

    fn load_system_dll(name: &str) -> HMODULE {
        let wide = wide_lit(name);
        // SAFETY: `wide` is NUL-terminated UTF-16; LOAD_LIBRARY_SEARCH_SYSTEM32 prevents DLL hijacking.
        unsafe { LoadLibraryExW(wide.as_ptr(), 0 as _, LOAD_LIBRARY_SEARCH_SYSTEM32) }
    }

    fn probe_media_foundation(notes: &mut Vec<String>) -> (bool, bool, bool, bool, bool) {
        let mfplat = load_system_dll("mfplat.dll");
        if mfplat.is_null() {
            notes.push("mfplat.dll unavailable (install Windows Media Feature Pack)".to_string());
            return (false, false, false, false, false);
        }

        // SAFETY: All function names are NUL-terminated ASCII literals queried from valid `mfplat`.
        let (startup_p, shutdown_p, enum_p) = unsafe {
            (
                GetProcAddress(mfplat, c"MFStartup".as_ptr().cast()),
                GetProcAddress(mfplat, c"MFShutdown".as_ptr().cast()),
                GetProcAddress(mfplat, c"MFTEnumEx".as_ptr().cast()),
            )
        };
        let (Some(startup_f), Some(shutdown_f), Some(enum_f)) = (startup_p, shutdown_p, enum_p)
        else {
            // SAFETY: `mfplat` is non-null and loaded by us.
            unsafe { FreeLibrary(mfplat) };
            notes.push("mfplat.dll missing MFStartup/MFTEnumEx exports".to_string());
            return (false, false, false, false, false);
        };

        // SAFETY: Function signatures match official Microsoft Media Foundation mfapi.h exports.
        let (mf_startup, mf_shutdown, mft_enum_ex) = unsafe {
            (
                std::mem::transmute::<unsafe extern "system" fn() -> isize, MfStartupFn>(startup_f),
                std::mem::transmute::<unsafe extern "system" fn() -> isize, MfShutdownFn>(
                    shutdown_f,
                ),
                std::mem::transmute::<unsafe extern "system" fn() -> isize, MftEnumExFn>(enum_f),
            )
        };

        // SAFETY: Initializing COM on this thread with standard multithreaded apartment.
        let co_hr = unsafe { CoInitializeEx(std::ptr::null(), COINIT_MULTITHREADED as u32) };
        let should_co_uninit = co_hr >= 0;
        if co_hr < 0 && co_hr != RPC_E_CHANGED_MODE {
            // SAFETY: `mfplat` is non-null and loaded by us.
            unsafe { FreeLibrary(mfplat) };
            notes.push(format!("CoInitializeEx failed (0x{co_hr:08x})"));
            return (true, false, false, false, false);
        }

        // SAFETY: Calling MFStartup with MF_VERSION and MFSTARTUP_NOSOCKET.
        let mf_hr = unsafe { mf_startup(MF_VERSION, MFSTARTUP_NOSOCKET) };
        if mf_hr < 0 {
            if should_co_uninit {
                // SAFETY: Matches successful `CoInitializeEx` on this thread.
                unsafe { CoUninitialize() };
            }
            // SAFETY: `mfplat` is non-null and loaded by us.
            unsafe { FreeLibrary(mfplat) };
            notes.push(format!("MFStartup failed (0x{mf_hr:08x})"));
            return (false, false, false, false, false);
        }

        let hw_flags = MFT_ENUM_FLAG_HARDWARE | MFT_ENUM_FLAG_SORTANDFILTER;
        let any_flags = MFT_ENUM_FLAG_SYNCMFT
            | MFT_ENUM_FLAG_ASYNCMFT
            | MFT_ENUM_FLAG_HARDWARE
            | MFT_ENUM_FLAG_SORTANDFILTER;

        let h264_hw = has_decoder(mft_enum_ex, MF_VIDEO_FORMAT_H264, hw_flags);
        let h264_any = has_decoder(mft_enum_ex, MF_VIDEO_FORMAT_H264, any_flags);
        let hevc_hw = has_decoder(mft_enum_ex, MF_VIDEO_FORMAT_HEVC, hw_flags);
        let hevc_any = has_decoder(mft_enum_ex, MF_VIDEO_FORMAT_HEVC, any_flags);

        // SAFETY: Pairs with successful `MFStartup`.
        unsafe { mf_shutdown() };
        if should_co_uninit {
            // SAFETY: Pairs with successful `CoInitializeEx`.
            unsafe { CoUninitialize() };
        }
        // SAFETY: `mfplat` is non-null and loaded by us.
        unsafe { FreeLibrary(mfplat) };
        (true, h264_hw, h264_any, hevc_hw, hevc_any)
    }

    fn has_decoder(mft_enum_ex: MftEnumExFn, subtype: GUID, flags: u32) -> bool {
        let input_type = MftRegisterTypeInfo {
            guid_major_type: MF_MEDIA_TYPE_VIDEO,
            guid_subtype: subtype,
        };
        let mut activates: *mut *mut c_void = std::ptr::null_mut();
        let mut count: u32 = 0;
        // SAFETY: `input_type`, `activates`, and `count` point to valid stack variables.
        let hr = unsafe {
            mft_enum_ex(
                MFT_CATEGORY_VIDEO_DECODER,
                flags,
                &input_type,
                std::ptr::null(),
                &mut activates,
                &mut count,
            )
        };
        if hr < 0 || activates.is_null() {
            return false;
        }
        // SAFETY: `MFTEnumEx` returns `count` COM `IMFActivate*` pointers in a `CoTaskMemAlloc` array.
        unsafe {
            for i in 0..count as usize {
                let obj = *activates.add(i);
                if !obj.is_null() {
                    release_com_unknown(obj);
                }
            }
            CoTaskMemFree(activates.cast());
        }
        count > 0
    }

    /// Releases a COM `IUnknown` pointer by invoking vtable slot 2 (`Release`).
    ///
    /// # Safety
    /// `obj` must be a valid non-null COM interface pointer inheriting `IUnknown`.
    unsafe fn release_com_unknown(obj: *mut c_void) {
        // SAFETY: Caller guarantees `obj` is a valid COM object whose first field is the vtable pointer.
        unsafe {
            let vtbl = *(obj.cast::<*const *const c_void>());
            let release_ptr = *vtbl.add(2);
            let release: unsafe extern "system" fn(*mut c_void) -> u32 =
                std::mem::transmute(release_ptr);
            release(obj);
        }
    }

    fn probe_wlan(notes: &mut Vec<String>) -> (bool, bool) {
        let wlan = load_system_dll("wlanapi.dll");
        if wlan.is_null() {
            notes.push("wlanapi.dll unavailable (Wi-Fi Direct / Miracast inactive)".to_string());
            return (false, false);
        }
        let has_symbol = |name: PCSTR| -> bool {
            // SAFETY: `wlan` is non-null and `name` is NUL-terminated ASCII.
            unsafe { GetProcAddress(wlan, name).is_some() }
        };
        let wfd = has_symbol(c"WFDOpenHandle".as_ptr().cast())
            && has_symbol(c"WFDCloseHandle".as_ptr().cast());
        let ie = has_symbol(c"WlanNotifyVsIeProviderInt".as_ptr().cast());
        if !ie {
            notes.push(
                "wlanapi.dll lacks WlanNotifyVsIeProviderInt; Miracast Sink IE advertisement unavailable"
                    .to_string(),
            );
        }
        // SAFETY: `wlan` was loaded by `LoadLibraryExW` above and is non-null.
        unsafe { FreeLibrary(wlan) };
        (wfd, ie)
    }

    #[allow(dead_code)]
    fn _unused_pcwstr(_: PCWSTR) {}
}

#[cfg(test)]
mod tests {
    use super::run_self_check;

    #[test]
    fn self_check_reports_architecture_and_os() {
        let check = run_self_check();
        assert!(!check.os.is_empty());
        assert!(!check.native_arch.is_empty());
        assert_ne!(check.process_arch, "unknown");
        if cfg!(windows) {
            assert_eq!(check.os, "windows");
            assert!(check.os_build > 0);
        }
    }
}
