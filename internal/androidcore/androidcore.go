// Package androidcore is a native module that carries the Rust core library
// (libandrofuse_core.so with the embedded EasyTier mesh, built by
// scripts/build-android-core.sh into kotlin/src/main/jniLibs/<abi>/) into the Android Companion
// APK and loads it, so internal/core can open it by its SONAME.
package androidcore

// Library loads the packaged core library.
//
//mygo:native
type Library interface {
	// Load loads libandrofuse_core.so into the app's linker namespace (System.loadLibrary);
	// afterwards core.Open("libandrofuse_core.so") resolves it. It fails when the APK was built
	// without the library.
	Load() error
}

// Vault seals small secrets (the mesh network secret) with an AES-GCM key that never leaves
// Android Keystore (`INV-08`).
//
//mygo:native
type Vault interface {
	// Seal encrypts plain; the result carries its own nonce.
	Seal(plain []byte) ([]byte, error)
	// Open decrypts what Seal returned on this device.
	Open(sealed []byte) ([]byte, error)
}
