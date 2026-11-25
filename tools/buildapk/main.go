// Command buildapk builds the optional Android Companion APK (arm64-v8a) via MyGo
// and works around missing Windows gradlew.bat in upstream mygo v0.10.0, verifying
// 16 KB page alignment (`zipalign -c -P 16 4`).
package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/YuanZhe-99/AndroFuse/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "buildapk:", err)
		os.Exit(1)
	}
}

func run() error {
	sdkRoot := findSDKRoot()
	ndkDir := findLatestDir(filepath.Join(sdkRoot, "ndk"))
	env := os.Environ()
	if sdkRoot != "" && os.Getenv("ANDROID_HOME") == "" {
		env = append(env, "ANDROID_HOME="+sdkRoot)
	}
	if ndkDir != "" && os.Getenv("ANDROID_NDK_HOME") == "" {
		env = append(env, "ANDROID_NDK_HOME="+ndkDir)
	}

	cmd := exec.Command("go", "tool", "mygo", "build", "-platform", "android/arm64", ".")
	cmd.Env = env
	outBytes, buildErr := cmd.CombinedOutput()
	outStr := string(outBytes)
	fmt.Print(outStr)

	if buildErr != nil {
		if runtime.GOOS == "windows" && strings.Contains(outStr, "gradlew.bat") {
			if err := runGradleWrapperFallback(env); err != nil {
				return fmt.Errorf("gradle wrapper fallback: %w", err)
			}
		} else {
			return fmt.Errorf("mygo build android/arm64: %w", buildErr)
		}
	}

	srcAPK, err := locateBuiltAPK()
	if err != nil {
		return err
	}
	outAPK := filepath.Join("build", fmt.Sprintf("AndroFuse-%s-android-arm64.apk", version.Version))
	if err := copyFile(srcAPK, outAPK); err != nil {
		return err
	}
	fmt.Printf("built APK: %s\n", outAPK)

	btDir := findLatestDir(filepath.Join(sdkRoot, "build-tools"))
	if btDir != "" {
		zipalign := filepath.Join(btDir, "zipalign")
		if runtime.GOOS == "windows" {
			zipalign += ".exe"
		}
		if _, err := os.Stat(zipalign); err == nil {
			vc := exec.Command(zipalign, "-c", "-P", "16", "4", outAPK)
			vc.Stdout = os.Stdout
			vc.Stderr = os.Stderr
			if err := vc.Run(); err != nil {
				return fmt.Errorf("zipalign 16KB check failed: %w", err)
			}
			fmt.Println("zipalign -c -P 16 4: ok")
		}
	}
	return nil
}

func runGradleWrapperFallback(env []string) error {
	androidDir := filepath.Join(".mygo", "android")
	wrapperJar := filepath.Join(androidDir, "gradle", "wrapper", "gradle-wrapper.jar")
	if _, err := os.Stat(wrapperJar); err != nil {
		return fmt.Errorf("missing %s", wrapperJar)
	}
	task := "assembleDebug"
	if os.Getenv("MYGO_ANDROID_KEYSTORE_PASSWORD") != "" {
		task = "assembleRelease"
	}
	relJar := filepath.Join("gradle", "wrapper", "gradle-wrapper.jar")
	gCmd := exec.Command("java", "-Xmx2048m", "-Dorg.gradle.appname=gradlew",
		"-classpath", relJar, "org.gradle.wrapper.GradleWrapperMain", task)
	gCmd.Dir = androidDir
	gCmd.Env = env
	gCmd.Stdout = os.Stdout
	gCmd.Stderr = os.Stderr
	return gCmd.Run()
}

func locateBuiltAPK() (string, error) {
	candidates := []string{
		filepath.Join("build", "android", fmt.Sprintf("androfuse-%s.apk", version.Version)),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	var found []string
	_ = filepath.WalkDir(filepath.Join(".mygo", "android", "app", "build", "outputs", "apk"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".apk") && !strings.Contains(strings.ToLower(d.Name()), "unsigned") {
			found = append(found, p)
		}
		return nil
	})
	if len(found) == 0 {
		return "", fmt.Errorf("no built APK found under build/android or .mygo/android")
	}
	sort.Strings(found)
	return found[len(found)-1], nil
}

func findSDKRoot() string {
	for _, k := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	if runtime.GOOS == "windows" {
		if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
			p := filepath.Join(local, "Android", "Sdk")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

func findLatestDir(parent string) string {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return ""
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return filepath.Join(parent, names[len(names)-1])
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
