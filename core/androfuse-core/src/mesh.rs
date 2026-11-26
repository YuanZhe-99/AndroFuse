//! Embedded EasyTier user-space mesh (PLAN §9.1-1), shared by the Windows desktop core and the
//! Android Companion core.
//!
//! The mesh always runs with `no_tun = true`: it never installs a Windows TAP/TUN adapter or takes
//! Android's `VpnService`; remote services are reached through loopback port forwards only
//! (`INV-07`). It is off until the caller sends `mesh.start`, and secrets never leave this module
//! except inside the TOML handed to EasyTier (`INV-08`).
//!
//! EasyTier is linked through upstream's `easytier-ffi` crate (LGPL-3.0, pinned in Cargo.toml)
//! without its `c-abi` feature, so its generic symbols are not exported from `androfuse_core`.

use serde::{Deserialize, Serialize};
use std::ffi::{CStr, CString, c_char};
use std::net::{IpAddr, SocketAddr};
use std::sync::Mutex;
use std::sync::atomic::{AtomicBool, Ordering};

/// EasyTier instance name used for the single AndroFuse mesh.
const INSTANCE_NAME: &str = "androfuse";

/// Serializes start/stop so two callers never race on the single named instance.
static LIFECYCLE: Mutex<()> = Mutex::new(());

/// Set once the mesh has been started, so stop/status never spin up EasyTier's runtime otherwise.
static STARTED: AtomicBool = AtomicBool::new(false);

/// One loopback TCP port forwarded across the mesh (`127.0.0.1:local_port` → `remote_host:remote_port`).
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
pub struct PortForward {
    pub local_port: u16,
    pub remote_host: String,
    pub remote_port: u16,
}

/// `mesh.start` parameters.
#[derive(Debug, Clone, Default, Deserialize)]
pub struct MeshConfig {
    pub network_name: String,
    #[serde(default)]
    pub network_secret: String,
    #[serde(default)]
    pub device_name: String,
    #[serde(default)]
    pub peers: Vec<String>,
    /// Addresses this node accepts connections on (`tcp://0.0.0.0:<random high port>`); empty by default,
    /// so the node only dials out (`INV-07`).
    #[serde(default)]
    pub listeners: Vec<String>,
    #[serde(default)]
    pub port_forwards: Vec<PortForward>,
}

/// `mesh.status` / `mesh.start` result.
#[derive(Debug, Clone, Default, Serialize)]
pub struct MeshStatus {
    pub running: bool,
    pub engine: &'static str,
    /// Raw EasyTier running-info JSON for the instance; may contain peer addresses, so callers
    /// must scrub it before logging (`INV-08`).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub info: Option<serde_json::Value>,
    /// EasyTier's own error for the instance (for example a listener port already in use); the
    /// instance is not `running` while it is set.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
}

/// Engine label reported in status so the UI can name the embedded EasyTier build.
pub const ENGINE: &str = "easytier 2.7.1 (embedded, no TUN)";

