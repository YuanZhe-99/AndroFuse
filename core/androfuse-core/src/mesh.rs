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

/// EasyTier instance name used when a caller names none (the Companion's single network).
const DEFAULT_INSTANCE: &str = "androfuse";

/// Most networks one process runs at once (one EasyTier instance each).
pub const MAX_INSTANCES: usize = 8;

/// Instances started and not deleted since. It also serializes start/stop so two callers never
/// race on one instance, and keeps stop/status from spinning up EasyTier's runtime before any start.
static STARTED: Mutex<Vec<String>> = Mutex::new(Vec::new());

/// Returns the instance name a request refers to: its own, or the default when empty.
/// Returns: an error for names EasyTier or a file name could not carry safely.
pub fn instance_name(name: &str) -> Result<String, String> {
    let n = name.trim();
    if n.is_empty() {
        return Ok(DEFAULT_INSTANCE.into());
    }
    if n.len() > 32
        || !n
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '_')
    {
        return Err("instance must be 1-32 letters, digits, '-' or '_'".into());
    }
    Ok(n.into())
}

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
    /// EasyTier instance to start or replace; empty means the default single instance. The desktop
    /// runs one instance per joined network.
    #[serde(default)]
    pub instance: String,
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
    /// Trades connection set-up speed for fewer background wakeups (phones on battery): one runtime
    /// thread, direct links built only when traffic needs them and no UPnP. Symmetric-NAT hole
    /// punching stays on: mobile carriers use symmetric NAT, so without it a phone on mobile data
    /// could only ever be reached through the network's servers.
    #[serde(default)]
    pub low_power: bool,
    /// Never builds direct links; all traffic goes through the network's servers (Settings ›
    /// Network › Try direct connections, off).
    #[serde(default)]
    pub disable_p2p: bool,
    /// Prefers the lowest-latency route over the fewest hops.
    #[serde(default)]
    pub latency_first: bool,
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

    let instance = instance_name(&cfg.instance)?;
    let mut t = String::new();
    t.push_str(&format!("instance_name = {}\n", q(&instance)));
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
    // TCP across the network (the computer's port forwards to a phone) rides KCP over UDP where both
    // ends allow it, which recovers from mobile-network loss far faster than TCP; EasyTier falls
    // back to TCP otherwise.
    t.push_str("\n[flags]\nno_tun = true\nenable_kcp_proxy = true\n");
    if cfg.low_power {
        t.push_str("multi_thread = false\nlazy_p2p = true\ndisable_upnp = true\n");
    }
    if cfg.disable_p2p {
        t.push_str("disable_p2p = true\n");
    }
    if cfg.latency_first {
        t.push_str("latency_first = true\n");
    }

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

/// Starts (or restarts) the mesh instance cfg names with cfg.
///
/// Side effects: replaces a running instance of the same name, leaving others alone; opens
/// outbound peer connections and loopback listeners for the configured forwards.
pub fn start(cfg: &MeshConfig) -> Result<MeshStatus, String> {
    let toml = build_toml(cfg)?;
    let instance = instance_name(&cfg.instance)?;
    {
        let mut started = STARTED.lock().unwrap_or_else(|p| p.into_inner());
        if started.contains(&instance) {
            delete_instance(&instance)?;
        } else if started.len() >= MAX_INSTANCES {
            return Err(format!("at most {MAX_INSTANCES} networks can run at once"));
        } else {
            started.push(instance.clone());
        }
        let c = CString::new(toml).map_err(|_| "config contains a NUL byte".to_string())?;
        // SAFETY: `c` is a valid NUL-terminated string that outlives the call; easytier-ffi copies it.
        if unsafe { easytier_ffi::run_network_instance(c.as_ptr()) } != 0 {
            let err = last_error("start mesh");
            started.retain(|n| n != &instance);
            return Err(err);
        }
    }
    Ok(status(&instance))
}

/// Stops the named mesh instance, or every instance when instance is None; stopping one that is
/// not running is not an error.
pub fn stop(instance: Option<&str>) -> Result<(), String> {
    let mut started = STARTED.lock().unwrap_or_else(|p| p.into_inner());
    let names: Vec<String> = match instance {
        Some(n) => {
            let n = instance_name(n)?;
            if started.contains(&n) {
                vec![n]
            } else {
                Vec::new()
            }
        }
        None => started.clone(),
    };
    for n in names {
        delete_instance(&n)?;
        started.retain(|s| s != &n);
    }
    Ok(())
}

