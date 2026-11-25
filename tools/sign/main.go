// Command sign manages the release Ed25519 key used to sign Windows helper bundles
// (`bundle.json` + `bundle.sig` in internal/bundlesig), matching Kyokai's format:
//
//	go run ./tools/sign keygen -out FILE
//	go run ./tools/sign bundle -dir DIR -version V
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/YuanZhe-99/AndroFuse/internal/bundlesig"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		fs := flag.NewFlagSet("keygen", flag.ExitOnError)
		out := fs.String("out", "", "where to write the private key")
		_ = fs.Parse(os.Args[2:])
		err = keygen(*out)
	case "bundle":
		fs := flag.NewFlagSet("bundle", flag.ExitOnError)
		dir := fs.String("dir", "", "the directory holding androfuse-svc.exe and core library")
		ver := fs.String("version", "", "the release version")
		_ = fs.Parse(os.Args[2:])
		err = bundle(*dir, *ver)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sign:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: go run ./tools/sign keygen -out FILE | bundle -dir DIR -version V")
	os.Exit(2)
}

func bundle(dir, version string) error {
	if dir == "" || version == "" {
		return errors.New("-dir and -version are required")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("ANDROFUSE_SIGNING_KEY")))
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return errors.New("ANDROFUSE_SIGNING_KEY does not hold a valid Ed25519 signing key")
	}
	return bundlesig.Sign(dir, version, ed25519.PrivateKey(raw))
}

func keygen(out string) error {
	if out == "" {
		return errors.New("-out is required")
	}
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(priv) + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
	return nil
}
