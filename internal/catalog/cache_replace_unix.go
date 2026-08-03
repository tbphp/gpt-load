//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package catalog

import "os"

// replaceCatalogFileAtomic uses same-directory rename, whose replacement is
// atomic on the Unix platforms supported by this package.
func replaceCatalogFileAtomic(temporaryPath, finalPath string) error {
	if err := requireSiblingCatalogPaths(temporaryPath, finalPath); err != nil {
		return err
	}
	return os.Rename(temporaryPath, finalPath)
}
