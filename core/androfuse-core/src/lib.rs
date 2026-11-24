//! C-ABI core library for AndroFuse shared between the Windows desktop runtime
//! and the Android controller/companion runtime.

pub mod diag;

use serde::{Deserialize, Serialize};
use std::panic::{AssertUnwindSafe, catch_unwind};
use std::sync::{Mutex, OnceLock};

/// C ABI version spoken between Go (`internal/core`) and `androfuse-core`.
pub const ABI_VERSION: u32 = 1;

static STATE: OnceLock<Mutex<Option<CoreState>>> = OnceLock::new();

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
    #[allow(dead_code)]
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

fn state_lock() -> &'static Mutex<Option<CoreState>> {
    STATE.get_or_init(|| Mutex::new(None))
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

/// Stops the core and clears in-memory state.
#[unsafe(no_mangle)]
pub extern "C" fn androfuse_stop() {
    let mut guard = match state_lock().lock() {
        Ok(g) => g,
        Err(p) => p.into_inner(),
    };
    *guard = None;
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
}