/// Reports whether the named mesh instance is running, with EasyTier's running info when it is.
pub fn status(instance: &str) -> MeshStatus {
    let mut out = MeshStatus {
        engine: ENGINE,
        ..MeshStatus::default()
    };
    let Ok(instance) = instance_name(instance) else {
        return out;
    };
    if !STARTED
        .lock()
        .unwrap_or_else(|p| p.into_inner())
        .contains(&instance)
    {
        return out;
    }
    let mut infos = [easytier_ffi::KeyValuePair {
        key: std::ptr::null(),
        value: std::ptr::null(),
    }; MAX_INSTANCES * 2];
    // SAFETY: `infos` is writable storage for `infos.len()` pairs; easytier-ffi allocates each
    // written key/value, and every one is released below with `free_string`.
    let n = unsafe { easytier_ffi::collect_network_infos(infos.as_mut_ptr(), infos.len()) };
    for kv in infos.iter().take(n.max(0) as usize) {
        let key = take_string(kv.key);
        let value = take_string(kv.value);
        if key == instance {
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
fn delete_instance(instance: &str) -> Result<(), String> {
    let name = CString::new(instance).map_err(|e| e.to_string())?;
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

/// Serializes tests that stop every mesh instance (the C ABI's `androfuse_stop`) with tests that
/// run instances, since the test harness runs them on parallel threads of one process.
#[cfg(test)]
pub(crate) static TEST_INSTANCES: Mutex<()> = Mutex::new(());

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn build_toml_is_no_tun_loopback_only_and_escapes_secrets() {
        let cfg = MeshConfig {
            instance: String::new(),
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
            low_power: false,
            disable_p2p: true,
            latency_first: true,
        };
        let t = build_toml(&cfg).unwrap();
        assert!(t.contains("disable_p2p = true") && t.contains("latency_first = true"));
        assert!(t.contains("[flags]\nno_tun = true\nenable_kcp_proxy = true"));
        assert!(t.contains("network_secret = \"p\\\"w\\\\d\""));
        assert!(t.contains("bind_addr = \"127.0.0.1:41235\""));
        assert!(t.contains("dst_addr = \"10.126.126.2:37000\""));
        assert!(!t.contains("0.0.0.0"));
        assert!(t.contains("listeners = []"));
        // EasyTier itself must accept the generated config.
        let c = CString::new(t).unwrap();
        // SAFETY: `c` is a valid NUL-terminated string for the duration of the call.
        let rc = unsafe { easytier_ffi::parse_config(c.as_ptr()) };
        assert_eq!(rc, 0, "{}", last_error("parse"));

        let low = build_toml(&MeshConfig {
            low_power: true,
            ..cfg
        })
        .unwrap();
        assert!(low.contains("multi_thread = false\nlazy_p2p = true"));
        // A phone on mobile data sits behind symmetric NAT: punching through it must stay on.
        assert!(!low.contains("disable_sym_hole_punching"));
        let c = CString::new(low).unwrap();
        // SAFETY: `c` is a valid NUL-terminated string for the duration of the call.
        let rc = unsafe { easytier_ffi::parse_config(c.as_ptr()) };
        assert_eq!(rc, 0, "{}", last_error("parse low-power"));
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
        // Other tests in this binary run instances concurrently, so only unused names are touched.
        assert!(stop(Some("never-started")).is_ok());
        let st = status("never-started");
        assert!(!st.running);
        assert_eq!(st.engine, ENGINE);
        assert!(instance_name("bad name!").is_err());
        assert_eq!(instance_name("").unwrap(), DEFAULT_INSTANCE);
    }

    #[test]
    fn two_instances_run_side_by_side_and_see_each_other() {
        let _serial = TEST_INSTANCES.lock().unwrap_or_else(|p| p.into_inner());
        // EasyTier binds outbound peer sockets to physical interfaces, so the peer is dialled on
        // this machine's LAN address (found without sending anything), not 127.0.0.1.
        let lan = std::net::UdpSocket::bind("0.0.0.0:0")
            .and_then(|s| s.connect("192.0.2.1:9").map(|_| s))
            .and_then(|s| s.local_addr())
            .map(|a| a.ip())
            .unwrap();
        let port = std::net::TcpListener::bind("127.0.0.1:0")
            .and_then(|l| l.local_addr())
            .map(|a| a.port())
            .unwrap();
        let base = MeshConfig {
            network_name: "af-test-two".into(),
            network_secret: "two-instance-secret".into(),
            ..MeshConfig::default()
        };
        start(&MeshConfig {
            instance: "af-a".into(),
            device_name: "node-a".into(),
            listeners: vec![format!("tcp://0.0.0.0:{port}")],
            ..base.clone()
        })
        .unwrap();
        start(&MeshConfig {
            instance: "af-b".into(),
            device_name: "node-b".into(),
            peers: vec![format!("tcp://{lan}:{port}")],
            ..base
        })
        .unwrap();
        let mut seen = false;
        for _ in 0..40 {
            let st = status("af-b");
            let routes = st
                .info
                .as_ref()
                .and_then(|i| i.get("routes"))
                .and_then(|r| r.as_array())
                .cloned()
                .unwrap_or_default();
            if routes
                .iter()
                .any(|r| r.get("hostname").and_then(|h| h.as_str()) == Some("node-a"))
            {
                seen = true;
                break;
            }
            std::thread::sleep(std::time::Duration::from_millis(250));
        }
        if !seen {
            println!(
                "{}",
                serde_json::to_string_pretty(&status("af-b").info).unwrap_or_default()
            );
        }
        assert!(status("af-a").running);
        assert!(stop(Some("af-a")).is_ok());
        assert!(!status("af-a").running);
        assert!(stop(Some("af-b")).is_ok());
        assert!(seen, "node-b never saw node-a");
    }
}
