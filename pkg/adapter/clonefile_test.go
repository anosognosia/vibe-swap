package adapter

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestCloneFile_CopiesContent(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("CloneFile is darwin-specific (uses unix.Clonefile); fallback path is still tested")
	}
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.bin")
	dst := filepath.Join(tmp, "dst.bin")
	payload := bytes.Repeat([]byte("hello world\n"), 1024)
	if err := os.WriteFile(src, payload, 0644); err != nil {
		t.Fatal(err)
	}
	if err := CloneFile(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("cloned file content differs from source")
	}
}

func TestCloneFile_SourceAndCloneAreIndependent(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.bin")
	dst := filepath.Join(tmp, "dst.bin")
	if err := os.WriteFile(src, bytes.Repeat([]byte{0x00}, 64*1024), 0644); err != nil {
		t.Fatal(err)
	}
	if err := CloneFile(src, dst); err != nil {
		t.Fatal(err)
	}

	// Overwrite source; clone should be unchanged.
	if err := os.WriteFile(src, bytes.Repeat([]byte{0xAA}, 64*1024), 0644); err != nil {
		t.Fatal(err)
	}
	dstContent, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(dstContent, []byte{0xAA}) {
		t.Fatal("clone was affected by source modification (expected isolation)")
	}

	// Overwrite clone; source should be unchanged.
	if err := os.WriteFile(dst, bytes.Repeat([]byte{0xBB}, 64*1024), 0644); err != nil {
		t.Fatal(err)
	}
	srcContent, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(srcContent, []byte{0xBB}) {
		t.Fatal("source was affected by clone modification (expected isolation)")
	}
}

