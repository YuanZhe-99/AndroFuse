// Command buildapk builds the optional Android Companion APK (arm64-v8a) with MyGo, verifies
// that it carries the Go native library, and checks 16 KB page alignment
// (`zipalign -c -P 16 4`).
//
// On Windows it supplies the `gradlew.bat` that upstream mygo v0.10.0 does not ship, so MyGo's own
// Gradle call runs with every project property it sets (manifest, native libraries, assets).
package main

import (
	"archive/zip"
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

// gradlewBat is a minimal Windows Gradle wrapper launcher equivalent to the Unix `gradlew`
// that MyGo writes into .mygo/android.
// Notes: MyGo passes its project properties (manifest, jniLibs, assets, application id) as
// ORG_GRADLE_PROJECT_* environment variables, which Gradle on Windows was verified to ignore
// (2026-11-25), so the launcher forwards each one as a `-P` argument. Keystore passwords are
// only set for release signing, which local builds do not use.
const gradlewBat = "@echo off\r\n" +
	"setlocal EnableDelayedExpansion\r\n" +
	"set DIRNAME=%~dp0\r\n" +
	"if defined JAVA_HOME (set \"JAVA_EXE=%JAVA_HOME%\\bin\\java.exe\") else (set JAVA_EXE=java.exe)\r\n" +
	"set PROPS=\r\n" +
	"for /f \"tokens=1* delims==\" %%A in ('set ORG_GRADLE_PROJECT_ 2^>nul') do (\r\n" +
	"  set \"NAME=%%A\"\r\n" +
	"  set PROPS=!PROPS! \"-P!NAME:~19!=%%B\"\r\n" +
	")\r\n" +
	"\"%JAVA_EXE%\" -Xmx2048m \"-Dorg.gradle.appname=gradlew\" -classpath \"%DIRNAME%gradle\\wrapper\\gradle-wrapper.jar\" org.gradle.wrapper.GradleWrapperMain !PROPS! %*\r\n" +
	"exit /b %ERRORLEVEL%\r\n"

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
	if !isJDK17(os.Getenv("JAVA_HOME")) {
		// Android Gradle asks for a JDK 17 toolchain; the JVM Gradle runs on satisfies it.
		if jdk := findJDK17(); jdk != "" {
			env = append(env, "JAVA_HOME="+jdk)
		}
	}

	if err := ensureGradlewBat(); err != nil {
		return err
	}
	out, err := mygoBuild(env)
	if err != nil && runtime.GOOS == "windows" && strings.Contains(out, "gradlew.bat") {
		// First build on a clean tree: MyGo only now wrote .mygo/android, so add the
		// launcher and build again.
		if err := ensureGradlewBat(); err != nil {
			return err
		}
		_, err = mygoBuild(env)
	}
	if err != nil {
		return fmt.Errorf("mygo build android/arm64: %w", err)
	}

	srcAPK, err := locateBuiltAPK()
	if err != nil {
		return err
	}
	if err := requireNativeLibs(srcAPK); err != nil {
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

// mygoBuild runs `go tool mygo build -platform android/arm64 .`, echoing and returning its output.
func mygoBuild(env []string) (string, error) {
	cmd := exec.Command("go", "tool", "mygo", "build", "-platform", "android/arm64", ".")
	cmd.Env = env
	b, err := cmd.CombinedOutput()
	fmt.Print(string(b))
	return string(b), err
}

// ensureGradlewBat writes .mygo/android/gradlew.bat on Windows once MyGo has generated the
// Android project; elsewhere, or before the project exists, it does nothing.
func ensureGradlewBat() error {
	if runtime.GOOS != "windows" {
		return nil
	}
	dir := filepath.Join(".mygo", "android")
	if _, err := os.Stat(filepath.Join(dir, "gradle", "wrapper", "gradle-wrapper.jar")); err != nil {
		return nil
	}
	return os.WriteFile(filepath.Join(dir, "gradlew.bat"), []byte(gradlewBat), 0o755)
}

// isJDK17 reports whether home is a JDK 17, from its `release` file.
func isJDK17(home string) bool {
	b, err := os.ReadFile(filepath.Join(strings.TrimSpace(home), "release"))
	return err == nil && strings.Contains(string(b), `JAVA_VERSION="17`)
}

// findJDK17 returns an installed JDK 17 home (Android Gradle's toolchain), or "".
func findJDK17() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	for _, root := range []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft"),
		filepath.Join(os.Getenv("ProgramFiles"), "Eclipse Adoptium"),
		filepath.Join(os.Getenv("ProgramFiles"), "Java"),
		filepath.Join(os.Getenv("ProgramFiles"), "Zulu"),
	} {
		matches, _ := filepath.Glob(filepath.Join(root, "jdk-17*"))
		sort.Strings(matches)
		for i := len(matches) - 1; i >= 0; i-- {
			if _, err := os.Stat(filepath.Join(matches[i], "bin", "java.exe")); err == nil {
				return matches[i]
			}
		}
	}
	return ""
}

// requireNativeLibs fails unless apk contains arm64-v8a native libraries: a Gradle run without
// MyGo's project properties produces an APK without the Go app inside.
func requireNativeLibs(apk string) error {
	r, err := zip.OpenReader(apk)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "lib/arm64-v8a/") && strings.HasSuffix(f.Name, ".so") {
			return nil
		}
	}
	return fmt.Errorf("%s has no lib/arm64-v8a/*.so: the Go app was not packaged", apk)
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