/// Builds the EasyTier TOML config for cfg.
///
/// Returns: the TOML text, or a user-facing validation error.
/// Notes: always sets `flags.no_tun = true` and binds forwards to `127.0.0.1` only (`INV-07`).
/// Strings are quoted with JSON escaping, which is valid TOML basic-string syntax.
pub fn build_toml(cfg: &MeshConfig) -> Result<String, String> {
    let name = cfg.network_name.trim();
    if name.is_empty() {
        return Err("network_name is required".into());
    }
    let q = |s: &str| serde_json::to_string(s).unwrap_or_else(|_| "\"\"".into());

    let mut t = String::new();
    t.push_str(&format!("instance_name = {}\n", q(INSTANCE_NAME)));
    if !cfg.device_name.trim().is_empty() {
        t.push_str(&format!("hostname = {}\n", q(cfg.device_name.trim())));
    }
    let mut listeners = Vec::new();
    for l in cfg
        .listeners
        .iter()
        .map(|l| l.trim())
        .filter(|l| !l.is_empty())
    {
        if !l.contains("://") {
            return Err("listener must be a URI such as tcp://0.0.0.0:34567".into());
        }
        listeners.push(q(l));
    }
    t.push_str(&format!(
        "dhcp = true\nlisteners = [{}]\n\n[network_identity]\n",
        listeners.join(", ")
    ));
    t.push_str(&format!("network_name = {}\n", q(name)));
    t.push_str(&format!(
        "network_secret = {}\n",
        q(cfg.network_secret.trim())
    ));
    t.push_str("\n[flags]\nno_tun = true\n");

    for peer in cfg.peers.iter().map(|p| p.trim()).filter(|p| !p.is_empty()) {
        if !peer.contains("://") {
            return Err("peer must be a URI such as tcp://host:11010".into());
        }
        t.push_str(&format!("\n[[peer]]\nuri = {}\n", q(peer)));
    }
    for pf in &cfg.port_forwards {
        if pf.local_port == 0 || pf.remote_port == 0 {
            return Err(format!(
                "invalid port forward {}→{}",
                pf.local_port, pf.remote_port
            ));
        }
        let ip: IpAddr = pf
            .remote_host
            .trim()
            .parse()
            .map_err(|_| "port forward remote_host must be a mesh IP address".to_string())?;
        let dst = SocketAddr::new(ip, pf.remote_port);
        t.push_str(&format!(
            "\n[[port_forward]]\nbind_addr = {}\ndst_addr = {}\nproto = \"tcp\"\n",
            q(&format!("127.0.0.1:{}", pf.local_port)),
            q(&dst.to_string()),
        ));
    }
    Ok(t)
}

/// Starts (or restarts) the embedded mesh with cfg.
///
/// Side effects: replaces any running `androfuse` instance; opens outbound peer connections and
/// loopback listeners for the configured forwards.
pub fn start(cfg: &MeshConfig) -> Result<MeshStatus, String> {
    let toml = build_toml(cfg)?;
    let _guard = LIFECYCLE.lock().unwrap_or_else(|p| p.into_inner());
    if STARTED.load(Ordering::SeqCst) {
        delete_instance()?;
    }
    let c = CString::new(toml).map_err(|_| "config contains a NUL byte".to_string())?;
    STARTED.store(true, Ordering::SeqCst);
    // SAFETY: `c` is a valid NUL-terminated string that outlives the call; easytier-ffi copies it.
    if unsafe { easytier_ffi::run_network_instance(c.as_ptr()) } != 0 {
        return Err(last_error("start mesh"));
    }
    Ok(status())
}

/// Stops the embedded mesh; a no-op when it is not running.
pub fn stop() -> Result<(), String> {
    if !STARTED.load(Ordering::SeqCst) {
        return Ok(());
    }
    let _guard = LIFECYCLE.lock().unwrap_or_else(|p| p.into_inner());
    delete_instance()
}

/// Reports whether the mesh instance is running, with EasyTier's running info when it is.
pub fn status() -> MeshStatus {
    if !STARTED.load(Ordering::SeqCst) {
        return MeshStatus {
            engine: ENGINE,
            ..MeshStatus::default()
        };
    }
    let mut infos = [easytier_ffi::KeyValuePair {
        key: std::ptr::null(),
        value: std::ptr::null(),
    }; 4];
    // SAFETY: `infos` is writable storage for `infos.len()` pairs; easytier-ffi allocates each
    // written key/value, and every one is released below with `free_string`.
    let n = unsafe { easytier_ffi::collect_network_infos(infos.as_mut_ptr(), infos.len()) };
    let mut out = MeshStatus {
        engine: ENGINE,
        ..MeshStatus::default()
    };
    for kv in infos.iter().take(n.max(0) as usize) {
        let key = take_string(kv.key);
        let value = take_string(kv.value);
        if key == INSTANCE_NAME {
            out.running = true;
            out.info = serde_json::from_str(&value).ok();
            out.error = out
                .info
                .as_ref()
                .and_then(|i| i.get("error_msg"))
                .and_then(|e| e.as_str())
                .filter(|e| !e.is_empty())
                .map(str::to_owned);
            out.running = out.error.is_none();
        }
    }
    out
}