func TestCloneTree_PreservesContent(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	dst := filepath.Join(tmp, "dst")
	if err := os.MkdirAll(filepath.Join(src, "sub", "deeper"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "config.json"), []byte(`{"a":1}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "deeper", "deep.bin"), bytes.Repeat([]byte{0x7F}, 8192), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := CloneTree(src, dst, CloneTreeOptions{}); err != nil {
		t.Fatal(err)
	}

	// Verify content
	if got, err := os.ReadFile(filepath.Join(dst, "config.json")); err != nil {
		t.Fatal(err)
	} else if string(got) != `{"a":1}` {
		t.Fatalf("config.json: %q", got)
	}
	if got, err := os.ReadFile(filepath.Join(dst, "sub", "file.txt")); err != nil {
		t.Fatal(err)
	} else if string(got) != "hello" {
		t.Fatalf("sub/file.txt: %q", got)
	}
}

func TestCloneTree_PreservesSymlinks(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	dst := filepath.Join(tmp, "dst")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "target.txt"), []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(src, "relative-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/tmp/absolute-link", filepath.Join(src, "abs-link")); err != nil {
		t.Fatal(err)
	}

	if _, err := CloneTree(src, dst, CloneTreeOptions{}); err != nil {
		t.Fatal(err)
	}

	// Both symlinks should be recreated as symlinks, not as regular files.
	li, err := os.Lstat(filepath.Join(dst, "relative-link"))
	if err != nil {
		t.Fatal(err)
	}
	if li.Mode()&os.ModeSymlink == 0 {
		t.Fatal("relative-link was not preserved as a symlink")
	}
	if target, _ := os.Readlink(filepath.Join(dst, "relative-link")); target != "target.txt" {
		t.Fatalf("relative-link target: %q", target)
	}

	li, err = os.Lstat(filepath.Join(dst, "abs-link"))
	if err != nil {
		t.Fatal(err)
	}
	if li.Mode()&os.ModeSymlink == 0 {
		t.Fatal("abs-link was not preserved as a symlink")
	}
	if target, _ := os.Readlink(filepath.Join(dst, "abs-link")); target != "/tmp/absolute-link" {
		t.Fatalf("abs-link target: %q", target)
	}
}

func TestCloneTree_PreservesPermissions(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	dst := filepath.Join(tmp, "dst")
	if err := os.MkdirAll(src, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "secret"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "secret", "key"), []byte("k"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "ro.txt"), []byte("x"), 0444); err != nil {
		t.Fatal(err)
	}

	if _, err := CloneTree(src, dst, CloneTreeOptions{}); err != nil {
		t.Fatal(err)
	}

	if fi, err := os.Stat(filepath.Join(dst, "secret")); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0700 {
		t.Fatalf("secret dir perm: %o (want 0700)", fi.Mode().Perm())
	}
	if fi, err := os.Stat(filepath.Join(dst, "secret", "key")); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0600 {
		t.Fatalf("key file perm: %o (want 0600)", fi.Mode().Perm())
	}
	if fi, err := os.Stat(filepath.Join(dst, "ro.txt")); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0444 {
		t.Fatalf("ro.txt perm: %o (want 0444)", fi.Mode().Perm())
	}
}

func TestCloneTree_ProgressFires(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	dst := filepath.Join(tmp, "dst")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		_ = os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0644)
	}
	// 5 distinct files
	for i := 0; i < 5; i++ {
		_ = os.WriteFile(filepath.Join(src, "f_"+string(rune('a'+i))), []byte("xxxx"), 0644)
	}

	calls := 0
	_, err := CloneTree(src, dst, CloneTreeOptions{
		Progress: func(_ string, _, _ int64) { calls++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls < 5 {
		t.Fatalf("Progress called %d times, want >= 5", calls)
	}
}

func TestCloneTree_SkipNames(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	dst := filepath.Join(tmp, "dst")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(src, "keep.txt"), []byte("k"), 0644)
	_ = os.WriteFile(filepath.Join(src, "skip.log"), []byte("s"), 0644)
	if err := os.MkdirAll(filepath.Join(src, "skip-dir"), 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(src, "skip-dir", "inside.txt"), []byte("i"), 0644)

	_, err := CloneTree(src, dst, CloneTreeOptions{
		SkipNames: map[string]struct{}{"skip.log": {}, "skip-dir": {}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dst, "keep.txt")); err != nil {
		t.Fatal("keep.txt should exist")
	}
	if _, err := os.Stat(filepath.Join(dst, "skip.log")); !os.IsNotExist(err) {
		t.Fatalf("skip.log should be skipped, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "skip-dir")); !os.IsNotExist(err) {
		t.Fatalf("skip-dir should be skipped, got err=%v", err)
	}
}

func TestAtomicSymlinkSwap_ReplacesRealDirWithSymlink(t *testing.T) {
	tmp := t.TempDir()
	live := filepath.Join(tmp, "Live")
	profile := filepath.Join(tmp, "profile-A")
	if err := os.MkdirAll(live, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(profile, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "x"), []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "x"), []byte("profile-A"), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := AtomicSymlinkSwap(live, profile)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Replaced {
		t.Fatal("expected Replaced=true")
	}
	if res.BackedUpTo == "" {
		t.Fatal("expected BackedUpTo to be set when replacing a real dir")
	}

	li, err := os.Lstat(live)
	if err != nil {
		t.Fatal(err)
	}
	if li.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected live to be a symlink after swap")
	}
	if got, _ := os.Readlink(live); got != "profile-A" {
		t.Fatalf("symlink target: %q (want relative 'profile-A')", got)
	}
	got, err := os.ReadFile(filepath.Join(live, "x"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "profile-A" {
		t.Fatalf("reading through symlink: %q", got)
	}

	// The backed-up real dir should still be readable.
	if _, err := os.Stat(res.BackedUpTo); err != nil {
		t.Fatalf("backed-up dir not found at %s: %v", res.BackedUpTo, err)
	}
	if got, _ := os.ReadFile(filepath.Join(res.BackedUpTo, "x")); string(got) != "original" {
		t.Fatalf("backed-up content: %q", got)
	}
}

func TestAtomicSymlinkSwap_ReplacesExistingSymlink(t *testing.T) {
	tmp := t.TempDir()
	live := filepath.Join(tmp, "Live")
	profileA := filepath.Join(tmp, "profile-A")
	profileB := filepath.Join(tmp, "profile-B")
	for _, p := range []string{profileA, profileB} {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(profileA, live); err != nil {
		t.Fatal(err)
	}

	res, err := AtomicSymlinkSwap(live, profileB)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Replaced {
		t.Fatal("expected Replaced=true")
	}
	if res.BackedUpTo != "" {
		t.Fatalf("BackedUpTo should be empty when swapping a symlink, got %q", res.BackedUpTo)
	}
	target, _ := os.Readlink(live)
	if target != "profile-B" {
		t.Fatalf("symlink target: %q (want 'profile-B')", target)
	}
}

func TestAtomicSymlinkSwap_CreatesFromNothing(t *testing.T) {
	tmp := t.TempDir()
	live := filepath.Join(tmp, "Brand-New-Live")
	profile := filepath.Join(tmp, "profile-A")
	if err := os.MkdirAll(profile, 0755); err != nil {
		t.Fatal(err)
	}

	res, err := AtomicSymlinkSwap(live, profile)
	if err != nil {
		t.Fatal(err)
	}
	if res.Replaced {
		t.Fatal("expected Replaced=false when live did not exist")
	}
	if res.BackedUpTo != "" {
		t.Fatal("expected BackedUpTo empty when live did not exist")
	}
	li, _ := os.Lstat(live)
	if li.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected live to be a symlink")
	}
}

func TestAtomicSymlinkSwap_LeavesProfilesUntouched(t *testing.T) {
	tmp := t.TempDir()
	live := filepath.Join(tmp, "Live")
	profileA := filepath.Join(tmp, "profile-A")
	profileB := filepath.Join(tmp, "profile-B")
	for _, p := range []string{profileA, profileB} {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(profileA, "x"), []byte("A"), 0644)
	_ = os.WriteFile(filepath.Join(profileB, "x"), []byte("B"), 0644)
	_ = os.Symlink(profileA, live)

	if _, err := AtomicSymlinkSwap(live, profileB); err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(filepath.Join(profileA, "x")); string(got) != "A" {
		t.Fatalf("profileA mutated: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(profileB, "x")); string(got) != "B" {
		t.Fatalf("profileB mutated: %q", got)
	}
}

// TestCloneFile_HasDifferentInode verifies the CoW primitive produces a
// physically independent file (different inode), which is what gives us the
// isolation guarantees from the other tests.
func TestCloneFile_HasDifferentInode(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only CoW behavior")
	}
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.bin")
	dst := filepath.Join(tmp, "dst.bin")
	if err := os.WriteFile(src, bytes.Repeat([]byte{0}, 4096), 0644); err != nil {
		t.Fatal(err)
	}
	if err := CloneFile(src, dst); err != nil {
		t.Fatal(err)
	}
	srcInfo, _ := os.Stat(src)
	dstInfo, _ := os.Stat(dst)
	srcSys := srcInfo.Sys().(*syscall.Stat_t)
	dstSys := dstInfo.Sys().(*syscall.Stat_t)
	if srcSys.Ino == dstSys.Ino {
		t.Fatalf("clone has same inode as source (%d) - CoW not active?", srcSys.Ino)
	}
}
