package adapter

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// ProgressFunc reports per-file copy progress while CloneTree walks a
// directory. src is the path of the file just cloned; done is the cumulative
// bytes cloned so far across the whole tree; total is the running estimate of
// the total size, or -1 if unknown.
//
// Implementations should return quickly; CloneTree calls this for every file.
type ProgressFunc func(src string, done, total int64)

// CloneFile creates a copy-on-write clone of a single regular file via the
// macOS clonefile(2) syscall. It transparently falls back to a regular byte
// copy on any error (e.g. when the volume is not APFS, or clonefile returns
// ENOTSUP). The returned error is from the fallback path if clonefile itself
// failed for a non-fallback reason (e.g. source doesn't exist).
func CloneFile(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !srcInfo.Mode().IsRegular() {
		return fmt.Errorf("CloneFile: %s is not a regular file", src)
	}
	if err := unix.Clonefile(src, dst, 0); err == nil {
		return nil
	}
	return copyRegular(src, dst, srcInfo.Mode().Perm())
}

func copyRegular(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// CloneTreeOptions tunes CloneTree.
type CloneTreeOptions struct {
	// Progress, if set, is invoked once per cloned file. The byte totals are
	// the running sums across the whole tree.
	Progress ProgressFunc
	// SkipNames is an optional set of basenames to skip during the walk
	// (e.g. lock files, journal files, sockets). Matched against the
	// basename of every entry encountered.
	SkipNames map[string]struct{}
}

// CloneTree creates a copy-on-write snapshot of src as dst.
//
// Symlinks are preserved (recreated, not followed). Directories are recreated
// with their original permissions. Special files (sockets, devices) are
// skipped because they have no meaningful in a snapshot.
//
// The returned byte count is the sum of regular file sizes successfully
// cloned; the count is intended for progress reporting, not for billing.
func CloneTree(src, dst string, opts CloneTreeOptions) (int64, error) {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return 0, err
	}
	if !srcInfo.IsDir() {
		return 0, fmt.Errorf("CloneTree: %s is not a directory", src)
	}
	if err := os.MkdirAll(dst, srcInfo.Mode().Perm()); err != nil {
		return 0, err
	}

	var totalBytes int64
	err = filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		li, lerr := os.Lstat(path)
		if lerr != nil {
			return lerr
		}
		if opts.SkipNames != nil {
			if _, skip := opts.SkipNames[filepath.Base(path)]; skip {
				if li.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)

		if li.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_ = os.Remove(target) // best-effort
			return os.Symlink(linkTarget, target)
		}
		if li.IsDir() {
			return os.MkdirAll(target, li.Mode().Perm())
		}
		if !li.Mode().IsRegular() {
			// Sockets, devices, FIFOs, etc. - skip.
			return nil
		}
		if err := CloneFile(path, target); err != nil {
			return err
		}
		totalBytes += li.Size()
		if opts.Progress != nil {
			opts.Progress(path, totalBytes, -1)
		}
		return nil
	})
	return totalBytes, err
}

// AtomicSymlinkSwapResult describes what happened during an atomic symlink
// swap. BackedUpTo is non-empty when the previous real directory at
// livePath was preserved as a sibling before being replaced with a symlink;
// the caller may choose to clean it up or keep it as a safety net.
type AtomicSymlinkSwapResult struct {
	// BackedUpTo is the path the original real directory was moved to
	// (suffixed with .real-bak-<unix-nano>). Empty when the previous entry
	// at livePath was already a symlink or did not exist.
	BackedUpTo string
	// Replaced indicates whether a previous entry at livePath was replaced.
	Replaced bool
}

// AtomicSymlinkSwap replaces livePath with a symlink pointing at newTarget.
//
// If livePath is currently a real directory, the directory is first renamed
// aside (suffixed with .real-bak-<unix-nano>) and the result is reported via
// AtomicSymlinkSwapResult.BackedUpTo. If livePath is already a symlink, it
// is removed and the swap is fully atomic via rename(2) of a temporary
// symlink in the same directory. If livePath does not exist, the symlink is
// created fresh.
//
// The new symlink is created in the same directory as livePath so the final
// rename(2) is atomic. If newTarget is on a different volume, the absolute
// path is used and the symlink is created directly at livePath (not via the
// rename dance) - in that case the swap is not atomic and a partial failure
// could leave livePath missing.
func AtomicSymlinkSwap(livePath, newTarget string) (AtomicSymlinkSwapResult, error) {
	var res AtomicSymlinkSwapResult
	dir := filepath.Dir(livePath)
	relTarget, err := filepath.Rel(dir, newTarget)
	if err != nil || filepath.IsAbs(relTarget) {
		relTarget = newTarget
	}

	li, lerr := os.Lstat(livePath)
	switch {
	case lerr == nil:
		res.Replaced = true
		if li.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(livePath); err != nil {
				return res, fmt.Errorf("could not remove old symlink: %w", err)
			}
		} else if li.IsDir() {
			back := fmt.Sprintf("%s.real-bak-%d", livePath, time.Now().UnixNano())
			if err := os.Rename(livePath, back); err != nil {
				return res, fmt.Errorf("could not move real dir aside: %w", err)
			}
			res.BackedUpTo = back
		} else {
			if err := os.Remove(livePath); err != nil {
				return res, fmt.Errorf("could not remove existing entry: %w", err)
			}
		}
	case os.IsNotExist(lerr):
		// nothing to do
	default:
		return res, lerr
	}

	// Build the symlink. If we can keep it in the same dir (relTarget is
	// relative and lives under dir), do the atomic rename dance.
	tmp := livePath + ".swap"
	_ = os.Remove(tmp)
	if relTarget != newTarget {
		// relative target, same volume - safe to use the rename dance
		if err := os.Symlink(relTarget, tmp); err != nil {
			_ = tryRestore(livePath, res.BackedUpTo)
			return res, err
		}
		if err := os.Rename(tmp, livePath); err != nil {
			_ = os.Remove(tmp)
			_ = tryRestore(livePath, res.BackedUpTo)
			return res, err
		}
		return res, nil
	}

	// absolute target - create the symlink directly
	if err := os.Symlink(relTarget, livePath); err != nil {
		_ = tryRestore(livePath, res.BackedUpTo)
		return res, err
	}
	return res, nil
}

func tryRestore(livePath, backedUpTo string) error {
	if backedUpTo == "" {
		return nil
	}
	return os.Rename(backedUpTo, livePath)
}
