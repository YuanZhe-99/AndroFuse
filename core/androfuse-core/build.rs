//! Build script for androfuse-core.

/// Delay-loads `packet.dll` on Windows MSVC targets.
///
/// Notes: EasyTier's optional fake-TCP transport links the Npcap/WinPcap packet library, which
/// most PCs do not have. AndroFuse's mesh never enables that transport, so the import is
/// delay-loaded: `androfuse_core.dll` loads everywhere and the library is only resolved if that
/// code path were ever called (PLAN §9.1-1).
fn main() {
    println!("cargo:rerun-if-changed=build.rs");
    let target = std::env::var("TARGET").unwrap_or_default();
    if target.ends_with("-pc-windows-msvc") {
        println!("cargo:rustc-link-arg=/DELAYLOAD:packet.dll");
        println!("cargo:rustc-link-lib=delayimp");
    }
}
