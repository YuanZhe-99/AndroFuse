// Command builddex compiles the clean-room Android app_process helper under
// server/dexhelper/src into build/androfuse-dex-helper.jar using javac and
// the Android SDK d8 dexer.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// main locates JDK javac and the newest Android SDK android.jar + d8 tool,
// compiles server/dexhelper/src with --release 11, and packages build/androfuse-dex-helper.jar.
//
// Side effects: Writes intermediate classes into build/dexhelper-classes/ and creates
// build/androfuse-dex-helper.jar (both gitignored).
func main() {
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sdkDir, err := findAndroidSDK()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	androidJar, err := newestAndroidJar(sdkDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	d8Tool, err := newestD8(sdkDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	javacTool, err := findJavac()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	srcRoot := filepath.Join(root, "server", "dexhelper", "src")
	var sources []string
	err = filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".java") {
			sources = append(sources, path)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(sources) == 0 {
		fmt.Fprintf(os.Stderr, "no Java sources found under %s\n", srcRoot)
		os.Exit(1)
	}

	buildDir := filepath.Join(root, "build")
	classesDir := filepath.Join(buildDir, "dexhelper-classes")
	_ = os.RemoveAll(classesDir)
	if err := os.MkdirAll(classesDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	javacArgs := []string{
		"--release", "11",
		"-Xlint:unchecked",
		"-classpath", androidJar,
		"-d", classesDir,
	}
	javacArgs = append(javacArgs, sources...)
	if err := runTool(javacTool, javacArgs...); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var classFiles []string
	err = filepath.WalkDir(classesDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".class") {
			classFiles = append(classFiles, path)
		}
		return nil
	})
	if err != nil || len(classFiles) == 0 {
		fmt.Fprintf(os.Stderr, "failed to collect compiled class files: %v\n", err)
		os.Exit(1)
	}

	outJar := filepath.Join(buildDir, "androfuse-dex-helper.jar")
	_ = os.Remove(outJar)
	d8Args := []string{
		"--min-api", "30",
		"--lib", androidJar,
		"--output", outJar,
	}
	d8Args = append(d8Args, classFiles...)
	if err := runTool(d8Tool, d8Args...); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	info, err := os.Stat(outJar)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("built: %s (%d bytes)\n", outJar, info.Size())
}

func runTool(bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", filepath.Base(bin), strings.Join(args[:min(4, len(args))], " "), err)
	}
	return nil
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above working directory")
		}
		dir = parent
	}
}

func findAndroidSDK() (string, error) {
	var candidates []string
	for _, env := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if v := os.Getenv(env); v != "" {
			candidates = append(candidates, v)
		}
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		candidates = append(candidates, filepath.Join(local, "Android", "Sdk"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, "AppData", "Local", "Android", "Sdk"),
			filepath.Join(home, "Android", "Sdk"),
		)
	}
	for _, dir := range candidates {
		if st, err := os.Stat(filepath.Join(dir, "platforms")); err == nil && st.IsDir() {
			return dir, nil
		}
	}
	return "", fmt.Errorf("Android SDK not found (set ANDROID_HOME or ANDROID_SDK_ROOT)")
}

func newestAndroidJar(sdk string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(sdk, "platforms"))
	if err != nil {
		return "", err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			p := filepath.Join(sdk, "platforms", e.Name(), "android.jar")
			if _, err := os.Stat(p); err == nil {
				dirs = append(dirs, p)
			}
		}
	}
	if len(dirs) == 0 {
		return "", fmt.Errorf("no android.jar under %s/platforms", sdk)
	}
	sort.Strings(dirs)
	return dirs[len(dirs)-1], nil
}

func newestD8(sdk string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(sdk, "build-tools"))
	if err != nil {
		return "", err
	}
	name := "d8"
	if runtime.GOOS == "windows" {
		name = "d8.bat"
	}
	var bins []string
	for _, e := range entries {
		if e.IsDir() {
			p := filepath.Join(sdk, "build-tools", e.Name(), name)
			if _, err := os.Stat(p); err == nil {
				bins = append(bins, p)
			}
		}
	}
	if len(bins) == 0 {
		return "", fmt.Errorf("no %s under %s/build-tools", name, sdk)
	}
	sort.Strings(bins)
	return bins[len(bins)-1], nil
}

func findJavac() (string, error) {
	name := "javac"
	if runtime.GOOS == "windows" {
		name = "javac.exe"
	}
	if jh := os.Getenv("JAVA_HOME"); jh != "" {
		p := filepath.Join(jh, "bin", name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return exec.LookPath(name)
}