/// Deletes the named instance; succeeds when none exists.
fn delete_instance() -> Result<(), String> {
    let name = CString::new(INSTANCE_NAME).map_err(|e| e.to_string())?;
    let names = [name.as_ptr()];
    // SAFETY: `names` holds one valid NUL-terminated string pointer that outlives the call.
    if unsafe { easytier_ffi::delete_network_instance(names.as_ptr(), names.len()) } != 0 {
        return Err(last_error("stop mesh"));
    }
    Ok(())
}

/// Copies an easytier-ffi string and releases it; null yields "".
fn take_string(p: *const c_char) -> String {
    if p.is_null() {
        return String::new();
    }
    // SAFETY: easytier-ffi returned `p` as a live NUL-terminated allocation owned by the caller.
    let s = unsafe { CStr::from_ptr(p) }.to_string_lossy().into_owned();
    easytier_ffi::free_string(p);
    s
}

/// Reads the thread-local easytier-ffi error message for the call that just failed.
fn last_error(what: &str) -> String {
    let mut p: *const c_char = std::ptr::null();
    // SAFETY: `p` is valid writable storage for one string pointer; the result is freed by take_string.
    unsafe { easytier_ffi::get_error_msg(&mut p) };
    let msg = take_string(p);
    if msg.is_empty() {
        format!("{what} failed")
    } else {
        format!("{what}: {msg}")
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn build_toml_is_no_tun_loopback_only_and_escapes_secrets() {
        let cfg = MeshConfig {
            network_name: "my-devices".into(),
            network_secret: "p\"w\\d".into(),
            device_name: "Fold 8".into(),
            peers: vec!["tcp://relay.example.org:11010".into()],
            listeners: vec![],
            port_forwards: vec![PortForward {
                local_port: 41235,
                remote_host: "10.126.126.2".into(),
                remote_port: 37000,
            }],
        };
        let t = build_toml(&cfg).unwrap();
        assert!(t.contains("[flags]\nno_tun = true"));
        assert!(t.contains("network_secret = \"p\\\"w\\\\d\""));
        assert!(t.contains("bind_addr = \"127.0.0.1:41235\""));
        assert!(t.contains("dst_addr = \"10.126.126.2:37000\""));
        assert!(!t.contains("0.0.0.0"));
        assert!(t.contains("listeners = []"));
        assert!(t.contains("listeners = []"));
        // EasyTier itself must accept the generated config.
        let c = CString::new(t).unwrap();
        // SAFETY: `c` is a valid NUL-terminated string for the duration of the call.
        let rc = unsafe { easytier_ffi::parse_config(c.as_ptr()) };
        assert_eq!(rc, 0, "{}", last_error("parse"));
    }

    #[test]
    fn build_toml_rejects_bad_input() {
        assert!(build_toml(&MeshConfig::default()).is_err());
        let mut cfg = MeshConfig {
            network_name: "n".into(),
            ..MeshConfig::default()
        };
        cfg.peers = vec!["relay.example.org".into()];
        assert!(build_toml(&cfg).is_err());
        cfg.peers.clear();
        cfg.port_forwards = vec![PortForward {
            local_port: 1,
            remote_host: "phone.local".into(),
            remote_port: 2,
        }];
        assert!(build_toml(&cfg).is_err());
    }

    #[test]
    fn status_is_stopped_by_default_and_stop_is_idempotent() {
        assert!(stop().is_ok());
        let st = status();
        assert!(!st.running);
        assert_eq!(st.engine, ENGINE);
    }
}
