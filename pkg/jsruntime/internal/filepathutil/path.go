package filepathutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolvePath returns the canonical path for path. When allowMissing is true,
// missing trailing components are appended to the nearest existing canonical
// parent. This keeps equivalent symlinked spellings such as /var and
// /private/var comparable without following a missing final component.
func ResolvePath(path string, allowMissing bool) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve JavaScript path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	if !allowMissing || !os.IsNotExist(err) {
		return "", err
	}

	missing := make([]string, 0, 2)
	current := absolute
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return absolute, nil
		}
		missing = append(missing, filepath.Base(current))
		current = parent
		resolved, parentErr := filepath.EvalSymlinks(current)
		if parentErr == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(parentErr) {
			return "", parentErr
		}
	}
}

func RelativeToBase(baseDir, name string) (string, error) {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return "", fmt.Errorf("resolve JavaScript path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if !WithinBase(baseDir, absolute) {
		return "", fmt.Errorf("JavaScript path escapes BaseDir: %s", name)
	}
	relative, err := filepath.Rel(baseDir, absolute)
	if err != nil {
		return "", fmt.Errorf("make JavaScript path relative to BaseDir: %w", err)
	}
	if relative == "." {
		return ".", nil
	}
	return relative, nil
}

func WithinBase(baseDir, path string) bool {
	relative, err := filepath.Rel(baseDir, path)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
