//go:build !windows

package gui

import "errors"

// errNoVault reports that secrets are not persisted on this platform.
var errNoVault = errors.New("secrets are only saved on Windows")

func sealSecret([]byte) ([]byte, error) { return nil, errNoVault }

func openSecret([]byte) ([]byte, error) { return nil, errNoVault }
