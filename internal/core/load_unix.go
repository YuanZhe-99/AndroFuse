//go:build darwin || linux || android

package core

import "github.com/ebitengine/purego"

func open(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
}

func symbol(h uintptr, name string) (uintptr, error) {
	return purego.Dlsym(h, name)
}

func syscallN(fn uintptr, args ...uintptr) uintptr {
	r, _, _ := purego.SyscallN(fn, args...)
	return r
}
